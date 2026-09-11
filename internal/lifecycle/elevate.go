package lifecycle

import (
	"fmt"
	"os/exec"
	"strings"
)

// The one elevation, and the two other system tools a lifecycle verb starts.
//
// WHY THERE IS ONE AT ALL. The macOS Application Firewall entry is machine-wide
// state with no per-account route: a standard account cannot add or remove one,
// and Gropius without it accepts the handshake and drops the data, so the LAN
// sees an empty response while loopback works. Every other step of every verb
// acts on files this account owns and elevates for nothing — the per-user link,
// the private runtime, the settings, and every removal.
//
// WHY IT IS THE AUTHORISATION PANEL AND NOT sudo. sudo can only ever accept the
// invoking user's own password, and a standard account is not in the sudoers
// set at all — so on exactly the accounts that most need this, a terminal
// prompt cannot succeed. The panel asks for an administrator's name AND
// password, so somebody else can enter theirs. It also sidesteps the standard
// input hazard completely: under `curl … | bash` the script's remaining text is
// standard input, and a terminal prompt would eat the rest of the installer.
//
// WHY osascript IS NAMED BY ABSOLUTE PATH. This is the process that draws the
// panel a person types an administrator password into. A bare name resolves
// through PATH, and a normal PATH puts user-writable directories ahead of
// /usr/bin; unprivileged code already running as this account could drop an
// `osascript` shim there, draw its own panel and harvest the password. The
// AppleScript's own argument escaping cannot help when the interpreter is
// attacker-supplied.
//
// WHY THE SCRIPT IS ASSEMBLED FROM LITERALS. Every line handed to osascript
// below is a constant, and the only varying value — the binary's path — travels
// as an argv argument that AppleScript escapes with `quoted form of`.
// Interpolating a path into a command that runs as root would be a local
// privilege-escalation surface in the install path itself.

// The two system tools these verbs start, written out at every call site
// rather than reached through these names. The pinned-subprocess scan reads
// string literals, so a path that arrives at exec through an identifier passes
// it in silence; the spelling below is here to be read, and the literal one
// line away is what the scan covers (internal/lifecycle/doctor.go does the same
// for the firewall query).
//
//	/usr/bin/osascript — draws the system authorisation panel
//	/usr/bin/open      — launches an application bundle

// The two firewall actions, as AppleScript. `fw` and `p` are bound by the
// preamble in elevateFirewall; the prompt is what the panel shows the person
// being asked, which is the stated reason the criteria require.
const (
	firewallGrantScript = `do shell script fw & " --add " & p & " && " & fw & " --unblockapp " & p ` +
		`with prompt "Gropius needs administrator rights to allow itself through the macOS firewall, ` +
		`so other machines on your network can reach it." with administrator privileges`

	firewallRemoveScript = `do shell script fw & " --remove " & p ` +
		`with prompt "Gropius needs administrator rights to remove its own entry from the macOS firewall. ` +
		`This is the last step of uninstalling it." with administrator privileges`
)

// grantFirewall allows a binary through the macOS Application Firewall.
func grantFirewall(binary string) error { return elevateFirewall(firewallGrantScript, binary) }

// revokeFirewall removes a binary's entry from the macOS Application Firewall.
func revokeFirewall(binary string) error { return elevateFirewall(firewallRemoveScript, binary) }

// elevateFirewall is the ONLY place in this package that asks for
// administrator rights. Both verbs reach it; a scan in internal/archtest holds
// it to being one site.
func elevateFirewall(script, binary string) error {
	if binary == "" {
		return fmt.Errorf("this installation's own binary path is not known")
	}
	cmd := exec.Command("/usr/bin/osascript",
		"-e", "on run argv",
		"-e", `set fw to "`+socketfilterfw+`"`,
		"-e", "set p to quoted form of (item 1 of argv)",
		"-e", script,
		"-e", "end run",
		"--", binary)
	// Nothing reads standard input. Left nil, os/exec connects /dev/null, which
	// is the point: under a piped bootstrap this process's standard input is
	// the rest of the installer script.
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%s", detail)
		}
		return err
	}
	return nil
}

// firewallGrantCommands are the two commands that make the grant by hand, for
// the report to print when the panel was declined or could not be raised.
func firewallGrantCommands(binary, home string) []string {
	return []string{
		"sudo " + socketfilterfw + " --add " + shellArg(binary, home),
		"sudo " + socketfilterfw + " --unblockapp " + shellArg(binary, home),
	}
}

// firewallRemoveCommand is the command that removes the entry by hand.
func firewallRemoveCommand(binary, home string) string {
	return "sudo " + socketfilterfw + " --remove " + shellArg(binary, home)
}

// The quoting these commands are printed under is shellArg's, in doctor.go:
// one rule for every root command line this product composes, whichever verb
// prints it.

// quitRunningCopy asks a running Gropius to quit, so the swap replaces a bundle
// nothing is executing and the launch that follows starts the new binary rather
// than activating the old process.
//
// A quit request rather than a signal: the app is a menu-bar application, and
// this is the message Launch Services already sends it. The name is a literal,
// so nothing is interpolated into the script.
func quitRunningCopy() error {
	cmd := exec.Command("/usr/bin/osascript", "-e", `quit app "Gropius"`)
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%s", detail)
		}
		return err
	}
	return nil
}

// launchBundle opens the installed application.
func launchBundle(bundle string) error {
	cmd := exec.Command("/usr/bin/open", bundle)
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%s", detail)
		}
		return err
	}
	return nil
}
