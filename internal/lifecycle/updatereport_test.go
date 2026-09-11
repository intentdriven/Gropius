package lifecycle

import (
	"path/filepath"
	"strings"
	"testing"
)

// The report `gropius update` ends on, as a value, and every assertion the
// intent makes about what a person reads.
//
// The value is what the assertions are made against rather than a run: an
// update quits an application, downloads a release, raises an authorisation
// panel and replaces a bundle, and none of that has to happen for the question
// "does this report say five true things" to be answerable. The behaviour that
// PRODUCES these values is tested beside this, through the seams.

// reportHome and reportDest are the persona's account and the bundle this Mac
// launches. The home is spelled the way doctor's redaction corpus spells one:
// a path with no real account name in it.
var (
	reportHome    = filepath.Join(string(filepath.Separator), "somewhere", "an-account")
	reportDest    = filepath.Join(string(filepath.Separator), "Applications", "Gropius.app")
	perAccountDir = filepath.Join(reportHome, "Applications", "Gropius.app")
)

// updateReportCases is the table every assertion below runs over: one entry per
// ending an update can have. A row added here is a row every rule in this file
// then covers, which is the point of driving all of them from one table.
func updateReportCases() map[string]updateReport {
	base := updateReport{
		Installed:      "0.5.0",
		Serving:        "0.5.0",
		Holder:         portOurs,
		Placed:         true,
		Dest:           reportDest,
		Home:           reportHome,
		Port:           11535,
		GrantAttempted: true,
		GrantMade:      true,
	}
	cases := map[string]updateReport{}

	cases["the new version is the one serving"] = base

	differs := base
	differs.Serving = "0.4.0"
	cases["another account is serving the previous version"] = differs

	noField := base
	noField.Serving = ""
	noField.ServingReason = servingReasonNoField
	cases["the running server publishes no version"] = noField

	silent := base
	silent.Holder = portSilent
	silent.Serving = ""
	silent.ServingReason = servingReasonSilent
	cases["something holds the port and answers no challenge"] = silent

	unanswered := base
	unanswered.Serving = ""
	unanswered.ServingReason = servingReasonUnanswered
	cases["the control plane did not answer"] = unanswered

	idle := base
	idle.Holder = portIdle
	idle.Serving = ""
	idle.ServingReason = servingReasonIdle
	cases["nothing is on the port"] = idle

	declined := base
	declined.GrantMade = false
	declined.GrantDetail = "User canceled."
	declined.GrantCommands = firewallGrantCommands(filepath.Join(reportDest, binaryInBundle), reportHome)
	cases["the authorisation panel was declined"] = declined

	perAccount := silent
	perAccount.Dest = perAccountDir
	cases["a per-account installation while another account serves"] = perAccount

	noVersion := base
	noVersion.Installed = ""
	noVersion.InstalledReason = installedReasonNoVersion
	cases["the downloaded build would not say what it is"] = noVersion

	failed := updateReport{
		Holder:        portOurs,
		Dest:          reportDest,
		Home:          reportHome,
		Port:          11535,
		SwapFailure:   "move the new bundle into place: file exists (the previous copy is left as it was)",
		DestVersion:   "0.4.0",
		Serving:       "",
		ServingReason: servingReasonNoField,
	}
	cases["the swap stopped and the previous bundle is back"] = failed

	kept := failed
	kept.DestVersion = ""
	kept.KeptStaging = filepath.Join(reportDest, "..", ".gropius-incoming-1234", "Gropius.app.retired")
	kept.SwapFailure = "the installed bundle was already set aside and is intact at " + kept.KeptStaging
	cases["the swap stopped and the only copy is in staging"] = kept

	return cases
}

// The five facts, every time, and the two versions on two lines.
//
// This is the whole of the intent's headline: a report that offers what was
// written to disk as a statement about what the machine is answering with is
// the failure this verb exists to remove, and two labels are what keep the two
// facts apart.
func TestTheUpdateReportCarriesTheFiveFactsOnSeparateLines(t *testing.T) {
	for name, r := range updateReportCases() {
		t.Run(name, func(t *testing.T) {
			lines := updateLines(r)
			text := strings.Join(lines, "\n")

			installed := lineWithLabel(t, lines, installedLabel)
			serving := lineWithLabel(t, lines, servingLabel)
			if installed == serving {
				t.Fatalf("the installed and serving facts are on one line:\n%s", text)
			}

			// Fact one and fact two: each version under its own label, and
			// never under the other's.
			if r.Installed != "" && !strings.Contains(installed, r.Installed) {
				t.Errorf("the installed line does not carry the version installed (%q):\n%s", r.Installed, installed)
			}
			if r.Serving != "" && !strings.Contains(serving, r.Serving) {
				t.Errorf("the serving line does not carry the version serving (%q):\n%s", r.Serving, serving)
			}
			if r.Serving == "" && !strings.Contains(serving, cannotBeDetermined) {
				t.Errorf("the serving version is unknown and the line does not say so:\n%s", serving)
			}
			// A build that would not say what it is reads as unknown; a run
			// that placed nothing reads as nothing installed. They are
			// different things to have happened and the line says which.
			if r.Installed == "" && r.Placed && !strings.Contains(installed, cannotBeDetermined) {
				t.Errorf("the installed version is unknown and the line does not say so:\n%s", installed)
			}
			if !r.Placed && !strings.Contains(installed, "nothing") {
				t.Errorf("nothing was placed and the installed line does not say so:\n%s", installed)
			}

			// Fact five: there is no way back, said rather than implied.
			if !strings.Contains(text, previousReleaseSentence) {
				t.Errorf("the report does not say the previous release cannot be fetched:\n%s", text)
			}

			// Facts three and four exist wherever a bundle was placed: a run
			// that placed nothing has no checksum to report and made no grant.
			if r.Placed {
				if !strings.Contains(text, checksumSentence) {
					t.Errorf("a bundle was placed and the report does not name the checksums:\n%s", text)
				}
				if !strings.Contains(text, grantReasonSentence) {
					t.Errorf("the report does not say why the grant has to be re-made:\n%s", text)
				}
			}
		})
	}
}

// Neither version is ever printed where the other was asked for. This is the
// one substitution the intent forbids by name, and it is the cheap mistake: the
// version just installed is to hand, and printing it on the serving line turns
// a true sentence about a bundle into a false one about a machine.
func TestTheUpdateReportNeverPrintsOneVersionUnderTheOthersLabel(t *testing.T) {
	for name, r := range updateReportCases() {
		if r.Installed == "" || r.Installed == r.Serving {
			continue
		}
		t.Run(name, func(t *testing.T) {
			serving := lineWithLabel(t, updateLines(r), servingLabel)
			if strings.Contains(serving, r.Installed) {
				t.Errorf("the serving line carries the version that was INSTALLED (%q):\n%s", r.Installed, serving)
			}
		})
	}
}

// "Cannot be determined" is an answer, and the version just installed is never
// put in its place. Three ways it goes unknown, and none of them may borrow the
// installed version.
func TestAnUndeterminableServingVersionIsSaidAndNeverSubstituted(t *testing.T) {
	for name, r := range updateReportCases() {
		if r.Serving != "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			serving := lineWithLabel(t, updateLines(r), servingLabel)
			if r.Installed != "" && strings.Contains(serving, r.Installed) {
				t.Errorf("the serving version is unknown and the line prints the installed one (%q):\n%s",
					r.Installed, serving)
			}
			if r.ServingReason != "" && !strings.Contains(serving, r.ServingReason) {
				t.Errorf("the serving line does not say WHY the version is unknown:\n%s", serving)
			}
		})
	}
}

// The wording rule, over every line the report can produce: a download checked
// against published checksums is verified, and it is not signed, notarised or
// trusted. Those three words are what a reader would take as Gatekeeper's
// verdict, which no control here can support.
func TestTheUpdateReportNeverCallsAReleaseSignedOrNotarised(t *testing.T) {
	for name, r := range updateReportCases() {
		t.Run(name, func(t *testing.T) {
			text := strings.ToLower(strings.Join(updateLines(r), "\n"))
			for _, banned := range bannedTrustWords {
				if strings.Contains(text, banned) {
					t.Errorf("the report says %q, which claims a control this product does not have:\n%s",
						banned, text)
				}
			}
		})
	}
}

// And there is no way back, in any rendering: no downgrade instruction, no
// version flag, and "rollback" nowhere as an offer. One release is published at
// a time, so a path back does not exist and the report must not imply one.
func TestTheUpdateReportOffersNoWayBack(t *testing.T) {
	for name, r := range updateReportCases() {
		t.Run(name, func(t *testing.T) {
			text := strings.ToLower(strings.Join(updateLines(r), "\n"))
			for _, offer := range []string{"rollback", "roll back", "downgrade", "--version", "reinstall the previous"} {
				if strings.Contains(text, offer) {
					t.Errorf("the report offers %q, and there is no release to go back to:\n%s", offer, text)
				}
			}
		})
	}
}

// Every line is written to be pasted into a message to a colleague, so doctor's
// redaction runs over the whole report rather than per line. An account name is
// exactly what a person does not notice themselves sending.
func TestEveryLineOfTheUpdateReportIsRedacted(t *testing.T) {
	for name, r := range updateReportCases() {
		t.Run(name, func(t *testing.T) {
			for _, line := range updateLines(r) {
				if strings.Contains(line, reportHome) {
					t.Errorf("a report line carries this account's home directory:\n%s", line)
				}
			}
		})
	}
}

// A declined panel is not a failed run, and the report has to read that way:
// the swap still stands, the grant is reported as not made, and the commands
// that make it by hand are there with the symptom to expect until they are run.
func TestADeclinedPanelIsReportedAsTheSwapStandingAndTheGrantNotMade(t *testing.T) {
	r := updateReportCases()["the authorisation panel was declined"]
	text := strings.Join(updateLines(r), "\n")

	if !strings.Contains(text, checksumSentence) {
		t.Errorf("the swap is not reported as done:\n%s", text)
	}
	if !strings.Contains(text, grantNotMadeSentence) {
		t.Errorf("the grant is not reported as NOT made:\n%s", text)
	}
	if !strings.Contains(text, emptyResponseSymptom) {
		t.Errorf("the report does not name the symptom to expect:\n%s", text)
	}
	for _, command := range r.GrantCommands {
		if !strings.Contains(text, command) {
			t.Errorf("the report does not carry the command that makes the grant by hand: %s\n%s", command, text)
		}
	}
}

// Where this Mac is answering from something this command did not put there,
// the report says so, names the two things that finish the job, and counts the
// other session rather than naming it.
func TestTheReportSaysWhenThisMacIsServingSomethingItDidNotInstall(t *testing.T) {
	for _, name := range []string{
		"another account is serving the previous version",
		"something holds the port and answers no challenge",
		"a per-account installation while another account serves",
	} {
		t.Run(name, func(t *testing.T) {
			r := updateReportCases()[name]
			text := strings.Join(updateLines(r), "\n")

			if !strings.Contains(text, didNotInstallSentence) {
				t.Errorf("the report does not say this Mac is serving something this command did not install:\n%s", text)
			}
			if !strings.Contains(text, logOutSentence) {
				t.Errorf("the report does not name logging out and restarting as what finishes the job:\n%s", text)
			}
			if !strings.Contains(text, cannotQuitSentence) {
				t.Errorf("the report does not say that quitting it is not something this command can do:\n%s", text)
			}
			// Counted, and counted as something that was actually observed:
			// one process holds a port, which is a fact about the port. What
			// it is a process OF is not, so the count is of processes.
			if !strings.Contains(text, countedNotNamed) {
				t.Errorf("the report does not count the holder rather than naming it:\n%s", text)
			}
			// What is counted is PROCESSES, because one process holds one
			// port and that is the thing this command looked at. Counting
			// login sessions or accounts would be counting something nothing
			// here can see.
			for _, invented := range []string{"1 other login session", "1 other account", "1 account"} {
				if strings.Contains(text, invented) {
					t.Errorf("the report counts %q, which nothing here observed:\n%s", invented, text)
				}
			}
		})
	}

	// And the ordinary ending does not carry any of it: a Mac serving the
	// version just installed has no cross-account story to tell.
	plain := strings.Join(updateLines(updateReportCases()["the new version is the one serving"]), "\n")
	if strings.Contains(plain, didNotInstallSentence) {
		t.Errorf("a plain success reports a cross-account ending:\n%s", plain)
	}
}

// A per-account installation changes what was updated and what was not, and the
// report has to separate them: this account's copy is new, and the Mac's server
// is not this account's.
func TestAPerAccountInstallationSaysWhoseCopyWasUpdated(t *testing.T) {
	r := updateReportCases()["a per-account installation while another account serves"]
	text := strings.Join(updateLines(r), "\n")
	if !strings.Contains(text, perAccountSentence) {
		t.Errorf("the report does not say this account's copy was updated and this Mac's server was not:\n%s", text)
	}
	// And the machine-wide case does not claim it.
	shared := strings.Join(updateLines(updateReportCases()["something holds the port and answers no challenge"]), "\n")
	if strings.Contains(shared, perAccountSentence) {
		t.Errorf("a machine-wide installation is reported as a per-account one:\n%s", shared)
	}
}

// A swap that stopped says which version is at the destination, or where the
// only remaining copy is. Either way a person knows what their Mac has.
func TestAFailedSwapNamesWhatIsAtTheDestination(t *testing.T) {
	restored := updateReportCases()["the swap stopped and the previous bundle is back"]
	text := strings.Join(updateLines(restored), "\n")
	if !strings.Contains(text, restored.DestVersion) {
		t.Errorf("a failed swap does not name the version at the destination:\n%s", text)
	}
	if !strings.Contains(text, restored.SwapFailure) {
		t.Errorf("a failed swap does not say what stopped it:\n%s", text)
	}
	if strings.Contains(text, checksumSentence) {
		t.Errorf("nothing was placed and the report claims a placement:\n%s", text)
	}

	kept := updateReportCases()["the swap stopped and the only copy is in staging"]
	keptText := strings.Join(updateLines(kept), "\n")
	if !strings.Contains(keptText, redact(kept.KeptStaging, kept.Home)) {
		t.Errorf("the kept staging directory is not named:\n%s", keptText)
	}
}

// lineWithLabel finds the one line carrying a label, and fails when there is
// not exactly one: two lines under one label is the ambiguity this report
// exists to remove.
func lineWithLabel(t *testing.T, lines []string, label string) string {
	t.Helper()
	var found []string
	for _, line := range lines {
		if strings.HasPrefix(line, label) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d lines begin with %q, want exactly one:\n%s", len(found), label, strings.Join(lines, "\n"))
	}
	return found[0]
}
