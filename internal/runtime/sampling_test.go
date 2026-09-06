package runtime

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
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

// Every accepted value is at least zero, so no rendered value may open with a
// dash and be read as another flag. Negative zero is the awkward case: it
// passes a "must be at least zero" check and strconv renders it "-0".
func TestNoRenderedSamplingValueLooksLikeAFlag(t *testing.T) {
	negZero := math.Copysign(0, -1)
	for _, s := range []config.Sampling{
		{Temperature: fptr(0), TopP: fptr(1), TopK: iptr(0), MinP: fptr(0), MaxTokens: iptr(0)},
		{Temperature: fptr(negZero), TopP: fptr(negZero), MinP: fptr(negZero)},
	} {
		args := samplingArgs(s)
		if len(args) == 0 {
			t.Fatalf("samplingArgs(%+v) rendered nothing", s)
		}
		for i := 1; i < len(args); i += 2 {
			if strings.HasPrefix(args[i], "-") {
				t.Errorf("value %q for %s would be read as a flag", args[i], args[i-1])
			}
		}
	}
	if args := samplingArgs(config.Sampling{
		Temperature: fptr(0), TopP: fptr(1), TopK: iptr(0), MinP: fptr(0), MaxTokens: iptr(0),
	}); len(args) != 10 {
		t.Errorf("samplingArgs = %v, want five flags and five values", args)
	}

	// Decimal notation, not scientific: the flag is read by a person in the
	// per-model log as often as by argparse, and "1e-05" is a poor way to
	// report a min-p.
	small := samplingArgs(config.Sampling{MinP: fptr(0.00001)})
	if len(small) != 2 || small[1] != "0.00001" {
		t.Errorf("samplingArgs = %v, want a plain decimal value", small)
	}
}

// Every sampling parameter the configuration holds must have a launch flag, or
// it is a default the panel accepts, stores and never applies — silently.
func TestEverySamplingParameterHasALaunchFlag(t *testing.T) {
	for _, b := range config.SamplingBounds() {
		if _, ok := samplingFlags[b.Field]; !ok {
			t.Errorf("the configuration holds a %q default with no launch flag, so saving it does nothing", b.Field)
		}
	}
	for field := range samplingFlags {
		found := false
		for _, b := range config.SamplingBounds() {
			if b.Field == field {
				found = true
			}
		}
		if !found {
			t.Errorf("a launch flag is rendered for %q, which is not a sampling default", field)
		}
	}
}

// The flag spellings and the accepted ranges were read off one version of the
// model server's source. Nothing else ties them to it, and a wrong flag name
// makes argparse exit on every model launch while the suite stays green — so
// a version bump has to come past this test.
func TestSamplingFlagsWereVerifiedAgainstThePinnedServer(t *testing.T) {
	if mlxLMVersion != samplingFlagsVerifiedAgainst {
		t.Fatalf("mlx-lm is pinned at %s but the sampling launch flags and ranges were read from %s — "+
			"re-read that release's mlx_lm/server.py argument parser and mlx_lm/sample_utils.py, update "+
			".abcd/development/research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md, then move this constant",
			mlxLMVersion, samplingFlagsVerifiedAgainst)
	}
	req, err := os.ReadFile("mlx-requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(req), "mlx-lm=="+samplingFlagsVerifiedAgainst+" ") {
		t.Errorf("mlx-requirements.txt does not pin mlx-lm==%s, which is the version the sampling flags were read from",
			samplingFlagsVerifiedAgainst)
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

	// Saving must not restart anything. specFor only ever holds the snapshot
	// taken at launch, so asserting on it could not fail; the launch count can.
	if l.launchCount() != 2 {
		t.Errorf("a saved change relaunched a model: %d launches, want 2", l.launchCount())
	}
	if err := p.Unload("org/plain"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	_, release, err := p.Acquire(context.Background(), "org/plain")
	if err != nil {
		t.Fatalf("Acquire after reload: %v", err)
	}
	release()
	if l.launchCount() != 3 {
		t.Errorf("reloading did not launch a new process: %d launches, want 3", l.launchCount())
	}
	if got := l.specFor("org/plain").Sampling.Temperature; got == nil || *got != 1.2 {
		t.Errorf("temperature after reload = %v, want the saved 1.2", got)
	}
}

// The command line is where an out-of-range integer would do its damage, so the
// rendering is checked at the extremes the type allows rather than only at the
// values the settings endpoint lets through.
func TestIntegerFlagsRenderExactlyAndNeverNegative(t *testing.T) {
	args := samplingArgs(config.Sampling{
		TopK:      iptr(1024),
		MaxTokens: iptr(config.MaxCompletionTokens),
	})
	if got, _ := flagValue(args, "--top-k"); got != "1024" {
		t.Errorf("--top-k = %q, want 1024", got)
	}
	want := strconv.Itoa(config.MaxCompletionTokens)
	if got, _ := flagValue(args, "--max-tokens"); got != want {
		t.Errorf("--max-tokens = %q, want %s", got, want)
	}

	// Rendered from the integer field, not from the float64 the bounds are
	// compared in. This is the property that does not depend on the
	// architecture: narrowing an out-of-range float64 saturates on arm64 and
	// wraps to a negative on amd64, so a test that only feeds extreme values
	// through Sampling would pass here and fail on the other build.
	if got := formatSamplingValue(config.SamplingValue{
		Field: "max_tokens", Integer: true, Int: 8192, Number: 1234,
	}); got != "8192" {
		t.Errorf("formatSamplingValue = %q, want the integer field's 8192 — "+
			"rendering through the float64 is implementation-defined out of range", got)
	}

	// Values beyond what the endpoint accepts are dropped, not rendered — and
	// certainly never rendered as a negative, which argparse would take.
	for _, v := range []int{1<<63 - 1, -(1 << 62)} {
		for _, a := range samplingArgs(config.Sampling{MaxTokens: iptr(v), TopK: iptr(v)}) {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
				t.Errorf("max_tokens/top_k %d rendered %q, which argparse reads as a value", v, a)
			}
		}
	}
}
