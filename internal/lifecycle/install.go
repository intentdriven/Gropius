package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/instance"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// `gropius install` is the second half of the bootstrap: the shell script
// fetches the archive, verifies it against the release's published checksums
// and clears the quarantine attribute, and then hands over to the binary IT
// VERIFIED — never to the bundle it is about to replace — so the bootstrap and
// the binary it calls are always the same build.
//
// What happens here is everything that needs a program rather than a script:
// the staged swap with a rename that can refuse, the one authorisation panel,
// foreground provisioning with a proportion, and the per-user link.
//
// Nothing here reads standard input. Under `curl … | bash` the script's own
// remaining text IS standard input, so a read would consume the rest of the
// installer; the one place consent is needed raises the system authorisation
// panel instead.

// The names of the stages, which a failure reports and a person retries.
const (
	stagePlace    = "placing the application"
	stageFirewall = "the firewall grant"
	stageRuntime  = "the MLX runtime"
	stageLink     = "the gropius command"
)

// retryInstall is the command that retries any stage of an install. One
// command, because the verb repairs what is missing rather than reinstalling
// what is not: running it again after a failure resumes rather than restarts.
const retryInstall = "gropius install"

// bundleName is the application bundle this product installs, and binaryInBundle
// is where its executable sits inside it.
const (
	bundleName     = "Gropius.app"
	binaryInBundle = "Contents/MacOS/gropius"
	// systemApplications is the machine-wide applications directory. One
	// bundle there is launched by every account on the Mac; the per-user
	// fallback below it is genuinely per-account.
	systemApplications = "/Applications"
)

// Provisioner is the part of runtime.Provisioner the install verb uses. It is
// an interface so a test can hand over a provisioner that reports stages and
// touches no network; *runtime.Provisioner is what a real run gets.
type Provisioner interface {
	Ensure(ctx context.Context) error
	Status() runtime.SetupStatus
	Installed() bool
}

// InstallEnv is everything install is allowed to do, handed in rather than
// reached for, so every case below is a test with no Mac, no panel and no
// network.
type InstallEnv struct {
	Paths config.Paths
	Home  string
	// Bundle is the verified bundle to place. Empty means there is nothing to
	// place — `gropius install` typed on a Mac where the application is already
	// where it belongs — and the rest of the install still runs.
	Bundle string
	// Dest is where the bundle belongs, derived from the fixed locations this
	// account's install uses and never from the command line.
	Dest string
	// PathEnv is this account's search path, which decides whether the link
	// below resolves for anybody.
	PathEnv string

	// Place performs the staged swap.
	Place func(src, dest string) error
	// Quit asks a running copy to quit before it is replaced.
	Quit func() error
	// Firewall grants a binary through the macOS Application Firewall, raising
	// the one authorisation panel this verb is allowed.
	Firewall func(binary string) error
	// Runtime is the private Python and MLX runtime.
	Runtime Provisioner
	// Launch opens the installed bundle.
	Launch func(bundle string) error
	// Serving reports whether this account's server is answering.
	Serving func() bool
	// Poll is how often progress is read and the server is asked whether it is
	// up yet.
	Poll time.Duration
}

// RunInstall is the install verb.
func RunInstall(env Env, args []string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		writeLine(env.Err, "gropius install: "+err.Error())
		return ExitFailed
	}
	dest := installDest(home)
	return runInstall(env, args, InstallEnv{
		Paths:    env.Paths,
		Home:     home,
		Dest:     dest,
		PathEnv:  os.Getenv("PATH"),
		Place:    PlaceBundle,
		Quit:     quitRunningCopy,
		Firewall: grantFirewall,
		Runtime:  runtime.NewProvisioner(env.Paths),
		Launch:   launchBundle,
		Serving:  func() bool { return instance.Probe(env.Paths, env.Port) == instance.HolderOurs },
		Poll:     250 * time.Millisecond,
	})
}

func runInstall(env Env, args []string, ie InstallEnv) int {
	fs := flags("install", env.Err)
	bundle := fs.String("bundle", "", "the verified bundle to place, as the bootstrap hands it over")
	placeOnly := fs.Bool("place-only", false, "place the bundle and stop: no firewall grant, no provisioning, no launch")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		writeLine(env.Err, "gropius install: unexpected argument "+quote(fs.Arg(0)))
		return ExitUsage
	}
	if *bundle != "" {
		ie.Bundle = *bundle
	}

	progress := env.progress()
	binary := filepath.Join(ie.Dest, binaryInBundle)

	if ie.Bundle != "" {
		// A running copy is asked to quit first. Launch Services activates an
		// already-running process instead of starting the new binary, so an
		// upgrade over a live app would report success while the old version
		// went on serving. A copy that will not quit is a warning: the swap
		// below replaces the bundle either way.
		if err := ie.Quit(); err != nil {
			writeLine(env.Err, "warning: a running copy could not be asked to quit ("+err.Error()+")")
		}
		writeLine(env.Err, stagePlace+": "+abbreviate(ie.Dest, ie.Home))
		if err := ie.Place(ie.Bundle, ie.Dest); err != nil {
			return fail(env, ie, stagePlace, err)
		}
	}

	if *placeOnly {
		writeLine(env.Out, "placed "+abbreviate(ie.Dest, ie.Home)+".")
		writeLine(env.Out, "The firewall grant, the MLX runtime and the "+linkFileName+
			" command are not done: run "+retryInstall+" to finish.")
		return ExitOK
	}

	// The ONE elevation. The firewall entry is machine-wide state with no
	// per-account route, which is what makes it the exception; a refusal leaves
	// an installation that serves loopback, so it is reported with the commands
	// that make the grant by hand rather than failing the install.
	if err := ie.Firewall(binary); err != nil {
		writeLine(env.Err, "warning: "+stageFirewall+" was not made ("+err.Error()+").")
		writeLine(env.Err, "Other machines may see an empty response until an administrator runs:")
		for _, c := range firewallGrantCommands(binary) {
			writeLine(env.Err, "  "+c)
		}
	}

	// Provisioning, in the foreground, with a proportion. Ensure is already
	// idempotent, so "repair rather than reinstall" is a report of what it did
	// rather than a second code path.
	missing := missingRuntimeParts(ie.Paths, ie.Runtime.Installed(), fileExists)
	if err := provisionWithProgress(context.Background(), ie.Runtime, progress, ie.Poll); err != nil {
		return fail(env, ie, stageRuntime, err)
	}
	if len(missing) == 0 {
		writeLine(env.Out, stageRuntime+" was already complete; nothing was reinstalled.")
	} else {
		writeLine(env.Out, stageRuntime+": installed what was missing ("+strings.Join(missing, ", ")+").")
	}

	// The per-user link, which elevates for nothing.
	link, err := linkCommand(ie.Home, binary)
	if err != nil {
		return fail(env, ie, stageLink, err)
	}
	writeLine(env.Out, "the "+linkFileName+" command is at "+abbreviate(link, ie.Home)+".")
	if dir := binDir(ie.Home); !onSearchPath(dir, ie.PathEnv) {
		writeLine(env.Out, pathAdvice(dir, ie.Home))
	}

	if err := ie.Launch(ie.Dest); err != nil {
		writeLine(env.Err, "warning: "+abbreviate(ie.Dest, ie.Home)+" could not be opened ("+err.Error()+")")
		return ExitOK
	}
	if waitUntil(ie.Serving, ie.Poll, 30*time.Second) {
		writeLine(env.Out, "Gropius is serving.")
	} else {
		// Said rather than assumed: the terminal's verdict is what was true
		// when it exited, and the app's own check on the next launch may still
		// finish what this run could not.
		writeLine(env.Out, "Gropius was opened; it was not answering yet when this command returned. Run gropius status to see.")
	}
	return ExitOK
}

// fail reports a stage that did not complete: what failed, why, and the command
// that retries it.
func fail(env Env, ie InstallEnv, stage string, err error) int {
	writeLine(env.Err, "gropius install: "+stage+" failed: "+abbreviate(err.Error(), ie.Home))
	writeLine(env.Err, "Retry with: "+retryInstall)
	return ExitFailed
}

// installDest is where the bundle belongs on this Mac.
//
// The machine-wide directory when this account can write it, and this account's
// own Applications directory otherwise — the same rule the bootstrap has always
// used, and deliberately not an elevation: a destination has a per-user
// equivalent, and installing for every account when one asked is not what was
// asked.
func installDest(home string) string {
	if writableDir(systemApplications) == nil {
		return filepath.Join(systemApplications, bundleName)
	}
	return filepath.Join(home, "Applications", bundleName)
}

// missingRuntimeParts names the pieces of the private runtime that are not
// there, which is what lets the report say whether it repaired anything.
func missingRuntimeParts(paths config.Paths, installed bool, exists func(string) bool) []string {
	var missing []string
	if !exists(paths.UV()) {
		missing = append(missing, "uv")
	}
	if !exists(paths.VenvPython()) {
		missing = append(missing, "Python")
	}
	if !installed {
		missing = append(missing, "the MLX packages")
	}
	return missing
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// provisionWithProgress runs the provisioner and renders what it reports, until
// it is done.
//
// The status is read rather than pushed, because runtime.Provisioner already
// publishes one for the panel to poll: reading it here is what keeps the words
// on the terminal and the words in the panel the same words.
func provisionWithProgress(ctx context.Context, p Provisioner, t Terminal, poll time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- p.Ensure(ctx) }()

	render := func() {
		s := p.Status()
		t.Step(string(s.Stage), s.Step, s.Steps)
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			render()
			t.Finish()
			return err
		case <-tick.C:
			render()
		}
	}
}

// waitUntil polls cond until it holds or the deadline passes.
func waitUntil(cond func() bool, poll, limit time.Duration) bool {
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.Now().Add(limit)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(poll)
	}
}

// progress is the terminal a verb reports work on: standard error, so a
// progress line never lands in the middle of output a caller is parsing.
func (e Env) progress() Terminal {
	return Terminal{Out: e.Err, Color: e.Term.Color, Redraw: e.Term.Redraw}
}
