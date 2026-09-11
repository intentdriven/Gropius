package lifecycle

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/instance"
)

// `gropius update` is the bootstrap's work done a second time, by the binary
// rather than the script, and then one thing that is genuinely new.
//
// Almost none of the mechanism is new: the fetch, the verification, the staged
// swap, the one elevation, the link and the launch are the installing verbs'
// own code reached again. What is new is the ENDING — a report that can say
// "the bundle is new and this Mac is still serving the old one" — because on a
// Mac where another login session holds the server port, a successful swap and
// a serving machine are two different facts.
//
// Nothing here reads standard input. Nothing here elevates for anything but the
// firewall grant. Nothing here contacts the forge unless a person typed the
// verb.

// retryUpdate is the command that runs the whole thing again. There is no
// resume: a download that stopped is downloaded again, which costs a minute and
// keeps this verb from carrying a second state machine.
const retryUpdate = "gropius update"

// UpdateEnv is everything update is allowed to do, handed in rather than
// reached for — the shape InstallEnv already has, for the same reason: every
// ending below is then a test with no Mac, no panel and no network.
type UpdateEnv struct {
	Paths config.Paths
	Home  string
	// Dest is where the bundle belongs, derived from the fixed locations this
	// account's install uses and never from the command line.
	Dest string
	Port int

	// Holder classifies the process on the server port through the instance
	// challenge — the same primitive the singleton election uses, asked rather
	// than re-implemented.
	Holder func() instance.Holder
	// PortBusy says whether anything is accepting connections on the port. It
	// is the ONE bit instance folds away: "nothing is there" and "something is
	// there that did not answer" are one classification to the election and two
	// different sentences to a person.
	PortBusy func() bool
	// ServingVersion reads the running server's version over the loopback
	// control plane. An empty version with no error is a build older than the
	// field, which is unknown rather than a failure.
	ServingVersion func() (string, error)

	// Staging makes the temporary directory the release is fetched into, and
	// hands back what removes it.
	Staging func() (dir string, cleanup func(), err error)
	// Fetch downloads one release asset by name. The origin is fixed in the
	// binary; this seam exists for the tests and carries no URL.
	Fetch func(name, dest string) error
	// Verify checks the archive against the checksums published beside it.
	Verify func(dir string) error
	// Unpack extracts the verified archive.
	Unpack func(archive, into string) error
	// Dequarantine clears the quarantine attribute on the verified bundle.
	Dequarantine func(bundle string)
	// StagedVersion runs the staged build's own version verb, inside the
	// directory that was just verified.
	StagedVersion func(program string) (string, error)

	// Place performs the staged swap. No second implementation.
	Place func(src, dest string) error
	// Quit asks THIS login session's running copy to quit. It cannot be aimed
	// at another account's server, and that is the mechanical reason this
	// command does not quit one.
	Quit func() error
	// Firewall re-makes the grant, raising the one authorisation panel.
	Firewall func(binary string) error
	// LinkMissing says whether this account's command link is gone.
	LinkMissing func() bool
	// Link re-asserts it when it is.
	Link func(home, binary string) (string, error)
	// Launch opens the placed bundle.
	Launch func(bundle string) error
	// Serving reports whether this account's server is answering.
	Serving func() bool
	// DestVersion is the version of the build at the destination, for a swap
	// that stopped. Empty when it cannot be read from here.
	DestVersion func() string
	Poll        time.Duration
}

// RunUpdate is the update verb.
func RunUpdate(env Env, args []string) int {
	ue, err := liveUpdateEnv(env)
	if err != nil {
		writeLine(env.Err, "gropius update: "+err.Error())
		return ExitFailed
	}
	return runUpdate(env, args, ue)
}

// versionish is a word a person types when they mean a release. It is matched
// so the refusal can say the true thing — that there is nothing to go back to —
// rather than "unexpected argument", which reads as a typo.
var versionish = regexp.MustCompile(`^v?\d+(\.\d+)*$`)

func runUpdate(env Env, args []string, ue UpdateEnv) int {
	fs := flags("update", env.Err)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		arg := fs.Arg(0)
		if versionish.MatchString(arg) {
			writeLine(env.Err, "gropius update: "+previousReleaseSentence)
			writeLine(env.Err, "This command installs the current release and takes no version.")
			return ExitUsage
		}
		writeLine(env.Err, "gropius update: unexpected argument "+Quote(arg))
		return ExitUsage
	}

	// ONE: who holds the port, before anything is fetched. A holder that
	// answered the challenge wrongly, or a data root no proof could be written
	// into, ends the run here — nothing downloaded, nothing placed.
	if classifyPort(ue) == portUnproven {
		// WHICH of the two things it found, because instance reports them the
		// same way and they are not the same thing to a person. The challenge
		// is a file written into this account's data root and read back over
		// the port: a root that cannot be written makes identity unprovable
		// with NOTHING on the port at all, and a refusal that said a process
		// was holding it would be asserting something nobody observed.
		//
		// The bit that tells them apart is the one this verb already asks for:
		// whether anything is accepting connections.
		if ue.PortBusy() {
			writeLine(env.Err, "gropius update: port "+strconv.Itoa(ue.Port)+
				" "+heldByUnproven)
			writeLine(env.Err, "Nothing was downloaded and nothing was replaced. A Mac with something on the "+
				"server port claiming to be Gropius and failing to prove it is not one to install software on.")
		} else {
			writeLine(env.Err, "gropius update: no proof of identity could be written into this account's data "+
				"root, so nothing can be checked against port "+strconv.Itoa(ue.Port)+".")
			writeLine(env.Err, "Nothing was downloaded and nothing was replaced. Run gropius doctor: the data "+
				"root is the check that will say what stopped the write.")
		}
		return ExitFailed
	}

	dir, cleanup, err := ue.Staging()
	if err != nil {
		return updateStopped(env, ue, "a temporary directory for the download", err)
	}
	defer cleanup()

	// TWO and THREE: fetch the archive and the checksums published beside it,
	// and verify before anything is unpacked, un-quarantined or placed.
	writeLine(env.Err, "downloading the current release…")
	for _, name := range []string{updateArchiveName, checksumsName} {
		if err := ue.Fetch(name, filepath.Join(dir, name)); err != nil {
			return updateStopped(env, ue, "the download", err)
		}
	}
	writeLine(env.Err, "verifying the download against the checksums published with the release…")
	if err := ue.Verify(dir); err != nil {
		return updateStopped(env, ue, "the verification", err)
	}

	// FOUR: unpack, clear the quarantine attribute on the VERIFIED bundle, and
	// refuse one whose program is a symbolic link — the same three acts, in the
	// same order, that the bootstrap performs.
	extracted := filepath.Join(dir, "extract")
	if err := ue.Unpack(filepath.Join(dir, updateArchiveName), extracted); err != nil {
		return updateStopped(env, ue, "unpacking the verified archive", err)
	}
	staged := filepath.Join(extracted, bundleName)
	if err := checkStagedBundle(staged); err != nil {
		return updateStopped(env, ue, "reading the verified bundle", err)
	}
	ue.Dequarantine(staged)

	// FIVE: the version being installed, read by running the staged build's own
	// version verb inside the directory that was just verified. A build that
	// refuses the verb is older than it, and the version is then unknown rather
	// than guessed.
	r := updateReport{Dest: ue.Dest, Home: ue.Home, Port: ue.Port}
	if version, err := ue.StagedVersion(filepath.Join(staged, binaryInBundle)); err == nil {
		r.Installed = version
	} else {
		r.InstalledReason = installedReasonNoVersion
	}

	// SIX: ask this account's running copy to quit, so the launch below starts
	// the new binary rather than activating the old process. It reaches only
	// this login session, which is why the report says what it says rather than
	// the code trying to reach further.
	if _, err := os.Lstat(ue.Dest); err == nil {
		if err := ue.Quit(); err != nil {
			writeLine(env.Err, "warning: a running copy could not be asked to quit ("+err.Error()+")")
		}
	}

	// SEVEN: the staged swap, which is PlaceBundle and not a second
	// implementation of it.
	writeLine(env.Err, stagePlace+": "+redact(ue.Dest, ue.Home))
	if err := ue.Place(staged, ue.Dest); err != nil {
		r.SwapFailure = redact(err.Error(), ue.Home)
		// Which of the two true things to say is the PLACER's answer, not a
		// guess made from its message: either it kept the only copy of the
		// application and says where, or the installed bundle is untouched at
		// the destination and the version there is what it was.
		var kept *keptStagingError
		if errors.As(err, &kept) {
			r.KeptStaging = kept.Path
		} else {
			r.DestVersion = ue.DestVersion()
		}
		finishUpdate(ue, &r)
		renderUpdate(env.Term, r)
		writeLine(env.Err, "Retry with: "+retryUpdate)
		return ExitFailed
	}
	r.Placed = true

	// EIGHT: the ONE elevation. The grant is re-made on every update because
	// the code identity it is keyed to changes with every build; a declined
	// panel is not a failed run, here or in the install beside it.
	r.GrantAttempted = true
	binary := filepath.Join(ue.Dest, binaryInBundle)
	if err := ue.Firewall(binary); err != nil {
		r.GrantDetail = redact(err.Error(), ue.Home)
		r.GrantCommands = firewallGrantCommands(binary, ue.Home)
	} else {
		r.GrantMade = true
	}

	// NINE: the link names a path inside the bundle rather than a build, so a
	// swap does not invalidate it. It is re-asserted only when it is gone.
	if ue.LinkMissing() {
		if link, err := ue.Link(ue.Home, binary); err != nil {
			writeLine(env.Err, "warning: the "+linkFileName+" command could not be linked ("+
				redact(err.Error(), ue.Home)+")")
		} else {
			writeLine(env.Err, "the "+linkFileName+" command is at "+redact(link, ue.Home)+".")
		}
	}

	// TEN: launch, and wait for an answer, exactly as the install's ending does.
	if err := ue.Launch(ue.Dest); err != nil {
		writeLine(env.Err, "warning: "+redact(ue.Dest, ue.Home)+" could not be opened ("+
			redact(err.Error(), ue.Home)+")")
	} else {
		waitUntil(ue.Serving, ue.Poll, 30*time.Second)
	}

	// ELEVEN: ask again, and report. The verdict is what was true when the
	// command returned, not what was true before the swap.
	finishUpdate(ue, &r)
	renderUpdate(env.Term, r)
	return ExitOK
}

// finishUpdate asks the port the second time and fills in what this Mac is
// actually serving.
//
// "Unknown" is a first-class answer here, and it is never the version just
// installed: a build with no version field, a control plane that did not
// answer, and a holder that answered no challenge are three different things to
// have found and three different sentences to read.
func finishUpdate(ue UpdateEnv, r *updateReport) {
	r.Holder = classifyPort(ue)
	switch r.Holder {
	case portOurs:
		version, err := ue.ServingVersion()
		switch {
		case err != nil:
			r.ServingReason = servingReasonUnanswered
		case version == "":
			r.ServingReason = servingReasonNoField
		default:
			r.Serving = version
		}
	case portSilent:
		r.ServingReason = servingReasonSilent
	case portUnproven:
		r.ServingReason = servingReasonUnproven
	default:
		r.ServingReason = servingReasonIdle
	}
}

// classifyPort asks instance the question the singleton election asks, and adds
// the one bit instance folds away: whether anything is accepting connections at
// all. It is not a second classification of identity — whether a holder is
// trustworthy stays instance's answer.
func classifyPort(ue UpdateEnv) portHolder {
	switch ue.Holder() {
	case instance.HolderOurs:
		return portOurs
	case instance.HolderForeign:
		return portUnproven
	default:
		if ue.PortBusy() {
			return portSilent
		}
		return portIdle
	}
}

// updateStopped reports a stage that did not complete. Nothing after it runs:
// the stages that follow raise an authorisation panel and replace an
// application, and neither was written for a run that stopped here.
func updateStopped(env Env, ue UpdateEnv, stage string, err error) int {
	writeLine(env.Err, "gropius update: "+stage+" stopped: "+redact(err.Error(), ue.Home))
	writeLine(env.Err, "Nothing was replaced. Retry with: "+retryUpdate)
	return ExitFailed
}

// fetchServingVersion asks the running server what build it is, over the
// loopback control plane.
//
// WHY IT IS DECODED HERE RATHER THAN ON ServerState. The field is a COORDINATED
// change in the lane that owns internal/gateway, and it has not landed: the
// contract test beside ServerState holds every field that struct declares to a
// field gateway.State publishes, and would rightly fail on one the control
// plane does not carry. So the decode sits here, reads the same read-only route
// status already reads, and answers "" for every build that does not publish a
// version — which is every build today. That is the fallback the intent names:
// the serving version is reported as unknown wherever it cannot be read, and
// the output is poorer rather than untrue. When the field lands it moves onto
// ServerState and into the list that test checks.
func fetchServingVersion(port int) (string, error) {
	body, err := controlPlaneGet(port, "/api/state")
	if err != nil {
		return "", err
	}
	var snapshot struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return "", err
	}
	return snapshot.Version, nil
}

// liveUpdateEnv is the world a real update acts on.
//
// It refuses to be built inside a test binary (see live.go). Every function it
// fills in downloads a release, quits an application, raises an authorisation
// panel or replaces the bundle this process is running from, and an update is
// the one verb where reaching the live path in a test would do all four to the
// machine running the suite.
func liveUpdateEnv(env Env) (UpdateEnv, error) {
	if err := liveEnvGuard("update"); err != nil {
		return UpdateEnv{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return UpdateEnv{}, err
	}
	dest := installDest(home)
	return UpdateEnv{
		Paths:          env.Paths,
		Home:           home,
		Dest:           dest,
		Port:           env.Port,
		Holder:         func() instance.Holder { return instance.ProbeExisting(env.Paths, env.Port) },
		PortBusy:       func() bool { return portAccepts(env.Port) },
		ServingVersion: func() (string, error) { return fetchServingVersion(env.Port) },
		Staging: func() (string, func(), error) {
			dir, err := os.MkdirTemp("", "gropius-update-")
			if err != nil {
				return "", func() {}, err
			}
			return dir, func() { os.RemoveAll(dir) }, nil
		},
		Fetch:         fetchAsset,
		Verify:        verifyChecksums,
		Unpack:        unpackArchive,
		Dequarantine:  clearQuarantine,
		StagedVersion: stagedVersion,
		Place:         PlaceBundle,
		Quit:          quitRunningCopy,
		Firewall:      grantFirewall,
		LinkMissing:   func() bool { return linkMissing(home) },
		Link:          linkCommand,
		Launch:        launchBundle,
		Serving:       func() bool { return instance.ProbeExisting(env.Paths, env.Port) == instance.HolderOurs },
		DestVersion:   func() string { return versionAtDest(env.Version, dest) },
		Poll:          250 * time.Millisecond,
	}, nil
}

// portAccepts says whether anything is accepting connections on the port. It
// connects and hangs up; it asks nothing and reads nothing.
func portAccepts(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// linkMissing reports whether this account's command link is gone.
func linkMissing(home string) bool {
	_, err := os.Lstat(filepath.Join(binDir(home), linkFileName))
	return err != nil
}

// versionAtDest is the version of the build at the destination, for a swap that
// stopped.
//
// It is THIS process's own version, and only where this process is running from
// the bundle at the destination — which is the ordinary case, since `gropius`
// is a link into that bundle. Anywhere else the answer is that it cannot be
// read from here, rather than a version borrowed from a different copy: the
// spec's rule is that a binary is executed only from a directory that was
// verified, and running the installed bundle to interview it is exactly what
// that rule refuses.
func versionAtDest(version, dest string) string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if rel, err := filepath.Rel(dest, self); err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return version
}
