package lifecycle

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/instance"
)

// The update verb, run end to end through its seams: no Mac, no panel, no
// network, and — because live.go refuses to build the live environment inside a
// test binary at all — no way for any of this to reach a real release.

// updateCalls records what an update asked the world to do, so a test can
// assert on what did NOT happen as easily as on what did. Refusing before
// anything is written is a claim about absence, and absence is what a recorder
// makes checkable.
type updateCalls struct {
	fetched   []string
	verified  int
	unpacked  int
	staged    int
	quit      int
	placed    []string
	firewall  []string
	linked    int
	launched  []string
	holderAsk int
}

// fakeUpdate is an update environment where every side effect is recorded and
// none of them happens.
type fakeUpdate struct {
	env   UpdateEnv
	calls *updateCalls
	// holders is what the port classification answers, in order: the question
	// is asked once before anything is fetched and once at the end.
	holders []instance.Holder
	busy    bool
	// serving is what the control plane says the running server's version is.
	serving    string
	servingErr error
	// failures a test can arm.
	fetchErr    error
	verifyErr   error
	placeErr    error
	firewallErr error
	quitErr     error
	launchErr   error
	// versionErr makes the staged build refuse its own version verb.
	versionErr error
	// linkMissing makes the per-user command link absent.
	linkMissing bool
	// answering is whether the launched copy answers before the deadline.
	answering bool
}

func newFakeUpdate(t *testing.T) *fakeUpdate {
	t.Helper()
	home := t.TempDir()
	dest := filepath.Join(home, "Applications", bundleName)
	// A bundle at the destination, so the quit and the swap have something to
	// act on — the ordinary case, since an update updates an installation.
	if err := os.MkdirAll(filepath.Join(dest, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, binaryInBundle), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := &fakeUpdate{
		calls:     &updateCalls{},
		holders:   []instance.Holder{instance.HolderOurs, instance.HolderOurs},
		serving:   "0.5.0",
		answering: true,
	}
	f.env = UpdateEnv{
		Home: home,
		Dest: dest,
		Port: 11535,
		Holder: func() instance.Holder {
			i := f.calls.holderAsk
			f.calls.holderAsk++
			if i < len(f.holders) {
				return f.holders[i]
			}
			return f.holders[len(f.holders)-1]
		},
		PortBusy:       func() bool { return f.busy },
		ServingVersion: func() (string, error) { return f.serving, f.servingErr },
		Staging: func() (string, func(), error) {
			dir := t.TempDir()
			return dir, func() {}, nil
		},
		Fetch: func(name, dest string) error {
			f.calls.fetched = append(f.calls.fetched, name)
			if f.fetchErr != nil {
				return f.fetchErr
			}
			return os.WriteFile(dest, []byte(name), 0o600)
		},
		Verify: func(string) error {
			f.calls.verified++
			return f.verifyErr
		},
		Unpack: func(_, into string) error {
			f.calls.unpacked++
			bundle := filepath.Join(into, bundleName)
			if err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(bundle, binaryInBundle), []byte("#!/bin/sh\n"), 0o755)
		},
		Dequarantine: func(string) {},
		StagedVersion: func(string) (string, error) {
			f.calls.staged++
			if f.versionErr != nil {
				return "", f.versionErr
			}
			return "0.5.0", nil
		},
		Place: func(_, dest string) error {
			f.calls.placed = append(f.calls.placed, dest)
			return f.placeErr
		},
		Quit: func() error {
			f.calls.quit++
			return f.quitErr
		},
		Firewall: func(binary string) error {
			f.calls.firewall = append(f.calls.firewall, binary)
			return f.firewallErr
		},
		LinkMissing: func() bool { return f.linkMissing },
		Link: func(_, binary string) (string, error) {
			f.calls.linked++
			return filepath.Join(home, ".local", "bin", linkFileName), nil
		},
		Launch: func(bundle string) error {
			f.calls.launched = append(f.calls.launched, bundle)
			return f.launchErr
		},
		Serving:     func() bool { return f.answering },
		DestVersion: func() string { return "0.4.0" },
		Poll:        time.Millisecond,
	}
	return f
}

// run drives the verb and hands back what a person would have read.
func (f *fakeUpdate) run(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	env, o, e := testEnv()
	env.Port = f.env.Port
	code = runUpdate(env, args, f.env)
	return code, o.String(), e.String()
}

// A port holder that answers the identity challenge wrongly — or a data root no
// proof can be written into — ends the run before anything is fetched.
//
// This is the ONE ending where nothing is written. A Mac with something on the
// server port that will not prove it is this account's Gropius is not a Mac to
// install software on, and the refusal comes before the download rather than
// after it.
func TestTheUpdateRefusesBeforeAnythingIsWrittenWhenThePortCannotProveItself(t *testing.T) {
	f := newFakeUpdate(t)
	f.holders = []instance.Holder{instance.HolderForeign}
	// Something IS on the port, and it answered wrongly. The other way this
	// classification arises — a data root no proof can be written into — is the
	// table below.
	f.busy = true

	code, out, errOut := f.run(t)

	if code == ExitOK {
		t.Errorf("exit = %d, want non-zero for a port this command refuses to update around", code)
	}
	if len(f.calls.fetched) != 0 || f.calls.verified != 0 {
		t.Errorf("something was downloaded before the refusal: %v", f.calls.fetched)
	}
	if len(f.calls.placed) != 0 {
		t.Errorf("a bundle was placed after the refusal: %v", f.calls.placed)
	}
	if len(f.calls.firewall) != 0 {
		t.Error("the authorisation panel was raised after the refusal")
	}
	if len(f.calls.launched) != 0 {
		t.Error("an application was launched after the refusal")
	}
	if f.calls.quit != 0 {
		t.Error("a running copy was asked to quit after the refusal")
	}
	// And it says what it found, on the port it found it on.
	text := out + errOut
	if !strings.Contains(text, strconv.Itoa(f.env.Port)) {
		t.Errorf("the refusal does not name the port:\n%s", text)
	}
	if !strings.Contains(text, "answered this account's identity challenge wrongly") {
		t.Errorf("the refusal does not say what it found:\n%s", text)
	}
}

// The same classification covers a second thing, and the refusal must not
// confuse them: instance reports a data root no proof can be written into the
// same way it reports a wrong answer, because in neither case can identity be
// established. But in the first case there may be NOTHING on the port at all,
// and a refusal that says a process is holding it has asserted something it did
// not observe.
//
// What tells them apart is the one bit instance folds away, which this verb
// already asks for: whether anything is accepting connections.
func TestTheRefusalSaysWhichOfTheTwoThingsItFound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		busy     bool
		wantSaid string
		notSaid  string
	}{
		{
			name:     "something answered the challenge wrongly",
			busy:     true,
			wantSaid: "answered this account's identity challenge wrongly",
		},
		{
			name:     "no proof could be written into the data root",
			busy:     false,
			wantSaid: "no proof of identity could be written",
			// There may be nothing on that port at all, so the refusal must
			// not say a process is holding it.
			notSaid: "is held by",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUpdate(t)
			f.holders = []instance.Holder{instance.HolderForeign}
			f.busy = tc.busy

			code, out, errOut := f.run(t)
			text := out + errOut

			if code == ExitOK {
				t.Errorf("exit = %d, want non-zero", code)
			}
			if len(f.calls.fetched) != 0 || len(f.calls.placed) != 0 {
				t.Error("something was downloaded or placed after the refusal")
			}
			if !strings.Contains(text, tc.wantSaid) {
				t.Errorf("the refusal does not say %q:\n%s", tc.wantSaid, text)
			}
			if tc.notSaid != "" && strings.Contains(text, tc.notSaid) {
				t.Errorf("the refusal asserts %q, which it did not observe:\n%s", tc.notSaid, text)
			}
		})
	}
}

// Something that holds the port and answers no challenge at all is NOT that
// case. Under a per-account data root that description fits another account's
// Gropius exactly, and refusing there would make a shared Mac unupdatable for
// as long as a colleague stays logged in. So the bundle is replaced, and the
// report says the version serving cannot be determined from here.
func TestAHolderThatAnswersNoChallengeStillGetsTheUpdate(t *testing.T) {
	f := newFakeUpdate(t)
	f.holders = []instance.Holder{instance.HolderNone, instance.HolderNone}
	f.busy = true

	code, out, errOut := f.run(t)

	if code != ExitOK {
		t.Fatalf("exit = %d (%s)", code, errOut)
	}
	if len(f.calls.placed) != 1 {
		t.Errorf("the bundle was placed %d times, want once", len(f.calls.placed))
	}
	if !strings.Contains(out, cannotBeDetermined) {
		t.Errorf("the report does not say the serving version cannot be determined:\n%s", out)
	}
	if !strings.Contains(out, didNotInstallSentence) {
		t.Errorf("the report does not say this Mac is serving something this command did not install:\n%s", out)
	}
	if strings.Contains(out, "0.5.0, on port") {
		t.Errorf("the version just installed was printed as the version serving:\n%s", out)
	}
}

// An idle port is the simple case: the bundle is placed and the launch is what
// makes the new version serve.
func TestAnIdlePortIsUpdatedAndLaunched(t *testing.T) {
	f := newFakeUpdate(t)
	f.holders = []instance.Holder{instance.HolderNone, instance.HolderOurs}
	f.busy = false

	code, out, errOut := f.run(t)

	if code != ExitOK {
		t.Fatalf("exit = %d (%s)", code, errOut)
	}
	if len(f.calls.launched) != 1 {
		t.Errorf("the application was launched %d times, want once", len(f.calls.launched))
	}
	if strings.Contains(out, didNotInstallSentence) {
		t.Errorf("an idle port was reported as another session serving:\n%s", out)
	}
}

// Nothing is asked to stop but this login session's own copy, once. The quit
// reaches only this session by construction — that is the mechanical reason
// this command cannot quit another account's server behind their back — and
// the report says so rather than the code trying.
func TestTheQuitReachesOnlyThisSessionAndIsSentOnce(t *testing.T) {
	f := newFakeUpdate(t)
	f.holders = []instance.Holder{instance.HolderNone, instance.HolderNone}
	f.busy = true

	if _, out, _ := f.run(t); !strings.Contains(out, cannotQuitSentence) {
		t.Errorf("the report does not say that quitting it is not something this command can do:\n%s", out)
	}
	// Exactly once — not "at most once". A run that never sent it would pass a
	// bound and prove nothing: the quit is what stops Launch Services
	// activating the old process instead of starting the new binary, so a swap
	// with no quit before it reports success while the old version goes on
	// serving.
	if f.calls.quit != 1 {
		t.Errorf("the quit was sent %d times, want exactly once", f.calls.quit)
	}

	// And it is sent only where there is an installed bundle to replace. On a
	// Mac with nothing at the destination there is nothing running from it, and
	// a warning about a quit that failed would be the first thing read.
	empty := newFakeUpdate(t)
	if err := os.RemoveAll(empty.env.Dest); err != nil {
		t.Fatal(err)
	}
	if _, _, errOut := empty.run(t); empty.calls.quit != 0 {
		t.Errorf("a quit was sent with no bundle at the destination (%s)", errOut)
	}
}

// A declined authorisation panel is not a failed run: the swap stands, the
// grant is reported as not made with the commands that make it by hand, and the
// exit is the answer code. What DOES fail is a download, a verification or a
// swap that stopped — so the rule is tested by its boundary rather than
// asserted by one example.
func TestWhatFailsARunAndWhatDoesNot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		arm      func(*fakeUpdate)
		wantCode int
		wantSaid string
	}{
		{
			name:     "a declined authorisation panel",
			arm:      func(f *fakeUpdate) { f.firewallErr = errors.New("User canceled.") },
			wantCode: ExitOK,
			wantSaid: grantNotMadeSentence,
		},
		{
			name:     "a download that stopped",
			arm:      func(f *fakeUpdate) { f.fetchErr = errors.New("could not resolve host") },
			wantCode: ExitFailed,
			wantSaid: "could not resolve host",
		},
		{
			name:     "a verification that refused",
			arm:      func(f *fakeUpdate) { f.verifyErr = errors.New(causeMismatch) },
			wantCode: ExitFailed,
			wantSaid: causeMismatch,
		},
		{
			name:     "a swap that stopped",
			arm:      func(f *fakeUpdate) { f.placeErr = errors.New("move the new bundle into place: file exists") },
			wantCode: ExitFailed,
			wantSaid: "file exists",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUpdate(t)
			tc.arm(f)
			code, out, errOut := f.run(t)

			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d\n%s\n%s", code, tc.wantCode, out, errOut)
			}
			if !strings.Contains(out+errOut, tc.wantSaid) {
				t.Errorf("the output does not say %q:\n%s\n%s", tc.wantSaid, out, errOut)
			}
		})
	}

	// The declined panel again, from the other side: the swap is still
	// reported as done and the by-hand commands are there.
	f := newFakeUpdate(t)
	f.firewallErr = errors.New("User canceled.")
	_, out, _ := f.run(t)
	if !strings.Contains(out, checksumSentence) {
		t.Errorf("a declined panel lost the report of the swap:\n%s", out)
	}
	if !strings.Contains(out, "socketfilterfw") {
		t.Errorf("a declined panel did not print the commands that make the grant by hand:\n%s", out)
	}
}

// A swap that stopped still says which version is at the destination, so a
// person knows what their Mac has.
func TestAStoppedSwapReportsWhatIsAtTheDestination(t *testing.T) {
	f := newFakeUpdate(t)
	f.placeErr = errors.New("move the new bundle into place: file exists (the previous copy is left as it was)")

	code, out, _ := f.run(t)
	if code == ExitOK {
		t.Errorf("exit = %d, want non-zero for a swap that stopped", code)
	}
	if !strings.Contains(out, "0.4.0") {
		t.Errorf("the report does not name the version at the destination:\n%s", out)
	}
	if len(f.calls.firewall) != 0 {
		t.Error("the authorisation panel was raised after the swap stopped")
	}
	if len(f.calls.launched) != 0 {
		t.Error("an application was launched after the swap stopped")
	}
}

// The one swap failure that keeps a staging directory names it, and names it as
// a path a person can actually go to.
//
// This is the failure where somebody has to DO something: the application is
// not where it belongs and the only copy of it is under a name nobody would
// think to look for. A report that names it relatively, or that names a
// directory the placer has already removed, is worse than one that says
// nothing — it sends a person looking for their application in the wrong place.
func TestAStoppedSwapThatKeptTheOnlyCopyNamesWhereItIs(t *testing.T) {
	f := newFakeUpdate(t)
	kept := filepath.Join(filepath.Dir(f.env.Dest), ".gropius-incoming-abcdef12", bundleName+".retired")
	f.env.Place = func(_, dest string) error {
		f.calls.placed = append(f.calls.placed, dest)
		return &keptStagingError{
			Path: kept,
			err: errors.New("move the new bundle into place: no space left on device — and the installed bundle " +
				"could NOT be put back. It is intact at " + kept),
		}
	}

	code, out, _ := f.run(t)
	if code == ExitOK {
		t.Errorf("exit = %d, want non-zero for a swap that stopped", code)
	}
	if !strings.Contains(out, redact(kept, f.env.Home)) {
		t.Errorf("the report does not name the path the only remaining copy is at:\nwant: %s\n%s",
			redact(kept, f.env.Home), out)
	}
	// And it does NOT claim a version is at the destination, because on this
	// path there is nothing there at all.
	if strings.Contains(out, "0.4.0") {
		t.Errorf("the report claims a version at a destination that is empty:\n%s", out)
	}
}

// And a swap that stopped WITHOUT keeping anything says the other true thing:
// the installed bundle is untouched at the destination. A copy that ran out of
// space is the ordinary case here, and the placer removes its staging directory
// on that path — so a report that sent a person to one would be sending them to
// a directory that does not exist.
func TestAStoppedSwapThatKeptNothingNamesTheVersionStillInstalled(t *testing.T) {
	f := newFakeUpdate(t)
	f.placeErr = errors.New("copy the verified bundle into /Applications: " +
		"/Applications/.gropius-incoming-abcdef12/Gropius.app/Contents/MacOS/gropius: no space left on device")

	_, out, _ := f.run(t)
	if !strings.Contains(out, "0.4.0") {
		t.Errorf("the report does not name the version still at the destination:\n%s", out)
	}
	if strings.Contains(out, "only remaining copy") {
		t.Errorf("the report sends a person to a staging directory the placer has already removed:\n%s", out)
	}
}

// The per-user link names a path inside the bundle rather than a build, so a
// swap does not invalidate it. It is re-asserted only when it is gone.
func TestThePerUserLinkIsLeftAloneUnlessItIsMissing(t *testing.T) {
	present := newFakeUpdate(t)
	if _, _, errOut := present.run(t); present.calls.linked != 0 {
		t.Errorf("the link was re-made while it was still there (%s)", errOut)
	}

	missing := newFakeUpdate(t)
	missing.linkMissing = true
	if _, _, errOut := missing.run(t); missing.calls.linked != 1 {
		t.Errorf("the link was made %d times for an account that has none (%s)", missing.calls.linked, errOut)
	}
}

// The version installed is read from the staged build, and a build that will
// not say what it is is reported as unknown rather than guessed.
func TestABuildThatWillNotSayWhatItIsIsReportedAsUnknown(t *testing.T) {
	f := newFakeUpdate(t)
	f.versionErr = errors.New("exit status 2")
	f.serving = ""

	code, out, errOut := f.run(t)
	if code != ExitOK {
		t.Fatalf("exit = %d (%s)", code, errOut)
	}
	installed := lineWithLabel(t, strings.Split(strings.TrimRight(out, "\n"), "\n"), installedLabel)
	if !strings.Contains(installed, cannotBeDetermined) {
		t.Errorf("the installed line does not say the version is unknown:\n%s", installed)
	}
}

// A version asked for is not a rollback that exists. Only the current release
// is published, so the command says the previous release cannot be fetched
// rather than implying a path that is not there.
func TestAskingForAVersionIsToldThePreviousReleaseCannotBeFetched(t *testing.T) {
	f := newFakeUpdate(t)
	code, _, errOut := f.run(t, "0.3.0")

	if code != ExitUsage {
		t.Errorf("exit = %d, want %d for a command line that cannot be honoured", code, ExitUsage)
	}
	if !strings.Contains(errOut, previousReleaseSentence) {
		t.Errorf("the refusal does not say the previous release cannot be fetched:\n%s", errOut)
	}
	if len(f.calls.fetched) != 0 {
		t.Errorf("a release was fetched for a version that cannot be had: %v", f.calls.fetched)
	}
}

// Every bad download fails closed through the verb itself, with the REAL
// verification: the fetch goes through the seam to a fake release server on
// loopback, and /usr/bin/shasum decides. Nothing is placed on any of them.
func TestTheUpdateFailsClosedOnEveryBadDownload(t *testing.T) {
	good := []byte("this is what the release workflow built")

	for _, tc := range []struct {
		name      string
		assets    map[string][]byte
		wantCause string
	}{
		{
			name: "a download that does not match the published checksums",
			assets: map[string][]byte{
				updateArchiveName: []byte("something else entirely"),
				checksumsName:     sums(digestOf(t, updateArchiveName, good)),
			},
			wantCause: causeMismatch,
		},
		{
			// A real checksums file, from a real release, that simply does
			// not cover the archive this command downloaded. Once the
			// verification is scoped to the archive's own line, this and "the
			// checksums name nothing that was downloaded" are the same thing
			// to have found, and it is named as the thing that matters.
			name: "a checksums file that does not cover the archive",
			assets: map[string][]byte{
				updateArchiveName: good,
				checksumsName:     sums(digestOf(t, "GropiusChat.app.zip", good)),
			},
			wantCause: causeArchiveNotCovered,
		},
		{
			name: "an empty checksums file",
			assets: map[string][]byte{
				updateArchiveName: good,
				checksumsName:     []byte(""),
			},
			wantCause: causeEmptyChecksums,
		},
		{
			name: "an error page served where an asset was asked for",
			assets: map[string][]byte{
				updateArchiveName: good,
				checksumsName:     []byte("<!DOCTYPE html>\n<html><body>404 Not Found</body></html>\n"),
			},
			wantCause: causeNotChecksums,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := newFakeRelease(t, tc.assets)
			f := newFakeUpdate(t)
			f.env.Fetch = func(name, dest string) error {
				f.calls.fetched = append(f.calls.fetched, name)
				return release.fetch(name, dest)
			}
			// The real thing, which is the point of this table.
			f.env.Verify = verifyChecksums

			code, out, errOut := f.run(t)

			if code == ExitOK {
				t.Errorf("exit = %d, want non-zero for a download that did not verify", code)
			}
			if len(f.calls.placed) != 0 {
				t.Errorf("a bundle was placed after a failed verification: %v", f.calls.placed)
			}
			if len(f.calls.firewall) != 0 {
				t.Error("the authorisation panel was raised after a failed verification")
			}
			if !strings.Contains(out+errOut, tc.wantCause) {
				t.Errorf("the failure does not name which cause fired (want %q):\n%s\n%s", tc.wantCause, out, errOut)
			}
		})
	}
}

// The serving version is decoded structurally from the snapshot the panel
// already polls, never by importing the gateway's type. A build with no version
// field is a build that cannot say, and that is unknown rather than the version
// just installed.
//
// WHAT THIS PROVES AND WHAT IT DOES NOT, so a green run is not over-read.
// NOTHING IN THE PRODUCT EMITS THE FIRST BODY: the version field is a
// coordinated change in the lane that owns internal/gateway and it has not
// landed (iss-2609111942567418). So the first row is a promise about the decode
// rather than a test of a running server, and it is here for the day the field
// arrives. The second row is the one that describes every Mac today, and it is
// the row the report actually depends on.
func TestTheServingVersionIsDecodedFromTheSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "a build that publishes its version",
			body: `{"version":"0.5.0","config":{"port":11535}}`,
			want: "0.5.0",
		},
		{
			name: "a build older than the field",
			body: `{"config":{"port":11535}}`,
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveOnLoopback(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/state" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			})
			got, err := fetchServingVersion(portOfServer(t, srv))
			if err != nil {
				t.Fatalf("fetchServingVersion: %v", err)
			}
			if got != tc.want {
				t.Errorf("fetchServingVersion = %q, want %q", got, tc.want)
			}
		})
	}

	// A control plane that does not answer is a different thing to have found
	// than a build with no field, and the report says which.
	if _, err := fetchServingVersion(1); err == nil {
		t.Error("a control plane that does not answer was read as a version")
	}
}

// The version the control plane hands back is UNTRUSTED TEXT, and it is
// checked before it becomes a line of the report.
//
// Where it comes from: the challenge in internal/instance proves a shared DATA
// ROOT, not an identity — deliberately, at mode 0640, so that under the
// shared-cache mode a peer account in the same group can answer it. That mode
// is a documented way to run this product. So any process of such an account
// that holds the loopback port is classified as ours and gets to put a string
// into this report.
//
// A newline in that string forges lines that read as the report's own — the
// grant sentence is four bytes of JSON away — and an escape sequence reaches
// the terminal. What cannot be fixed here is a peer that answers with a
// PLAUSIBLE version: the report's provenance is the control plane, and a
// report that asked is a report that can be told something false. What can be
// fixed is that the answer has to look like a version at all.
func TestAVersionFromTheControlPlaneIsCheckedBeforeItIsPrinted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		serving string
	}{
		{"a forged second line", "0.4.0\nThe firewall grant was re-made for this build."},
		{"an escape sequence", "0.4.0\x1b[2J\x1b[H"},
		{"a carriage return that redraws the line", "0.4.0\rserving:   0.5.0"},
		{"something enormous", strings.Repeat("9", 4096)},
		{"a sentence", "whatever you say it is"},
		{"nothing but spaces", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUpdate(t)
			f.serving = tc.serving

			_, out, _ := f.run(t)
			serving := lineWithLabel(t, strings.Split(strings.TrimRight(out, "\n"), "\n"), servingLabel)

			if !strings.Contains(serving, cannotBeDetermined) {
				t.Errorf("a version that is not one was printed as a fact:\n%s", serving)
			}
			// And nothing it carried reached the report at all.
			if strings.Contains(out, "\x1b") || strings.Contains(out, "\r") {
				t.Errorf("a control character from the control plane reached the report: %q", out)
			}
			if strings.Contains(out, "The firewall grant was re-made for this build.") && !f.env.LinkMissing() {
				// The grant WAS made in this run, so that sentence is
				// legitimately present — what must not happen is it arriving
				// twice, forged by the responder.
				if strings.Count(out, "The firewall grant was re-made for this build.") > 1 {
					t.Errorf("the responder forged a line of the report:\n%s", out)
				}
			}
		})
	}

	// A version that looks like one is still reported, so the rule above is a
	// check rather than a refusal to read anything.
	f := newFakeUpdate(t)
	f.serving = "0.4.0"
	if _, out, _ := f.run(t); !strings.Contains(out, "0.4.0") {
		t.Errorf("an ordinary version was refused:\n%s", out)
	}
}

// The swap is the one the installing verbs already perform: the update's
// default placer IS PlaceBundle, and no second staging implementation exists in
// the package.
func TestTheUpdateUsesTheSwapTheInstallerAlreadyPerforms(t *testing.T) {
	allowLiveEnvInTest(t)
	env, _, _ := testEnv()
	ue, err := liveUpdateEnv(env)
	if err != nil {
		t.Fatalf("liveUpdateEnv: %v", err)
	}
	if reflect.ValueOf(ue.Place).Pointer() != reflect.ValueOf(PlaceBundle).Pointer() {
		t.Error("the update places bundles with something other than PlaceBundle")
	}
	if reflect.ValueOf(ue.Firewall).Pointer() != reflect.ValueOf(grantFirewall).Pointer() {
		t.Error("the update makes the firewall grant with something other than the one elevation")
	}
	if reflect.ValueOf(ue.Quit).Pointer() != reflect.ValueOf(quitRunningCopy).Pointer() {
		t.Error("the update quits with something other than the quit the installer sends")
	}
}
