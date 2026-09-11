package archtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The committed identity pin (.abcd/config/identity.json) records the author
// identity every commit here is expected to carry. A pin with nothing enforcing
// it is a record, not a gate: `abcd ahoy identity-check` reads it, but nothing
// invokes that command, and continuous integration is the wrong place for the
// question — by the time CI can see a divergent author, the fix is a history
// rewrite. The check belongs at commit time, in the scaffolded hook.
//
// These tests execute .githooks/pre-commit against throwaway repositories, so
// they hold the gate to what it DOES rather than to text in the file. Four
// behaviours, and each of them is load-bearing:
//
//   - a matching pin passes;
//   - a divergent identity blocks, and the refusal names the pin so the reader
//     knows which file to look at;
//   - a pin that is present but unreadable blocks (fail closed) — an
//     unverifiable identity is never treated as a verified one;
//   - no pin passes, because a repository without a pin has not expressed an
//     opinion for the gate to enforce.
//
// The gate must also run BEFORE the private name guard. The name guard is
// permitted to be inactive (it warns and continues when no banlist is present),
// and a future edit that moved the identity check after it — or inside it —
// could make the identity check conditional on a machine-local file. The
// divergent-identity case pins the ordering by construction: these temporary
// repositories have no banlist at all, so a gate that ran second would still
// have to block.

const identityPinPath = ".abcd/config/identity.json"

// runPreCommit runs this repository's real pre-commit hook inside repo and
// returns its exit status and everything it wrote to stderr (where the hook
// puts every refusal).
func runPreCommit(t *testing.T, repo string) (int, string) {
	t.Helper()
	hook := filepath.Join(repoRootDir(t), ".githooks", "pre-commit")
	if _, err := os.Stat(hook); err != nil {
		t.Fatalf("pre-commit hook not found: %v", err)
	}

	cmd := exec.Command(hook)
	cmd.Dir = repo
	// A deliberately minimal environment. HOME and the git config overrides
	// point away from the developer's own settings so the fixture repo's local
	// identity is the only one in play -- otherwise a global user.email would
	// decide the result and the test would pass or fail per machine.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + repo,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v\n%s", hook, err, out.String())
	}
	return code, out.String()
}

// newPinnedRepo makes a throwaway git repository whose local identity is
// name/email, writing pin as the contents of the identity file when pin is
// non-empty.
func newPinnedRepo(t *testing.T, name, email, pin string) string {
	t.Helper()
	repo := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"HOME="+repo,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "--quiet")
	git("config", "user.name", name)
	git("config", "user.email", email)

	if pin != "" {
		dir := filepath.Join(repo, filepath.FromSlash(".abcd/config"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(pin), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

const matchingPin = `{
  "name": "Alice",
  "email": "alice@example.invalid"
}
`

func TestIdentityGatePassesWhenTheIdentityMatchesThePin(t *testing.T) {
	repo := newPinnedRepo(t, "Alice", "alice@example.invalid", matchingPin)
	code, out := runPreCommit(t, repo)
	if code != 0 {
		t.Fatalf("pre-commit refused a commit whose identity matches the pin (exit %d):\n%s", code, out)
	}
}

func TestIdentityGateBlocksADivergentIdentityAndNamesThePin(t *testing.T) {
	// Same name, different address: the gate must hold both halves, because an
	// address is what the forge attributes a commit to.
	repo := newPinnedRepo(t, "Alice", "bob@example.invalid", matchingPin)
	code, out := runPreCommit(t, repo)
	if code == 0 {
		t.Fatalf("pre-commit allowed a commit whose identity diverges from the pin:\n%s", out)
	}
	if !strings.Contains(out, identityPinPath) {
		t.Errorf("the refusal does not name %s, so the reader is not told which file to look at:\n%s", identityPinPath, out)
	}
	if !strings.Contains(out, "alice@example.invalid") {
		t.Errorf("the refusal does not quote the pinned identity:\n%s", out)
	}
}

func TestIdentityGateBlocksADivergentNameEvenWhenTheAddressMatches(t *testing.T) {
	repo := newPinnedRepo(t, "Bob", "alice@example.invalid", matchingPin)
	code, out := runPreCommit(t, repo)
	if code == 0 {
		t.Fatalf("pre-commit allowed a commit whose author NAME diverges from the pin:\n%s", out)
	}
	if !strings.Contains(out, identityPinPath) {
		t.Errorf("the refusal does not name %s:\n%s", identityPinPath, out)
	}
}

func TestIdentityGateFailsClosedOnAPinItCannotRead(t *testing.T) {
	// Malformed: the keys are not the canonical lowercase "name"/"email", so
	// the gate's own reader comes back empty. A pin that is present but cannot
	// be understood must never be treated as no pin at all -- that would let a
	// one-character typo in the pin silently disable the gate.
	t.Run("malformed", func(t *testing.T) {
		repo := newPinnedRepo(t, "Alice", "alice@example.invalid", `{"Name": "Alice", "Email": "alice@example.invalid"}`)
		code, out := runPreCommit(t, repo)
		if code == 0 {
			t.Fatalf("pre-commit passed with a pin whose name/email could not be read:\n%s", out)
		}
		if !strings.Contains(out, identityPinPath) {
			t.Errorf("the refusal does not name %s:\n%s", identityPinPath, out)
		}
	})

	// Empty values are the same class arriving by a different route.
	t.Run("empty values", func(t *testing.T) {
		repo := newPinnedRepo(t, "Alice", "alice@example.invalid", `{"name": "", "email": ""}`)
		code, out := runPreCommit(t, repo)
		if code == 0 {
			t.Fatalf("pre-commit passed with an empty pin:\n%s", out)
		}
	})

	// Unreadable on disk. root can read anything, so the mode carries no
	// meaning there and the case is skipped rather than asserted falsely.
	t.Run("unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: file modes do not deny reads")
		}
		repo := newPinnedRepo(t, "Alice", "alice@example.invalid", matchingPin)
		pin := filepath.Join(repo, filepath.FromSlash(identityPinPath))
		if err := os.Chmod(pin, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(pin, 0o644) })
		code, out := runPreCommit(t, repo)
		if code == 0 {
			t.Fatalf("pre-commit passed with a pin it could not open:\n%s", out)
		}
	})
}

func TestIdentityGatePassesWhenTheRepositoryHasNoPin(t *testing.T) {
	// No pin is the check declining to have an opinion, which is what makes the
	// gate safe to carry in a repository that has not adopted one. It is also
	// why the pin and the gate have to be reasoned about as one unit: a green
	// run before the pin lands proves nothing about the gate.
	repo := newPinnedRepo(t, "Carol", "carol@example.invalid", "")
	code, out := runPreCommit(t, repo)
	if code != 0 {
		t.Fatalf("pre-commit refused a commit in a repository with no identity pin (exit %d):\n%s", code, out)
	}
}
