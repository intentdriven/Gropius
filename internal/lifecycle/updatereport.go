package lifecycle

import (
	"regexp"
	"strconv"
	"strings"
)

// plausibleVersion is the shape a build's own version may take before this
// product will print it as a fact.
//
// IT IS A CHECK ON UNTRUSTED TEXT, not a formatting preference. The version on
// the serving line arrives from whatever holds the loopback port, and the
// challenge that let it in proves a shared DATA ROOT rather than an identity —
// deliberately, at mode 0640, so that a peer account can answer it under the
// shared-cache mode this product documents. So a string from there is a string
// a peer account chose: a newline in it forges lines that read as the report's
// own, and an escape sequence reaches the terminal.
//
// What this cannot check is whether a plausible version is a TRUE one. The
// report's provenance for that line is the control plane, and a report that
// asks is a report that can be told something false; what it must not do is
// let the answer stop being a version at all.
var plausibleVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)

// What an update says when it is over, as a value and a pure rendering of it.
//
// THE FAILURE THIS EXISTS TO REMOVE. An update that reports success while the
// Mac goes on serving the previous version is worse than one that refuses,
// because a person makes decisions on that sentence: they tell a colleague the
// bug is fixed, and the colleague still gets the old behaviour. The swap
// succeeding, the grant being renewed and the launch returning are all true
// statements about a BUNDLE, and on a Mac where another login session holds the
// port none of them is a statement about what answers requests.
//
// So the report carries two facts under two labels — the version installed, and
// the version serving — and says plainly when the second cannot be known rather
// than printing the first in its place.

// portHolder is what update found on the server port.
//
// It is the instance classification with ONE bit added, and that bit is not a
// second judgement of identity: internal/instance folds "nothing is there" and
// "something is there that did not answer" into HolderNone, and an update has
// to tell those two apart to say a true sentence. Whether the holder is
// trustworthy stays instance's question, asked once and not re-asked here.
type portHolder int

const (
	// portIdle: nothing is accepting connections on the port.
	portIdle portHolder = iota
	// portOurs: a Gropius that shares this account's data root answered the
	// challenge — this account's own copy, or, under a shared root, a peer's.
	portOurs
	// portSilent: something accepted a connection and answered no challenge.
	// Under a per-account data root that description fits another account's
	// Gropius exactly, which is why it is not a refusal.
	portSilent
	// portUnproven: something answered the challenge WRONGLY, or no proof
	// could be written into the data root. This is the one ending where
	// nothing is fetched and nothing is placed.
	portUnproven
)

// The sentences the report is made of. They are constants because the tests
// that hold this report to the intent's criteria assert on them by name: a
// wording change then moves the test with the text rather than silently
// passing a rule the words no longer satisfy.
const (
	installedLabel = "installed:"
	servingLabel   = "serving:  "

	// cannotBeDetermined is the first-class answer. It is never replaced by
	// the version just installed — that substitution is the whole failure.
	cannotBeDetermined = "cannot be determined"

	checksumSentence = "The download was verified against the checksums published with the release."

	grantReasonSentence = "The firewall grant is re-made on every update: the build's code identity changes with " +
		"every build, so the entry that covered the previous build does not cover this one."
	grantMadeSentence    = "The firewall grant was re-made for this build."
	grantNotMadeSentence = "The firewall grant was NOT re-made"
	emptyResponseSymptom = "Other machines may see an empty response until an administrator runs:"

	previousReleaseSentence = "There is no way back: only the current release is published, so the previous " +
		"release cannot be fetched."

	didNotInstallSentence = "This Mac is serving a version this command did not install."
	logOutSentence        = "The bundle just placed takes effect when that session logs out, or when Gropius is " +
		"restarted there."
	cannotQuitSentence = "Quitting it is not something this command can do: a quit request reaches only this " +
		"login session."
	// countedNotNamed is how every holder is reported. Which account a process
	// belongs to is that account's business, and this output is written to be
	// pasted into a message to a colleague.
	//
	// What is counted is PROCESSES. One process holds one port, and that is the
	// thing this command looked at; whether it is a login session, an account
	// or a script is not something anything here can see, so it is not
	// something the report says.
	countedNotNamed = "it is counted here and not named"
	heldBySilent    = "is held by 1 process that did not identify itself; " + countedNotNamed + "."
	heldByPeer      = "is held by 1 Gropius that shares this account's data root and is serving a different " +
		"build; " + countedNotNamed + "."
	// heldByUnproven is the ending that is NOT a cross-account story. Something
	// answered the challenge and answered it wrongly, and telling a person to
	// wait for it to log out would be advice about a machine they do not have.
	heldByUnproven = "is held by 1 process that answered this account's identity challenge wrongly; " +
		countedNotNamed + ". Run gropius doctor to see what is there."
	perAccountSentence = "This account's own copy was updated; the copy this Mac serves from is not this " +
		"account's."
)

// Why a serving version is not known. Each is a different thing to have found,
// and a person reading the line can act on the difference.
const (
	servingReasonNoField     = "the running server does not publish its version"
	servingReasonUnanswered  = "the server did not answer the control plane"
	servingReasonSilent      = "something holds the port and answered no identity challenge"
	servingReasonUnproven    = "something holds the port and answered the identity challenge wrongly"
	servingReasonIdle        = "nothing is serving on this Mac"
	servingReasonUnreadable  = "the running server answered with something that is not a version"
	installedReasonNoVersion = "the downloaded build did not answer its own version verb"
)

// bannedTrustWords are the words no line of this report may carry. A download
// checked against published checksums is VERIFIED; it is not signed, notarised
// or trusted, and a reader takes any of those three as Gatekeeper's verdict,
// which no control in this product can support.
var bannedTrustWords = []string{"signed", "notarised", "notarized", "trusted"}

// updateReport is everything an update did, as a value. Every assertion the
// intent makes is made against this and its rendering, with no process, no
// panel, no network and no Mac.
type updateReport struct {
	// Installed is the version placed, empty when the downloaded build would
	// not say what it is.
	Installed string
	// InstalledReason says why Installed is empty.
	InstalledReason string
	// Serving is the version this Mac is answering with, empty when it cannot
	// be determined.
	Serving string
	// ServingReason says why Serving is empty. It is never a stand-in for the
	// installed version.
	ServingReason string
	// Holder is what held the port when the command returned — the verdict is
	// what was true at the end, not what was true before the swap.
	Holder portHolder
	// Placed is whether a new bundle reached the destination.
	Placed bool
	// Dest is where the bundle belongs, and Home is this account's home, which
	// every line is redacted against.
	Dest string
	Home string
	Port int

	// SwapFailure is what stopped the swap, already carrying the placer's own
	// account of where things are.
	SwapFailure string
	// DestVersion is the version at the destination after a failed swap, empty
	// when it could not be read from here.
	DestVersion string
	// KeptStaging is the directory holding the only remaining copy of the
	// application, on the one failure path that keeps one.
	KeptStaging string

	// GrantAttempted is whether the firewall grant was reached at all.
	GrantAttempted bool
	// GrantMade is whether it was made. A declined panel is not a failed run.
	GrantMade     bool
	GrantDetail   string
	GrantCommands []string
}

// servedByAnother reports whether this Mac is answering from something this
// command did not put there.
//
// Two ways to know it, and both are things that were observed rather than
// assumed: the running server gave a version and it is not the one just
// installed, or something holds the port and would not identify itself — which,
// under a per-account data root, is exactly what another account's Gropius
// looks like from here.
func (r updateReport) servedByAnother() bool {
	if r.Holder == portSilent {
		return true
	}
	return r.Serving != "" && r.Installed != "" && r.Serving != r.Installed
}

// holderLine says what is on the port, counted rather than named, and says it
// as the two cases genuinely differ: something that would not identify itself
// at all, and a Gropius that proved it shares this account's data root and is
// serving a different build.
func (r updateReport) holderLine() string {
	held := heldBySilent
	if r.Holder == portOurs {
		held = heldByPeer
	}
	return "port " + strconv.Itoa(r.Port) + " " + held
}

// perAccountInstall reports whether the destination is inside this account's
// own home, which is the installation no other account launches.
func (r updateReport) perAccountInstall() bool {
	return r.Home != "" && strings.HasPrefix(r.Dest, r.Home+"/")
}

// updateLines is the whole rendering: a slice of lines, pure over the report,
// every one of them redacted.
//
// A slice rather than a writer so the wording rules can be asserted line by
// line, which is what "neither version is ever printed under the other's label"
// needs in order to mean anything.
func updateLines(r updateReport) []string {
	out := []string{
		installedLabel + " " + r.installedText(),
		servingLabel + " " + r.servingText(),
	}
	if r.SwapFailure != "" {
		out = append(out, r.swapFailureLines()...)
	}
	if r.servedByAnother() {
		out = append(out, didNotInstallSentence, logOutSentence, r.holderLine(), cannotQuitSentence)
		if r.perAccountInstall() {
			out = append(out, perAccountSentence)
		}
	}
	if r.Holder == portUnproven {
		out = append(out, "port "+strconv.Itoa(r.Port)+" "+heldByUnproven)
	}
	if r.Placed {
		out = append(out, checksumSentence)
	}
	if r.GrantAttempted {
		out = append(out, r.grantLines()...)
	}
	out = append(out, previousReleaseSentence)

	for i, line := range out {
		out[i] = redact(line, r.Home)
	}
	return out
}

// installedText is the first fact: the version placed, and where.
func (r updateReport) installedText() string {
	if !r.Placed {
		return "nothing — no bundle was placed at " + r.Dest
	}
	version := r.Installed
	if version == "" {
		version = cannotBeDetermined + " — " + r.InstalledReason
	}
	return version + ", at " + r.Dest
}

// servingText is the second fact, and it is a separate fact. Where it is not
// known it says so and says why; the version just installed never appears here.
func (r updateReport) servingText() string {
	if r.Serving != "" {
		return r.Serving + ", on port " + strconv.Itoa(r.Port)
	}
	// Every caller of this report fills in a reason: finishUpdate assigns one
	// in all four arms of its switch. An empty one would be a caller that
	// forgot, and a report that said only "cannot be determined" with no
	// account of why is the kind of line this verb exists to remove.
	return cannotBeDetermined + " — " + r.ServingReason
}

// swapFailureLines say what stopped the swap and what this Mac has now. The
// placer's own message already names the kept staging directory where there is
// one; what is added here is the version at the destination, which is the
// question a person actually has.
func (r updateReport) swapFailureLines() []string {
	lines := []string{"The swap stopped: " + r.SwapFailure}
	switch {
	case r.DestVersion != "":
		lines = append(lines, "The application at "+r.Dest+" is still "+r.DestVersion+".")
	case r.KeptStaging != "":
		lines = append(lines, "The only remaining copy of the application is at "+r.KeptStaging+
			"; move it to "+r.Dest+".")
	default:
		lines = append(lines, "The version at "+r.Dest+" "+cannotBeDetermined+" from here.")
	}
	return lines
}

// grantLines report the grant as an act this command performed — made or not
// made — and never as the observed firewall query doctor is carefully hedged
// about. The reason is on its own line either way, so the authorisation panel
// reads as expected rather than as a fault.
func (r updateReport) grantLines() []string {
	lines := []string{grantReasonSentence}
	if r.GrantMade {
		return append(lines, grantMadeSentence)
	}
	detail := ""
	if r.GrantDetail != "" {
		detail = " (" + r.GrantDetail + ")"
	}
	lines = append(lines, grantNotMadeSentence+detail+". The bundle above is in place; this Mac serves loopback.",
		emptyResponseSymptom)
	for _, c := range r.GrantCommands {
		lines = append(lines, "  "+c)
	}
	return lines
}

// renderUpdate writes the report for a person.
func renderUpdate(t Terminal, r updateReport) {
	for _, line := range updateLines(r) {
		writeLine(t.Out, line)
	}
}
