package lifecycle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// uninstallFixture lays down an installation in a temporary directory: a
// bundle, the private runtime, the settings, the registry, the logs, the
// statistics, the per-user link, and a model that must survive all of it.
func uninstallFixture(t *testing.T) (Env, UninstallEnv, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env, out, errOut := testEnv()

	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	root := filepath.Join(home, "Library", "Application Support", "Gropius")
	paths := config.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{paths.Config, paths.State, paths.UV(), paths.VenvPython(),
		filepath.Join(paths.Logs, "gropius.log"), filepath.Join(paths.Stats, "2026-09.json"),
		filepath.Join(paths.Python, "cpython-3.12", "bin", "python3"),
		filepath.Join(paths.Models, "mlx-community", "a-model", "weights.safetensors"),
		filepath.Join(paths.HFCache, "blob"),
	} {
		writeFileAt(t, f, "x")
	}

	bundle := bundleAt(t, filepath.Join(home, "Applications", "Gropius.app"), "installed")
	binary := filepath.Join(bundle, binaryInBundle)
	link, err := linkCommand(home, binary)
	if err != nil {
		t.Fatal(err)
	}

	return env, UninstallEnv{
		Paths:    paths,
		Home:     home,
		Bundles:  []string{bundle},
		Link:     link,
		Binary:   binary,
		Terminal: true,
		Firewall: func(string) error { return nil },
		OwnerOf:  func(string) (int, error) { return os.Getuid(), nil },
		Uid:      os.Getuid(),
	}, out, errOut
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Uninstall removes the application, the runtime, the settings, the registry,
// the logs and the link — and leaves the models, which are the expensive
// thing, saying how much space they take and what removes them.
func TestUninstallRemovesTheApplicationAndLeavesTheModels(t *testing.T) {
	env, ue, out, _ := uninstallFixture(t)

	if code := runUninstall(env, nil, ue); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, out)
	}

	for _, gone := range []string{ue.Bundles[0], ue.Paths.Venv, ue.Paths.Python, ue.Paths.Bin,
		ue.Paths.Config, ue.Paths.State, ue.Paths.Logs, ue.Paths.Stats, ue.Link} {
		if exists(gone) {
			t.Errorf("%s is still there", abbreviate(gone, ue.Home))
		}
	}
	for _, kept := range []string{ue.Paths.Models, ue.Paths.HFCache} {
		if !exists(kept) {
			t.Errorf("%s was removed; the downloaded models are what uninstall leaves", abbreviate(kept, ue.Home))
		}
	}
	got := out.String()
	if !strings.Contains(got, "--purge") {
		t.Errorf("the output does not name the flag that removes the models:\n%s", got)
	}
	if !strings.Contains(got, "B") {
		t.Errorf("the output does not state the size of what it left:\n%s", got)
	}
}

// --purge without a terminal and without --yes deletes NOTHING and names the
// flag. Under a piped bootstrap standard input is the rest of the installer, so
// there is nobody to ask and nothing to read.
func TestPurgeRefusesWithoutATerminalUnlessToldYes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		args     []string
		deletes  bool
	}{
		{"no terminal, no --yes", false, []string{"--purge"}, false},
		{"no terminal, --yes", false, []string{"--purge", "--yes"}, true},
		{"a terminal", true, []string{"--purge"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, ue, out, errOut := uninstallFixture(t)
			ue.Terminal = tc.terminal

			code := runUninstall(env, tc.args, ue)
			modelsGone := !exists(ue.Paths.Models)
			if modelsGone != tc.deletes {
				t.Errorf("models removed = %v, want %v", modelsGone, tc.deletes)
			}
			if tc.deletes {
				return
			}
			if code == ExitOK {
				t.Errorf("a refused purge exited %d, which reads as having done the work", code)
			}
			if exists(ue.Bundles[0]) == false {
				t.Error("a refused purge removed the application; it must delete nothing at all")
			}
			if !strings.Contains(errOut.String(), "--yes") {
				t.Errorf("the refusal does not name the flag that would answer it:\n%s%s", out, errOut)
			}
		})
	}
}

// The firewall entry is machine-wide state with no per-account route, so its
// removal is the one authorisation panel. A refusal leaves everything else
// removed and reports the entry as the one thing remaining, with the command.
func TestUninstallSurvivesARefusedAuthorisation(t *testing.T) {
	env, ue, out, _ := uninstallFixture(t)
	asked := 0
	ue.Firewall = func(string) error {
		asked++
		return errors.New("the authorisation was declined")
	}

	if code := runUninstall(env, nil, ue); code != ExitOK {
		t.Fatalf("exit = %d, want %d: a declined panel is not a failed uninstall", code, ExitOK)
	}
	if asked != 1 {
		t.Errorf("the panel was raised %d times, want exactly 1", asked)
	}
	if exists(ue.Bundles[0]) {
		t.Error("a refused authorisation stopped the rest of the removal")
	}
	got := out.String()
	if !strings.Contains(got, "--remove") {
		t.Errorf("the output does not name the command that removes the firewall entry:\n%s", got)
	}
}

// Under shared-cache mode this account's own directory goes and the shared root
// is not touched. The output names what remains there, its size, the accounts
// it belongs to counted rather than named, and the one deliberate command that
// removes it.
func TestSharedCacheUninstallLeavesTheSharedRootAlone(t *testing.T) {
	env, ue, out, _ := uninstallFixture(t)

	// The shared layout: models in the shared root, everything of this
	// account's own in its own directory.
	shared := filepath.Join(t.TempDir(), "Shared", "Gropius")
	ue.SharedRoot = shared
	ue.Paths.Models = filepath.Join(shared, "models")
	ue.Paths.HFCache = filepath.Join(shared, "hf", "hub")
	writeFileAt(t, filepath.Join(ue.Paths.Models, "mlx-community", "a-model", "weights.safetensors"), "ours")
	other := filepath.Join(ue.Paths.Models, "mlx-community", "another-model", "weights.safetensors")
	writeFileAt(t, other, "another account's")
	ue.OwnerOf = func(path string) (int, error) {
		if strings.Contains(path, "another-model") {
			return os.Getuid() + 1, nil
		}
		return os.Getuid(), nil
	}

	if code := runUninstall(env, nil, ue); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !exists(shared) || !exists(other) {
		t.Error("the shared root was touched; it holds every account's models")
	}
	if exists(ue.Paths.Config) {
		t.Error("this account's own directory was left in place")
	}
	got := out.String()
	for _, want := range []string{"shared", "1 other account", "rm -rf"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "another-model") {
		t.Errorf("the output names another account's models rather than counting the accounts:\n%s", got)
	}
}

// --purge under shared-cache mode removes only what this account owns, which
// the sticky bit on the shared directory's 3775 mode makes the only removal the
// filesystem permits anyway. The output separates deleted from retained.
func TestSharedPurgeRemovesOnlyWhatThisAccountOwns(t *testing.T) {
	env, ue, out, _ := uninstallFixture(t)

	shared := filepath.Join(t.TempDir(), "Shared", "Gropius")
	ue.SharedRoot = shared
	ue.Paths.Models = filepath.Join(shared, "models")
	ue.Paths.HFCache = filepath.Join(shared, "hf", "hub")
	ours := filepath.Join(ue.Paths.Models, "mlx-community", "ours", "weights.safetensors")
	theirs := filepath.Join(ue.Paths.Models, "mlx-community", "theirs", "weights.safetensors")
	writeFileAt(t, ours, "ours")
	writeFileAt(t, theirs, "theirs")
	ue.OwnerOf = func(path string) (int, error) {
		if strings.Contains(path, "theirs") {
			return os.Getuid() + 1, nil
		}
		return os.Getuid(), nil
	}

	if code := runUninstall(env, []string{"--purge", "--yes"}, ue); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if exists(ours) {
		t.Error("this account's own model survived --purge")
	}
	if !exists(theirs) {
		t.Error("another account's model was removed; only what this account owns may go")
	}
	got := out.String()
	for _, want := range []string{"removed", "1 other account"} {
		if !strings.Contains(got, want) {
			t.Errorf("the output does not separate what was deleted from what was not (%q missing):\n%s", want, got)
		}
	}
}

// GROPIUS_ROOT is never a deletion path. A directory any local account can
// pre-create as a symlink would otherwise choose what is deleted, so the
// removal acts on the fixed locations this account's install uses — and says so
// rather than leaving a reader to expect the variable to be honoured.
func TestGropiusRootIsNeverADeletionPath(t *testing.T) {
	decoy := t.TempDir()
	witness := filepath.Join(decoy, "models", "someone-elses-data")
	writeFileAt(t, witness, "not ours to delete")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROPIUS_ROOT", decoy)

	env, _, _ := testEnv()
	ue, err := liveUninstallEnv(env)
	if err != nil {
		t.Fatal(err)
	}
	// The bundle locations and the panel are the two things a test may not
	// exercise for real; everything the case is about — which root is resolved,
	// and what is said about the one that was not — is left live.
	ue.Bundles = nil
	ue.Firewall = func(string) error { return nil }
	ue.Terminal = false

	out := &bytes.Buffer{}
	env.Out = out
	if code := runUninstall(env, nil, ue); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !exists(witness) {
		t.Fatal("uninstall deleted what GROPIUS_ROOT named")
	}
	if !strings.Contains(out.String(), "GROPIUS_ROOT") {
		t.Errorf("the output does not name the root it did not remove:\n%s", out)
	}
}

// The removal is in-process and acts on fixed locations. This is the pure half
// of the shared purge: given who owns what, which entries may go.
func TestOnlyEntriesThisAccountOwnsArePurged(t *testing.T) {
	owners := map[string]int{"ours": 501, "theirs": 502, "also-ours": 501}
	ownerOf := func(path string) (int, error) {
		uid, ok := owners[filepath.Base(path)]
		if !ok {
			return 0, errors.New("no owner")
		}
		return uid, nil
	}

	mine, others := partitionByOwner([]string{"a/ours", "b/theirs", "c/also-ours", "d/unknown"}, 501, ownerOf)
	if strings.Join(mine, ",") != "a/ours,c/also-ours" {
		t.Errorf("this account's own entries are %v", mine)
	}
	// An entry whose owner cannot be read is left alone: an unreadable owner is
	// not evidence that it is ours.
	if strings.Join(others, ",") != "b/theirs,d/unknown" {
		t.Errorf("the entries left alone are %v", others)
	}
}
