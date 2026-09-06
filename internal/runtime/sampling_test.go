package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }

// recordingPython stands in for the venv interpreter and writes each argument
// it was given on its own line, so a test can see the argument vector exactly
// as the kernel received it — including whether a flag and its value are two
// elements or one string.
func recordingPython(t *testing.T) (paths config.Paths, argvFile string) {
	t.Helper()
	root := t.TempDir()
	paths = config.NewPaths(root)
	if err := os.MkdirAll(filepath.Dir(paths.VenvPython()), 0o755); err != nil {
		t.Fatal(err)
	}
	argvFile = filepath.Join(root, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + argvFile + "\nexit 0\n"
	if err := os.WriteFile(paths.VenvPython(), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Logs, 0o755); err != nil {
		t.Fatal(err)
	}
	return paths, argvFile
}

func launchAndReadArgv(t *testing.T, s config.Sampling) []string {
	t.Helper()
	paths, argvFile := recordingPython(t)
	l := &ExecLauncher{Paths: paths, LogDir: paths.Logs}
	p, err := l.Launch(context.Background(), Spec{
		RepoID:    "org/name",
		ModelPath: t.TempDir(),
		Port:      1234,
		Sampling:  s,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the recording interpreter never exited")
	}
	b, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading the recorded argument vector: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// flagValue returns the element after name, and whether name is present.
func flagValue(argv []string, name string) (string, bool) {
	for i, a := range argv {
		if a == name {
			if i+1 < len(argv) {
				return argv[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

// The default reaches the request by being the model server's own start-up
// value, so it has to arrive as a launch flag — and as its own argv element,
// never spliced into a string.
func TestLaunchPassesSamplingDefaultsAsSeparateFlags(t *testing.T) {
	argv := launchAndReadArgv(t, config.Sampling{
		Temperature: fptr(0.7),
		TopP:        fptr(0.95),
		TopK:        iptr(40),
		MinP:        fptr(0.05),
		MaxTokens:   iptr(8192),
	})

	want := map[string]string{
		"--temp":       "0.7",
		"--top-p":      "0.95",
		"--top-k":      "40",
		"--min-p":      "0.05",
		"--max-tokens": "8192",
	}
	for flag, value := range want {
		got, ok := flagValue(argv, flag)
		if !ok {
			t.Errorf("%s is missing from the argument vector %v", flag, argv)
			continue
		}
		if got != value {
			t.Errorf("%s = %q, want %q", flag, got, value)
		}
	}
}

// A parameter left blank must pass no flag at all, so the model server keeps
// its own default rather than being handed a zero that means something else.
func TestLaunchPassesNoFlagForAnUnsetParameter(t *testing.T) {
	argv := launchAndReadArgv(t, config.Sampling{Temperature: fptr(0)})

	if got, ok := flagValue(argv, "--temp"); !ok || got != "0" {
		t.Errorf("--temp = %q (present=%v), want an explicit 0 — zero is a real temperature", got, ok)
	}
	for _, flag := range []string{"--top-p", "--top-k", "--min-p", "--max-tokens"} {
		if _, ok := flagValue(argv, flag); ok {
			t.Errorf("%s was passed for an unset parameter: %v", flag, argv)
		}
	}
}

// The settings endpoint refuses an out-of-range value, but the launcher is the
// last place the value can still do harm — the model server rejects every
// request that omits a parameter whose start-up value it will not accept. It
// checks again rather than trusting whoever built the Spec.
func TestLaunchDropsAnOutOfRangeSamplingValue(t *testing.T) {
	argv := launchAndReadArgv(t, config.Sampling{
		Temperature: fptr(-1),
		TopP:        fptr(2),
		MaxTokens:   iptr(4096),
	})

	for _, flag := range []string{"--temp", "--top-p"} {
		if _, ok := flagValue(argv, flag); ok {
			t.Errorf("%s was passed with a value the model server rejects: %v", flag, argv)
		}
	}
	if got, _ := flagValue(argv, "--max-tokens"); got != "4096" {
		t.Errorf("--max-tokens = %q, want the sound value kept", got)
	}
}

// Every accepted value is at least zero, so no rendered value can open with a
// dash and be read as another flag.
func TestNoRenderedSamplingValueLooksLikeAFlag(t *testing.T) {
	args := samplingArgs(config.Sampling{
		Temperature: fptr(0), TopP: fptr(1), TopK: iptr(0),
		MinP: fptr(0), MaxTokens: iptr(0),
	})
	if len(args) != 10 {
		t.Fatalf("samplingArgs = %v, want five flags and five values", args)
	}
	for i := 1; i < len(args); i += 2 {
		if strings.HasPrefix(args[i], "-") {
			t.Errorf("value %q for %s would be read as a flag", args[i], args[i-1])
		}
	}
}

// A per-model override applies to that model's server and no other, and the
// pool reads the effective set when it starts a process — so a saved change
// reaches the next load without the pool being rebuilt.
func TestPoolLaunchesEachModelWithItsOwnSamplingDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.Sampling = config.Sampling{Temperature: fptr(0.7)}
	cfg.ModelSampling = map[string]config.Sampling{
		"org/special": {Temperature: fptr(0.1)},
	}

	var mu sync.Mutex
	live := cfg

	l := newFakeLauncher()
	src := &fakeSource{models: map[string]int64{"org/plain": 1 << 20, "org/special": 1 << 20}}
	p := newTestPool(t, l, src, PoolOptions{
		MaxResidentBytes: 1 << 30,
		SamplingFor: func(repoID string) config.Sampling {
			mu.Lock()
			defer mu.Unlock()
			return live.EffectiveSampling(repoID)
		},
	})

	for _, id := range []string{"org/plain", "org/special"} {
		_, release, err := p.Acquire(context.Background(), id)
		if err != nil {
			t.Fatalf("Acquire(%s): %v", id, err)
		}
		release()
	}

	if got := l.specFor("org/plain").Sampling.Temperature; got == nil || *got != 0.7 {
		t.Errorf("org/plain launched with temperature %v, want the machine-wide 0.7", got)
	}
	if got := l.specFor("org/special").Sampling.Temperature; got == nil || *got != 0.1 {
		t.Errorf("org/special launched with temperature %v, want its override 0.1", got)
	}

	// Save a new machine-wide default. The model already running keeps the one
	// it was launched with; the next load picks the new one up.
	mu.Lock()
	live.Sampling = config.Sampling{Temperature: fptr(1.2)}
	mu.Unlock()

	if got := l.specFor("org/plain").Sampling.Temperature; got == nil || *got != 0.7 {
		t.Errorf("a resident model's launch arguments changed under it: %v", got)
	}
	if err := p.Unload("org/plain"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	_, release, err := p.Acquire(context.Background(), "org/plain")
	if err != nil {
		t.Fatalf("Acquire after reload: %v", err)
	}
	release()
	if got := l.specFor("org/plain").Sampling.Temperature; got == nil || *got != 1.2 {
		t.Errorf("temperature after reload = %v, want the saved 1.2", got)
	}
}
