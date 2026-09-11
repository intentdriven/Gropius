package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// fakeProvisioner is the runtime seam: a provisioner that reports the stages a
// test tells it to and never touches the network.
type fakeProvisioner struct {
	mu        sync.Mutex
	status    runtime.SetupStatus
	installed bool
	// ensure is what Ensure does, with the provisioner handed back so it can
	// report stages as it goes.
	ensure func(p *fakeProvisioner) error
	calls  int
}

func (p *fakeProvisioner) Status() runtime.SetupStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Ready = p.installed
	return s
}

func (p *fakeProvisioner) Installed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.installed
}

func (p *fakeProvisioner) Ensure(context.Context) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.ensure == nil {
		p.report(runtime.StageReady, 3)
		p.mu.Lock()
		p.installed = true
		p.mu.Unlock()
		return nil
	}
	return p.ensure(p)
}

func (p *fakeProvisioner) report(stage runtime.SetupStage, done int) {
	p.mu.Lock()
	p.status = runtime.SetupStatus{Stage: stage, Step: done, Steps: 3}
	p.mu.Unlock()
}

// lockedBuffer is a buffer a test reads while the progress loop writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Provisioning in the foreground reports a PROPORTION, read from the same
// SetupStatus the panel polls. A spinner is what it replaces: the failure being
// reported is an operator who cannot tell working from stuck.
func TestProvisioningPrintsTheProportionItReads(t *testing.T) {
	out := &lockedBuffer{}
	release := make(chan struct{})

	p := &fakeProvisioner{ensure: func(p *fakeProvisioner) error {
		p.report(runtime.StageMLX, 2)
		<-release
		p.report(runtime.StageReady, 3)
		p.mu.Lock()
		p.installed = true
		p.mu.Unlock()
		return nil
	}}

	done := make(chan error, 1)
	go func() {
		done <- provisionWithProgress(context.Background(), p, Terminal{Out: out}, time.Millisecond)
	}()

	waitFor(t, func() bool { return strings.Contains(out.String(), string(runtime.StageMLX)) },
		"the progress line never named the stage that was running")
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("provisionWithProgress: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "66%") {
		t.Errorf("the progress never reported the proportion two stages of three make:\n%s", got)
	}
	if !strings.Contains(got, "100%") {
		t.Errorf("the finished run never reported itself complete:\n%s", got)
	}
}

// A provisioning failure travels out of the loop rather than being rendered as
// success.
func TestProvisioningCarriesItsFailureOut(t *testing.T) {
	boom := errors.New("the MLX wheel would not install")
	p := &fakeProvisioner{ensure: func(p *fakeProvisioner) error {
		p.report(runtime.StageFailed, 2)
		return boom
	}}
	err := provisionWithProgress(context.Background(), p, Terminal{Out: discardWriter{}}, time.Millisecond)
	if !errors.Is(err, boom) {
		t.Errorf("provisionWithProgress = %v, want the provisioner's own failure", err)
	}
}

// A Mac where provisioning already completed is REPAIRED rather than
// reinstalled, and the report says which of the two it did.
func TestInstallSaysWhetherItRepairedOrFoundNothingToDo(t *testing.T) {
	t.Run("a complete runtime repairs nothing and says so", func(t *testing.T) {
		env, ie, out, _ := installFixture(t)
		ie.Runtime.(*fakeProvisioner).installed = true
		completeRuntime(t, ie.Paths)

		if code := runInstall(env, nil, ie); code != ExitOK {
			t.Fatalf("exit = %d, want %d", code, ExitOK)
		}
		if !strings.Contains(out.String(), "nothing was reinstalled") {
			t.Errorf("install did not say it left a complete runtime alone:\n%s", out)
		}
	})

	t.Run("a runtime missing a piece repairs it and says which", func(t *testing.T) {
		env, ie, out, _ := installFixture(t)
		completeRuntime(t, ie.Paths)
		if err := os.RemoveAll(ie.Paths.Venv); err != nil {
			t.Fatal(err)
		}

		if code := runInstall(env, nil, ie); code != ExitOK {
			t.Fatalf("exit = %d, want %d", code, ExitOK)
		}
		got := out.String()
		if strings.Contains(got, "nothing was reinstalled") {
			t.Errorf("install reported nothing to do although the virtualenv was missing:\n%s", got)
		}
		if !strings.Contains(got, "MLX") {
			t.Errorf("install did not name what it repaired:\n%s", got)
		}
	})
}

// A running copy is asked to quit only when there is an installed bundle to
// replace. On a first install there is nothing running to quit, and a warning
// about a quit that failed would be the first thing a new user read.
func TestInstallQuitsOnlyWhatItIsAboutToReplace(t *testing.T) {
	env, ie, _, errOut := installFixture(t)
	quits := 0
	ie.Quit = func() error { quits++; return errors.New("Gropius is not running") }

	if code := runInstall(env, nil, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if quits != 0 {
		t.Error("a first install asked a copy that is not there to quit")
	}
	if strings.Contains(errOut.String(), "could not be asked to quit") {
		t.Errorf("a first install warned about quitting nothing:\n%s", errOut)
	}

	// An upgrade over an installed bundle does ask, because Launch Services
	// activates a running process instead of starting the new binary.
	env, ie, _, _ = installFixture(t)
	bundleAt(t, ie.Dest, "installed")
	ie.Quit = func() error { quits++; return nil }
	if code := runInstall(env, nil, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if quits != 1 {
		t.Errorf("an upgrade asked %d times to quit the running copy, want 1", quits)
	}
}

// A partial failure exits non-zero, names the stage and the cause, and names
// the command that retries it.
func TestInstallNamesTheStageThatFailedAndHowToRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		breakIt func(ie *InstallEnv)
		stage   string
	}{
		{
			name: "placing the application",
			breakIt: func(ie *InstallEnv) {
				ie.Place = func(string, string) error { return errors.New("no space left on device") }
			},
			stage: stagePlace,
		},
		{
			name: "the MLX runtime",
			breakIt: func(ie *InstallEnv) {
				ie.Runtime = &fakeProvisioner{ensure: func(p *fakeProvisioner) error {
					p.report(runtime.StageFailed, 1)
					return errors.New("no space left on device")
				}}
			},
			stage: stageRuntime,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, ie, _, errOut := installFixture(t)
			tc.breakIt(&ie)

			if code := runInstall(env, nil, ie); code != ExitFailed {
				t.Fatalf("exit = %d, want %d for a failed install", code, ExitFailed)
			}
			got := errOut.String()
			for _, want := range []string{tc.stage, "no space left on device", "gropius install"} {
				if !strings.Contains(got, want) {
					t.Errorf("the failure does not carry %q:\n%s", want, got)
				}
			}
		})
	}
}

// The firewall grant is asked for once, through the system authorisation
// panel, and a refusal is reported with the commands that grant it by hand
// rather than failing the install: the app serves loopback either way, and a
// person who declined the panel has not lost the install.
func TestInstallAsksForTheFirewallGrantOnceAndSurvivesARefusal(t *testing.T) {
	env, ie, _, errOut := installFixture(t)
	asked := 0
	ie.Firewall = func(string) error {
		asked++
		return errors.New("the authorisation was declined")
	}

	if code := runInstall(env, nil, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d: a declined firewall grant is not a failed install", code, ExitOK)
	}
	if asked != 1 {
		t.Errorf("the panel was raised %d times, want exactly 1", asked)
	}
	got := errOut.String()
	if !strings.Contains(got, "--add") || !strings.Contains(got, "--unblockapp") {
		t.Errorf("a refused grant did not name the commands that make it by hand:\n%s", got)
	}
}

// The install says when the bin directory is not on the search path.
func TestInstallSaysWhenTheCommandWillNotResolve(t *testing.T) {
	env, ie, out, _ := installFixture(t)
	ie.PathEnv = "/usr/bin:/bin"

	if code := runInstall(env, nil, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Errorf("install left a link that resolves for nobody and said nothing:\n%s", out)
	}

	// And says nothing about it when it is already there.
	env, ie, out, _ = installFixture(t)
	ie.PathEnv = filepath.Join(ie.Home, ".local", "bin")
	if code := runInstall(env, nil, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if strings.Contains(out.String(), "export PATH=") {
		t.Errorf("install told an account that already has the directory on its PATH to add it:\n%s", out)
	}
}

// --place-only stops after the swap, and says what it did not do. It is what
// the bootstrap passes where there is no console to answer an authorisation
// panel and no business provisioning a runtime.
func TestInstallPlaceOnlyStopsAfterTheSwapAndSaysSo(t *testing.T) {
	env, ie, out, _ := installFixture(t)
	asked := 0
	ie.Firewall = func(string) error { asked++; return nil }

	if code := runInstall(env, []string{"--place-only"}, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if asked != 0 {
		t.Error("--place-only raised the authorisation panel")
	}
	if p := ie.Runtime.(*fakeProvisioner); p.calls != 0 {
		t.Error("--place-only provisioned the runtime")
	}
	if !strings.Contains(out.String(), "gropius install") {
		t.Errorf("--place-only did not name what finishes the install:\n%s", out)
	}
}

// The closing lines the bootstrap used to print are still printed, now by the
// verb that finishes the install: where the app is, and what to do next.
func TestInstallSaysWhatToDoNext(t *testing.T) {
	env, ie, out, _ := installFixture(t)

	if code := runInstall(env, nil, ie); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	got := out.String()
	for _, want := range []string{"menu bar", "control panel"} {
		if !strings.Contains(got, want) {
			t.Errorf("the finished install does not say where Gropius is or what to do next (%q missing):\n%s", want, got)
		}
	}
}

// installFixture is an install with every seam answered from a temporary
// directory: no panel, no network, no Mac.
func installFixture(t *testing.T) (Env, InstallEnv, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env, out, errOut := testEnv()

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "Library", "Application Support", "Gropius")
	paths := config.NewPaths(root)
	dest := filepath.Join(home, "Applications", "Gropius.app")
	bundle := bundleAt(t, filepath.Join(dir, "verified", "Gropius.app"), "new")

	ie := InstallEnv{
		Paths:    paths,
		Home:     home,
		Bundle:   bundle,
		Dest:     dest,
		PathEnv:  filepath.Join(home, ".local", "bin"),
		Place:    PlaceBundle,
		Quit:     func() error { return nil },
		Firewall: func(string) error { return nil },
		Runtime:  &fakeProvisioner{},
		Launch:   func(string) error { return nil },
		Serving:  func() bool { return true },
		Poll:     time.Millisecond,
	}
	return env, ie, out, errOut
}

// completeRuntime lays down the files a provisioned runtime leaves behind, so
// the report can be asked what it found.
func completeRuntime(t *testing.T, paths config.Paths) {
	t.Helper()
	for _, f := range []string{paths.UV(), paths.VenvPython()} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// waitFor blocks until cond holds, failing the test rather than hanging when it
// never does.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

// discardWriter is a writer that keeps nothing, for a test that reads no output.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
