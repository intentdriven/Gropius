package lifecycle

import (
	"os/exec"
	"strings"
	"testing"
)

// The commands a person is told to paste into a root shell are quoted by this
// product, not by the person reading them.
//
// The path in them is derived from this account's home directory, which whoever
// runs the verb controls; a single quote inside it would close the hand-written
// quoting and the rest would be read as commands. A standard account could
// otherwise produce, out of a declined authorisation panel, a line an
// administrator has been told to run as root. The elevated AppleScript has
// always been safe here — `quoted form of` does this job — and this is the same
// rule for the copy a person pastes.
func TestThePrintedRootCommandsQuoteThePathTheyCarry(t *testing.T) {
	// A path a hostile HOME could produce. The shell must read all of it as one
	// word, and run nothing of it.
	hostile := "/tmp/x'; touch " + t.TempDir() + "/pwned; '"

	for _, cmd := range append(firewallGrantCommands(hostile), firewallRemoveCommand(hostile)) {
		// The path is checked by asking a shell what the command line parses
		// to, with the tool replaced by `echo` so nothing privileged runs and
		// nothing is executed but the parse itself.
		parsed := parseAsShellWords(t, strings.TrimPrefix(cmd, "sudo "))
		if len(parsed) != 3 {
			t.Fatalf("%q parses to %d words (%q), want the tool, the flag and ONE path", cmd, len(parsed), parsed)
		}
		if parsed[2] != hostile {
			t.Errorf("%q parses its path as %q, want %q", cmd, parsed[2], hostile)
		}
	}
}

// parseAsShellWords returns the words a shell would split a command line into,
// without running it: the command is turned into a printf of its own arguments.
func parseAsShellWords(t *testing.T, line string) []string {
	t.Helper()
	first := strings.Index(line, " ")
	if first < 0 {
		t.Fatalf("%q is one word", line)
	}
	script := "printf '%s\\n' " + line[:first] + line[first:]
	out, err := exec.Command("/bin/bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("parsing %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}
