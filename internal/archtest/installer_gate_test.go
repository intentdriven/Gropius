package archtest_test

// The release chain must prove the tagged tree before it publishes it.
//
// v0.3.0 was tagged from the commit that cut it and published while an
// install.sh repair was still in flight on the default branch, so the tagged
// tree carried a script that stopped with "DEST?: unbound variable" on its
// first line of work. The chain built that tree, packaged it, attested it and
// released it without ever running the script, and the break was reachable only
// by somebody installing from the tag — which is also why nothing caught it
// (iss-2609081257343394).
//
// Two halves are held here, because either alone is satisfiable by a chain that
// proves nothing: that release.yml executes the installer from the checked-out
// tagged tree between building the artefacts and publishing them, and that the
// installer really does install from the directory that gate hands it rather
// than reaching for a Release that does not exist yet.
//
// WHAT THESE TESTS CLAIM, AND WHAT THEY DO NOT.
//
// Everything below except TestTheInstallerInstallsFromTheAssetDirectoryAndNot-
// TheNetwork and TestTheInstallerRefusesTheAssetDirectorySeamOutsideCI reads
// workflow YAML. Reading YAML cannot establish that a step executes, or that
// executing it can fail the job: those are properties of what GitHub's runner
// does with the file, and nothing in a Go test observes that. Three rounds of
// adversarial review have now found, each time, a further way to write a step
// that satisfies every assertion here and installs nothing —
// `continue-on-error`, an `if:` on the gate, a decoy step with the same name,
// an echo-only body, `exit 0`, `set +e`, `|| true`, a heredoc swallowing the
// whole body, an `if` wrapping it, a `trap … exit 0` on ERR, `{ … } &`, a
// sibling job that publishes, and a whitespace variant defeating a substring
// match. Each round closed what was named; the next found more. That pattern is
// the signature of a syntactic check standing in for a semantic property, and
// it does not terminate.
//
// So these tests are stated as what they are: they REFUSE THE KNOWN WAYS to
// neuter the gate. They are a hurdle in front of an edit that would remove the
// gate's effect, and a place to record each defeat as it is found. They are not
// a proof that the gate runs, and not a proof that it is fatal. A body can
// still be made inert by a form nobody has named yet.
//
// The one assertion here that is not syntactic is the digest binding
// (TestTheGateBindsTheBytesItExercisedToTheBytesPublished): the gate records
// the digests of the three assets it exercised and the publish step re-verifies
// them before its first upload, so if the gate did not run, or ran on other
// bytes, the publish step fails in Actions rather than in a Go test. That is
// the shape to prefer whenever a property can be moved into the workflow
// itself.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// gateStepName anchors every assertion below on the gate step itself. The
// earlier version of this guard anchored on the first textual "./install.sh" in
// the job, which a decoy step (`ls -l ./install.sh`) sitting in the slot between
// packaging and attestation satisfies while the real gate is moved after
// `gh release create` — reproduced, and it left the guard green.
const gateStepName = "Gate the release on the tagged tree's own installer"

// The two steps that turn a build into a published release. Named here because
// the gate's fatality is only worth as much as their unconditionality: a gate
// that fails the job does not stop a step that runs anyway.
const (
	attestStepName  = "Attest the release assets"
	publishStepName = "Publish the Release with both apps attached"
)

// TestTheReleaseGateRunsTheTaggedTreesInstallerBeforeItPublishes holds the
// gate's POSITION, which is the whole of its value (a gate after the publish is
// a report, not a gate), and refuses the known ways of writing a step that
// occupies that position while doing nothing: `continue-on-error: true`, an
// `if:` that can turn it off, a duplicate name shadowing the real step, an
// echo-only body, `exit 0`, `set +e`, `|| true`, a `trap … exit 0`, a
// backgrounded body, a heredoc that swallows the invocations, and a
// conditional wrapping them. Every one of those was reproduced against an
// earlier version of this test and left it green.
//
// It does NOT prove the gate runs, and does not prove the gate is fatal — see
// the note at the top of this file. It is a list of refusals, and the list is
// open.
func TestTheReleaseGateRunsTheTaggedTreesInstallerBeforeItPublishes(t *testing.T) {
	root := repoRootDir(t)
	// Comments are stripped before the ordering below reads positions: this
	// workflow explains itself at length, and a step's own comment naming a
	// later step would otherwise reorder the file as this test sees it.
	job := withoutComments(workflowJob(t, readRepoFile(t, root, filepath.Join(".github", "workflows", "release.yml")), "release"))

	// The gate is found by its own `- name:` line, not by the first mention of
	// the script anywhere in the job, so `gate` is the position of the step
	// that actually does the work.
	//
	// workflowStep takes the FIRST match, so a second step carrying the same
	// name would shadow the real gate: every assertion below would read the
	// decoy, and the gate itself could then be moved after `gh release create`
	// or deleted outright. The name has to identify one step.
	if n := len(stepsNamed(job, gateStepName)); n != 1 {
		t.Fatalf("the release job declares %d steps named %q, want exactly 1: every assertion here anchors on "+
			"that name and reads the FIRST match, so a duplicate shadows the real gate", n, gateStepName)
	}
	step, gate := workflowStep(t, job, gateStepName)

	if strings.Contains(job, "raw.githubusercontent.com") {
		t.Error("the release job fetches install.sh over the network; it must run the tagged tree's own copy, " +
			"which is the copy that differs from the default branch when a repair is in flight")
	}

	// A gate is a step that can fail the job. `continue-on-error: true` demotes
	// it to a report, and an `if:` gives anyone a switch to turn it off; both
	// leave every position assertion above satisfied. Neither key may appear on
	// this step at all — `continue-on-error: false` is allowed by YAML but has
	// no reason to be written, and refusing the key outright means no future
	// edit can flip a literal to an expression that resolves to true.
	for _, forbidden := range []struct{ key, why string }{
		{"continue-on-error", "a step that continues on error cannot fail the job; the gate would report a broken installer and publish it anyway"},
		{"if", "a conditional gate is a gate somebody can switch off; this step must run on every release, unconditionally"},
	} {
		if hasStepKey(step, forbidden.key) {
			t.Errorf("the installer gate declares %q: %s", forbidden.key, forbidden.why)
		}
	}

	for _, after := range []struct{ what, marker string }{
		{"the artefacts are packaged", "shasum -a 256 Gropius.app.zip"},
	} {
		i := strings.Index(job, after.marker)
		if i < 0 {
			t.Fatalf("the release job no longer contains %q; this guard's ordering check reads it as the marker for %s",
				after.marker, after.what)
		}
		if i > gate {
			t.Errorf("the installer gate runs before %s; it must exercise the artefacts that will ship", after.what)
		}
	}
	for _, before := range []struct{ what, marker string }{
		{"the build-provenance attestation", "attest-build-provenance"},
		{"the Release is published", "gh release create"},
		{"the assets are uploaded to an existing Release", "gh release upload"},
	} {
		i := strings.Index(job, before.marker)
		if i < 0 {
			t.Fatalf("the release job no longer contains %q; this guard's ordering check reads it as the marker for %s",
				before.marker, before.what)
		}
		if i < gate {
			t.Errorf("the installer gate runs after %s; a gate that runs after the publish is a report, not a gate", before.what)
		}
	}

	// Everything below reads the gate's shell BODY, and reads it as whole
	// LINES. Substring matching over the step block is satisfied by a step
	// with the right name whose body merely mentions the expected text —
	//
	//	- name: Gate the release on the tagged tree's own installer
	//	  run: echo './install.sh GROPIUS_ASSET_DIR dist/Gropius.app/…'
	//
	// — which was reproduced against the substring version of this guard and
	// left it green while the gate installed nothing. An anchored line is the
	// difference between "the text appears" and "the command runs".
	// The body arrives DEDENTED — the block scalar's common indentation
	// removed, which is what bash is handed — so a line at column 0 below is a
	// line at the body's top level, and an anchored `^` means "run by the body
	// itself" rather than "run somewhere inside something".
	body := stepRunBody(t, step, gateStepName)

	// …and with every heredoc's CONTENT blanked. `cat >/dev/null <<'NOTES'`
	// opened at the top of the body, the whole real body inside it, then the
	// closing `NOTES` and an `echo`, is valid YAML, `bash -n` clean, and leaves
	// every anchored assertion below matching text that never executes —
	// reproduced. Reading the heredoc-blanked body means those assertions can
	// only be satisfied by lines bash would actually run. The gate writes one
	// legitimate heredoc (the `open` stub), which carries none of the text
	// asserted on here.
	live := stripHeredocs(body)

	// The body's own fatality. `set -euo pipefail` is the first thing it does,
	// so nothing runs unguarded ahead of it; and nothing later may take the
	// guard back off, stop early with a success, swallow a failure, hand the
	// failure to a handler that exits 0, or fork the work into a child the step
	// never waits for. Each of `exit 0` after the `set` line, `set +e`, `|| true`
	// on an invocation, `trap … exit 0` on ERR, and `{ … } &` was reproduced and
	// left the step green having installed and verified nothing.
	if first := firstCommand(live); first != "set -euo pipefail" {
		t.Errorf("the gate's first command is %q, want %q: the body has to be under -e, -u and pipefail before "+
			"it does anything, and a command ahead of the `set` line runs unguarded", first, "set -euo pipefail")
	}
	for _, neuter := range []struct{ pattern, what, why string }{
		{`(?m)^[ \t]*set[ \t]+\+`, "set +e / set +o pipefail",
			"it takes the guard back off, and every command after it can fail without failing the step"},
		{`(?m)^[ \t]*exit[ \t]+0[ \t]*$`, "a bare `exit 0`",
			"the step then succeeds having installed and verified however little ran before it, and the release publishes"},
		{`(?m)\|\|[ \t]*(true|:)\b`, "`|| true`",
			"it swallows the failure of whatever it is attached to, which is the one thing this step exists to surface"},
		{`(?m)^[ \t]*trap[ \t]`, "a `trap`",
			"`trap 'echo …; exit 0' ERR` after the `set` line makes the body exit 0 on the first failing command — " +
				"verified directly: `bash -c 'set -euo pipefail; trap \"exit 0\" ERR; false'` exits 0, because the " +
				"handler runs before -e can take the shell down"},
		{`(?m)(^|[^&|>])&[ \t]*$`, "a backgrounded command",
			"`{ … } &` runs the work in a child the step never waits for, so the step's status is the fork's rather " +
				"than the installer's and a broken installer leaves the step green"},
	} {
		if regexp.MustCompile(neuter.pattern).MatchString(live) {
			t.Errorf("the gate's body contains %s: %s", neuter.what, neuter.why)
		}
	}

	// The work itself, matched as whole lines AT THE BODY'S TOP LEVEL. Both
	// documented invocations plus the piped form, and the asset directory that
	// points all three at the artefacts THIS job built rather than a published
	// Release — of which there is none yet, which is the point of gating here.
	lastInvocation := -1
	for _, run := range []struct{ pattern, why string }{
		{`(?m)^export GROPIUS_ASSET_DIR="\$PWD"[ \t]*$`,
			"the gate does not point install.sh at the artefacts it built; it would install the PREVIOUS release and pass while the tagged one is broken"},
		{`(?m)^bash -s -- < \./install\.sh[ \t]*$`,
			"the gate never runs the checked-out install.sh in its documented server form"},
		{`(?m)^bash -s -- client < \./install\.sh[ \t]*$`,
			"the gate never runs the checked-out install.sh in its documented client form"},
		{`(?m)^cat \./install\.sh \| bash -s --[ \t]*$`,
			"the gate never runs install.sh through a real pipe, which is the form the README documents (`curl … | bash`)"},
	} {
		loc := regexp.MustCompile(run.pattern).FindStringIndex(live)
		if loc == nil {
			t.Errorf("no top-level line of the gate's body matches %s: %s", run.pattern, run.why)
			continue
		}
		if loc[0] > lastInvocation {
			lastInvocation = loc[0]
		}
	}

	// A conditional wrapping the body is the remaining named way to keep every
	// line above matching while none of it runs: `if [ "${SOME_FLAG:-0}" = "1" ];
	// then` … `fi` around the preserved body was reproduced, valid YAML and
	// `bash -n` clean. Two things refuse it. An INDENTED wrapper is refused by
	// the `^` anchors above, which require the invocations at the body's top
	// level. An UNINDENTED one is refused here: no compound statement may OPEN
	// at the top level before the last of those invocations. The gate's own
	// `for app in …` loop opens after them, which is why this is bounded rather
	// than blanket — and why moving that loop ahead of the invocations would
	// (correctly) fail.
	if lastInvocation >= 0 {
		if openers := topLevelCompoundOpeners(live, lastInvocation); len(openers) > 0 {
			t.Errorf("the gate's body opens a compound statement at its top level before it runs the installer: %q. "+
				"A conditional, a `{ … }` group or a function definition wrapping the invocations leaves every "+
				"assertion above satisfied while the body runs nothing", openers)
		}
	}

	// The gate must bind what it INSTALLED to what this job BUILT. Asserting
	// only that some .app landed somewhere is satisfied by install.sh falling
	// through to the previous published Release if the asset-directory seam ever
	// stops taking effect while the runner has network — the gate would then go
	// green having never touched the tagged artefacts, which is this gate's own
	// failure mode one level up.
	for _, bind := range []struct{ pattern, why string }{
		{`(?m)^[ \t]*built="dist/Gropius\.app/Contents/MacOS/gropius"[ \t]*$`, "the built server executable"},
		{`(?m)^[ \t]*built="client/dist/GropiusChat\.app/Contents/MacOS/GropiusChat"[ \t]*$`, "the built client executable"},
	} {
		if !regexp.MustCompile(bind.pattern).MatchString(live) {
			t.Errorf("the installer gate never binds what it installed to %s (no line matching %s); "+
				"a fall-through to the PREVIOUS release would install, launch and pass", bind.why, bind.pattern)
		}
	}

	// The two halves must not drift apart: a gate that sets GROPIUS_ASSET_DIR
	// against a script that ignores it would install the previous release and
	// pass.
	installer := readRepoFile(t, root, "install.sh")
	if !strings.Contains(installer, "GROPIUS_ASSET_DIR") {
		t.Error("install.sh reads no GROPIUS_ASSET_DIR; the release gate's assets would be ignored and the " +
			"PREVIOUS release installed instead")
	}
	// install.sh honours that seam only under GITHUB_ACTIONS, so the gate must
	// actually be running there — it is, but a future edit that moves this work
	// into a container or a local rehearsal would silently stop installing from
	// the workspace and start installing the previous release.
	if !strings.Contains(installer, "GITHUB_ACTIONS") {
		t.Error("install.sh no longer restricts GROPIUS_ASSET_DIR to CI; the seam voids the script's only " +
			"integrity control wherever it is honoured")
	}
}

// TestNothingAfterTheInstallerGatePublishesOnAFailedRun holds what the gate's
// fatality is actually worth.
//
// The gate failing the job stops the release only for as long as the steps
// after it are unconditional. `if: ${{ always() }}` on the publish step — a
// plausible edit, and one that sells itself as making re-runs idempotent —
// makes `gh release create` run after the gate has failed: broken installer,
// Release published, assets uploaded, and every assertion in the test above
// still green, because it inspects keys on the GATE and nothing holds the two
// steps to its right. Reproduced.
//
// The tripwire that closes the job is the one step after the gate that may
// carry a condition, and it carries `!cancelled()` on purpose: it asserts the
// job pushed to no branch, which matters most when an earlier step FAILED. It
// publishes nothing, which is the line drawn below.
func TestNothingAfterTheInstallerGatePublishesOnAFailedRun(t *testing.T) {
	root := repoRootDir(t)
	job := withoutComments(workflowJob(t, readRepoFile(t, root, filepath.Join(".github", "workflows", "release.yml")), "release"))
	_, gate := workflowStep(t, job, gateStepName)

	// The two steps that publish must be unconditional, and they must sit after
	// the gate. No `if:` at all, not merely no `if:` that looks dangerous:
	// refusing the key outright is what stops a later edit from writing a
	// condition whose truth has to be reasoned about.
	for _, name := range []string{attestStepName, publishStepName} {
		step, at := workflowStep(t, job, name)
		if at < gate {
			t.Errorf("%q runs BEFORE the installer gate; a gate that runs after the publish is a report, not a gate", name)
		}
		for _, forbidden := range []struct{ key, why string }{
			{"if", "a condition here can be true on a run the gate FAILED (`always()` is the obvious one), and the release then publishes over a broken installer"},
			{"continue-on-error", "a publishing step that continues on error hides its own failure from the job"},
		} {
			if hasStepKey(step, forbidden.key) {
				t.Errorf("%q declares %q: %s", name, forbidden.key, forbidden.why)
			}
		}
	}

	// And no OTHER step to the gate's right may combine a condition with
	// publishing work: that is the same defeat wearing a different name.
	// `always()` is refused outright wherever it appears after the gate — it
	// runs on failure AND on cancellation, and no step in this job needs that.
	for _, s := range workflowSteps(job) {
		if s.at <= gate || !hasStepKey(s.block, "if") {
			continue
		}
		if strings.Contains(s.block, "always()") {
			t.Errorf("the step %q runs after the installer gate under `always()`; that condition is true on a run "+
				"the gate failed, so nothing about the gate's fatality survives it", s.name)
		}
		if routes := publishRoutesIn(s.block); len(routes) > 0 {
			t.Errorf("the step %q runs %s after the installer gate and declares an `if:`; a conditional publish "+
				"is a publish that can outlive the gate's failure", s.name, strings.Join(routes, ", "))
		}
	}
}

// TestNoJobOutsideTheReleaseJobPublishes holds the guard's FIELD OF VIEW.
//
// Both tests above are scoped to the `release` job, and a guard that reads one
// job is blind to the job added beside it. A second job with `needs: verify`,
// `if: ${{ always() }}`, `contents: write` and a `gh release create` leaves the
// `release` job untouched — every assertion above stays green — and publishes a
// Release the installer gate never gated. Reproduced.
//
// So the whole workflow is read here, not one job of it, and two things are
// held. TEXT: no job but `release` may carry a publish route. CAPABILITY: no
// job but `release` may be granted `contents: write`, which is what a Release
// takes; that also bounds the `site` job, whose work lives in another file this
// guard does not read — a called workflow can never exceed the permissions its
// caller grants, so `contents: read` there is a cap, not a promise.
//
// "No job may publish without depending on `release`" is the same property
// stated the other way round and is subsumed: only `release` may publish, and
// `release` is the job the gate sits inside.
func TestNoJobOutsideTheReleaseJobPublishes(t *testing.T) {
	root := repoRootDir(t)
	wf := withoutComments(readRepoFile(t, root, filepath.Join(".github", "workflows", "release.yml")))
	jobs := workflowJobs(t, wf)
	if len(jobs) < 2 {
		t.Fatalf("release.yml declares %d jobs; this guard exists to read the ones BESIDE `release`, and reading "+
			"fewer than two means the job splitter stopped working", len(jobs))
	}

	var release string
	for _, job := range jobs {
		if job.name == "release" {
			release = job.block
			continue
		}
		if routes := publishRoutesIn(job.block); len(routes) > 0 {
			t.Errorf("the job %q publishes (%s). Every assertion about the installer gate is scoped to the `release` "+
				"job, so a sibling job publishes outside all of them — with `needs: verify` it does not even wait "+
				"for the gate's job to finish", job.name, strings.Join(routes, ", "))
		}
		if regexp.MustCompile(`(?m)^      contents:[ \t]*write\b`).MatchString(job.block) {
			t.Errorf("the job %q is granted `contents: write`; only the gated `release` job may hold the permission "+
				"a Release takes, whatever it does with it", job.name)
		}
	}

	// And the release job really does hold the routes named above: if the
	// markers drift, this guard reads a workflow it no longer understands and
	// every assertion in it passes vacuously.
	if release == "" {
		t.Fatal("release.yml declares no `release` job")
	}
	for _, want := range []string{"`gh release create`", "`gh release upload`", "the build-provenance attestation"} {
		found := false
		for _, got := range publishRoutesIn(release) {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the release job carries no %s; this guard recognises publishing by that route, so if it moved "+
				"or was renamed the checks above pass while saying nothing", want)
		}
	}
}

// TestTheGateBindsTheBytesItExercisedToTheBytesPublished closes the gap between
// "the gate ran first" and "the gate ran on THESE bytes".
//
// The gate validates dist/Gropius.app/Contents/MacOS/gropius; the publish step
// uploads Gropius.app.zip. Nothing in the ordering assertions stops a
// "Re-package after the gate" step inserted between the two: the gate passes,
// the artefacts are rebuilt, and the Release ships bytes the gate never saw.
// Reproduced.
//
// BOTH available fixes are taken, because they close different halves.
//
//  1. The DIGEST BINDING, which is the real one and the only assertion in this
//     file that is not a statement about YAML text: the gate records the sha256
//     of the three assets it exercised, and the publish step re-verifies them
//     before its first upload. If the assets changed after the gate ran — or if
//     the gate never ran, so the digest file does not exist — the publish step
//     fails in Actions. That is checked by a runner, not by this test.
//
//  2. NO `run:` BETWEEN THEM. The attestation is a `uses:` step and cannot
//     verify anything itself, so it would attest re-packaged bytes before the
//     publish step got the chance to refuse them. A re-packaging step is a
//     `run:` step, so no step between the gate and the publish may declare one.
//     This half IS a statement about YAML text, and a `uses:` action that
//     re-packages defeats it.
func TestTheGateBindsTheBytesItExercisedToTheBytesPublished(t *testing.T) {
	root := repoRootDir(t)
	job := withoutComments(workflowJob(t, readRepoFile(t, root, filepath.Join(".github", "workflows", "release.yml")), "release"))
	gateStep, gate := workflowStep(t, job, gateStepName)
	pubStep, publish := workflowStep(t, job, publishStepName)

	const digests = `shasum -a 256 Gropius.app.zip GropiusChat.app.zip SHA256SUMS.txt | tee "$RUNNER_TEMP/gate-verified-assets.txt"`
	const verify = `shasum -a 256 -c "$RUNNER_TEMP/gate-verified-assets.txt"`

	gateBody := normalisedShellLines(stripHeredocs(stepRunBody(t, gateStep, gateStepName)))
	if !containsLine(gateBody, digests) {
		t.Errorf("the installer gate records no digests of the assets it exercised (no line %q); without them the "+
			"publish step has nothing to compare against, and a step that re-packages dist/ after the gate ships "+
			"artefacts the gate never saw", digests)
	}

	pubBody := normalisedShellLines(stripHeredocs(stepRunBody(t, pubStep, publishStepName)))
	verifiedAt, publishesAt := -1, -1
	for i, line := range pubBody {
		if verifiedAt < 0 && line == verify {
			verifiedAt = i
		}
		if publishesAt < 0 && len(publishRoutesIn(line)) > 0 {
			publishesAt = i
		}
	}
	switch {
	case verifiedAt < 0:
		t.Errorf("the publish step does not re-verify the gate's digests (no line %q); the ordering assertions say "+
			"the gate ran BEFORE this step, and nothing says it ran on the bytes this step uploads", verify)
	case publishesAt < 0:
		t.Errorf("the publish step %q carries no publish route at all; this guard reads its ordering against one, "+
			"so it no longer says anything", publishStepName)
	case verifiedAt > publishesAt:
		t.Errorf("the publish step uploads (line %d) before it re-verifies the gate's digests (line %d); a check "+
			"after the upload reports on bytes that have already shipped", publishesAt, verifiedAt)
	}

	for _, s := range workflowSteps(job) {
		if s.at <= gate || s.at >= publish {
			continue
		}
		if hasStepKey(s.block, "run") {
			t.Errorf("the step %q runs shell between the installer gate and the publish; a step there is where a "+
				"re-packaging of dist/, client/dist/ or SHA256SUMS.txt would go, and the attestation immediately "+
				"below it would then attest bytes the gate never exercised", s.name)
		}
	}
}

// TestTheInstallerInstallsFromTheAssetDirectoryAndNotTheNetwork runs the real
// install.sh, on a real Mac, against a local asset directory — the seam the
// release gate stands on.
//
// It runs the CLIENT invocation with an asset that unpacks to something other
// than the bundle, so the script stops at its "did not contain" refusal: that
// is after the checksum verification, after the unpacking and after the
// destination choice — the line v0.3.0 died on — and before a single byte is
// written outside this test's temporary directories. `curl` and `gh` are
// stubbed to record any attempt to reach the network, so the seam cannot pass
// by quietly downloading the published release instead.
func TestTheInstallerInstallsFromTheAssetDirectoryAndNotTheNetwork(t *testing.T) {
	fx := installerFixture(t)

	out, err := fx.run(t, "GITHUB_ACTIONS=true")
	if err == nil {
		t.Fatalf("install.sh succeeded against an asset carrying no bundle:\n%s", out)
	}

	if _, err := os.Stat(fx.reached); err == nil {
		t.Errorf("install.sh reached for the network although GROPIUS_ASSET_DIR named the assets:\n%s", out)
	}
	for _, want := range []struct{ reached, marker string }{
		// Honouring the seam is announced, and announced as what it is: the
		// checksum below is verified against a file from the same directory as
		// the bundle, so it attests nothing about origin. Without this, a run
		// installing an attacker-authored zip is indistinguishable in its
		// output from a genuine download.
		{"said it was installing from the asset directory", "NOT from the published GitHub Release"},
		{"said the checksum proves nothing about origin", "NOTHING about the origin"},
		// The checksums file is read from the directory too, and the download
		// is verified against it before anything is unpacked.
		{"verified the asset against the checksums beside it", "Checksum OK."},
		// The line v0.3.0 died on, executed. An installer that stops here
		// stops before it has copied anything, which is what made the break
		// invisible to everything except a real run.
		{"chose a destination and said so", "Installing GropiusChat.app to "},
		// And it read the asset directory's zip rather than a downloaded one.
		{"unpacked the asset from the directory", "did not contain GropiusChat.app"},
	} {
		if !strings.Contains(out, want.marker) {
			t.Errorf("install.sh never %s (no %q in its output):\n%s", want.reached, want.marker, out)
		}
	}
}

// TestTheInstallerRefusesTheAssetDirectorySeamOutsideCI is the seam's own
// boundary.
//
// GROPIUS_ASSET_DIR makes `fetch` serve the bundle AND the SHA256SUMS.txt the
// bundle is checked against out of one caller-named directory, so the
// verification compares bytes with their own digest: an attacker-authored zip
// with a matching checksums file installs, has its quarantine cleared, gets a
// firewall rule and is launched, printing "Checksum OK." with nothing in the
// output to distinguish it from a genuine download. That is the whole of this
// script's integrity control, substituted by two environment variables.
//
// TWO, and both are ordinary environment variables: GROPIUS_ASSET_DIR names the
// directory, and GITHUB_ACTIONS=true is what makes the script honour it. A
// caller who can set one can set the other — install.sh:107 says so in as many
// words — and TestTheInstallerInstallsFromTheAssetDirectoryAndNotTheNetwork,
// forty lines above, is that caller: it sets GITHUB_ACTIONS=true from an
// ordinary `go test` on a developer's Mac and the seam is honoured end to end.
// So this is not a control that makes the seam unreachable outside the release
// workflow. What it does is stop the seam being reached by ACCIDENT — a stray
// export, a copied tutorial line — and make the substitution loud when it does
// happen, which is the claim install.sh:26-33 and :107-108 make and the one to
// keep making here. The test below holds the refusal, not the unreachability.
func TestTheInstallerRefusesTheAssetDirectorySeamOutsideCI(t *testing.T) {
	fx := installerFixture(t)

	// Exactly the run above, minus GITHUB_ACTIONS.
	out, err := fx.run(t)
	if err == nil {
		t.Fatalf("install.sh honoured GROPIUS_ASSET_DIR outside CI:\n%s", out)
	}
	if !strings.Contains(out, "GROPIUS_ASSET_DIR") || !strings.Contains(out, "CI-only") {
		t.Errorf("install.sh failed outside CI without naming GROPIUS_ASSET_DIR as a CI-only seam, so a user "+
			"cannot tell what refused them:\n%s", out)
	}
	// It must refuse BEFORE it uses the directory: reaching the verification at
	// all means it compared the caller's bytes against the caller's digest.
	if strings.Contains(out, "Checksum OK.") {
		t.Errorf("install.sh verified a checksum against the caller's own directory before refusing the seam; "+
			"the refusal has to precede the substituted integrity control:\n%s", out)
	}
	if _, err := os.Stat(fx.reached); err == nil {
		t.Errorf("install.sh fell back to the network after refusing the seam; it must stop:\n%s", out)
	}
}

// TestTheInstallerVerifiesTheArchiveItDownloadedIsInTheChecksums holds the
// scope of the one integrity control in the whole install path.
//
// `shasum -c --ignore-missing` answers for the files the checksums file NAMES.
// Absent names are skipped; names that are present and irrelevant are verified
// and reported as a pass. Nothing in that invocation asserted that the archive
// it protects was in scope, so a checksums file naming any other readable file
// with a correct digest exited 0 with the download never looked at — and the
// unverified archive went on to be unpacked, have its quarantine cleared and
// hand its binary the install (iss-2609120417422598, reproduced against this
// Mac's own /usr/bin/shasum).
//
// The bootstrap now takes the same three moves the update verb took
// (internal/lifecycle/updatefetch.go): the checksums file is narrowed to the
// archive's OWN line before shasum sees it, --ignore-missing goes with the
// narrowing, a line naming a PATH is refused rather than matched, and the pass
// is read line by line rather than as a substring — because
// "/somewhere/GropiusChat.app.zip: OK" ends in the same characters as the line
// this script is looking for.
//
// Narrowing the input is not making the verdict: the digest is still computed
// and compared by /usr/bin/shasum, against a real archive, in every case below.
func TestTheInstallerVerifiesTheArchiveItDownloadedIsInTheChecksums(t *testing.T) {
	const archive = "GropiusChat.app.zip"

	// THE REPRODUCTION, as the capture recorded it: a checksums file naming one
	// readable file that is not the download. /dev/null is readable from every
	// directory and its digest is fixed, which is what made this the cheapest
	// possible forgery — and it exited 0.
	t.Run("names another readable file and not the archive", func(t *testing.T) {
		fx := installerFixture(t)
		fx.writeChecksums(t, digestOf(t, fx.dir, "/dev/null")+"  /dev/null\n")

		out, err := fx.run(t, "GITHUB_ACTIONS=true")
		fx.assertRefusedUnverified(t, out, err, archive)
	})

	// The same hole reached without a readable decoy: a real checksums file for
	// a real release that simply does not cover THIS download. It failed before
	// the change too — shasum reports that no file was verified — but it failed
	// as "corrupt or tampered", which sends a user looking at their network for
	// a checksums file that is intact and answers about something else.
	t.Run("names only the other bundle", func(t *testing.T) {
		fx := installerFixture(t)
		fx.writeChecksums(t, digestOf(t, fx.dir, "/dev/null")+"  Gropius.app.zip\n")

		out, err := fx.run(t, "GITHUB_ACTIONS=true")
		fx.assertRefusedUnverified(t, out, err, archive)
	})

	// A line naming the archive BY A PATH verifies a file that was never
	// downloaded — here the asset directory's own copy, which is a different
	// file from the one in the staging directory even when the bytes agree —
	// and it makes shasum print a success line CONTAINING the one the script
	// looks for. Both halves are refused: the line is not matched, and the pass
	// is read line by line.
	t.Run("names the archive by a path rather than a bare name", func(t *testing.T) {
		fx := installerFixture(t)
		byPath := filepath.Join(fx.assets, archive)
		fx.writeChecksums(t, digestOf(t, fx.dir, byPath)+"  "+byPath+"\n")

		out, err := fx.run(t, "GITHUB_ACTIONS=true")
		fx.assertRefusedUnverified(t, out, err, archive)
	})

	// And the two cases that must not move. The fixture's own checksums file is
	// the honest one: a bare name for the archive, with its real digest.
	t.Run("names the archive with its own digest", func(t *testing.T) {
		fx := installerFixture(t)

		out, err := fx.run(t, "GITHUB_ACTIONS=true")
		if err == nil {
			t.Fatalf("install.sh succeeded against an asset carrying no bundle:\n%s", out)
		}
		if !printedChecksumPass(out) {
			t.Errorf("install.sh refused a checksums file that names the archive with its own digest; the "+
				"narrowing must not cost the honest case:\n%s", out)
		}
		// Past the verification, which is what a pass has to mean here.
		if !strings.Contains(out, "did not contain "+strings.TrimSuffix(archive, ".zip")) {
			t.Errorf("install.sh did not go on to unpack the verified archive:\n%s", out)
		}
	})

	t.Run("names the archive with the wrong digest", func(t *testing.T) {
		fx := installerFixture(t)
		fx.writeChecksums(t, strings.Repeat("0", 64)+"  "+archive+"\n")

		out, err := fx.run(t, "GITHUB_ACTIONS=true")
		if err == nil {
			t.Fatalf("install.sh installed an archive whose digest does not match its line:\n%s", out)
		}
		if printedChecksumPass(out) {
			t.Errorf("install.sh printed a pass for an archive whose digest does not match:\n%s", out)
		}
		if !strings.Contains(out, "corrupt or tampered") {
			t.Errorf("a digest mismatch must still be reported as a mismatch, not as a scope failure:\n%s", out)
		}
	})
}

// assertRefusedUnverified holds what every checksums file that does not cover
// the download has to produce: a non-zero exit, no "Checksum OK.", a message
// naming the archive that was not in scope, and nothing unpacked.
func (fx *fixture) assertRefusedUnverified(t *testing.T, out string, err error, archive string) {
	t.Helper()
	if err == nil {
		t.Fatalf("install.sh exited 0 with %s never verified — the archive was not in the checksums:\n%s", archive, out)
	}
	if printedChecksumPass(out) {
		t.Errorf("install.sh printed a pass although the checksums carry no line for %s; the reassurance is the "+
			"worst part of this failure:\n%s", archive, out)
	}
	if !strings.Contains(out, "no line for "+archive) {
		t.Errorf("the refusal does not say that the checksums carry no line for %s, so a user cannot tell a "+
			"substituted checksums file from a corrupt download:\n%s", archive, out)
	}
	// The refusal has to precede the unpacking: `ditto -x -k` on an unverified
	// archive is the first thing that acts on attacker-chosen bytes.
	if strings.Contains(out, "did not contain ") {
		t.Errorf("install.sh unpacked an archive it never verified:\n%s", out)
	}
}

// printedChecksumPass reports whether install.sh printed its pass — the line
// "Checksum OK." and nothing else. Read as a LINE and not as a substring,
// because the script's own warning about the CI seam quotes the same words
// ("the \"Checksum OK.\" below proves only that the directory is
// self-consistent"), and a substring match therefore reported every refused run
// as a pass.
func printedChecksumPass(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "Checksum OK." {
			return true
		}
	}
	return false
}

// writeChecksums replaces the asset directory's SHA256SUMS.txt — the file the
// download is checked against, which `fetch` serves out of the same directory
// as the download itself under the CI seam.
func (fx *fixture) writeChecksums(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.assets, "SHA256SUMS.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// digestOf is the hex digest /usr/bin/shasum computes for a file, which is what
// makes every forged checksums file above internally correct: each one carries
// a digest that matches the file it names, so what refuses it is the SCOPE and
// nothing else.
func digestOf(t *testing.T, dir, path string) string {
	t.Helper()
	fields := strings.Fields(runIn(t, dir, "shasum", "-a", "256", path))
	if len(fields) == 0 {
		t.Fatalf("shasum said nothing about %s", path)
	}
	return fields[0]
}

// installerFixture builds a local asset directory the installer accepts —
// a zip that verifies against a SHA256SUMS.txt beside it — plus stubs that
// record any reach for the network. The zip deliberately carries no .app, so
// every run stops at install.sh's "did not contain" refusal, which is BEFORE
// the first byte is written to the destination.
type fixture struct {
	dir, assets, home, stubs, reached string
	appsBefore                        []string
}

func installerFixture(t *testing.T) *fixture {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("install.sh refuses anything but macOS, and it needs ditto and shasum")
	}
	root := repoRootDir(t)
	floor := plistString(t, filepath.Join(root, "build", "Info.plist"), minimumSystemVersionKey)
	if hostMacOSMajor(t) < majorOf(t, floor) {
		// Loud in CI. This guard is the only thing that executes install.sh,
		// and a runner image below the bundle's floor turns it into a silent
		// no-op — the failure mode the release gate exists to prevent, one
		// level up. ci.yml pins macos-26 for exactly this reason.
		//
		// COUPLING, recorded rather than removed. Keying the fatality on
		// GITHUB_ACTIONS ties the Go suite to the runner image: raise
		// build/Info.plist's LSMinimumSystemVersion above what GitHub offers
		// — 27, say, on its release day — and `go test ./...` turns red on
		// ci.yml's check job AND on release.yml's verify job, with a message
		// about runner provisioning rather than about the plist that was
		// edited. The alternative is to key it on a variable the workflows
		// set (GROPIUS_INSTALLER_GATE_REQUIRED=1), which decouples "this
		// repository's CI" from "any GitHub Actions runner"; it is not done
		// here because a variable a workflow sets is a variable a workflow
		// edit can unset, and the failure this guard prevents is exactly a
		// silent skip. If the floor is ever raised past the available image,
		// the fix is to pin the image or ship the floor — not to soften this.
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("this runner is macOS %d but install.sh declares a floor of %s, so this guard would not run: "+
				"pin the workflow's runner to a macOS image at or above the floor",
				hostMacOSMajor(t), floor)
		}
		t.Skipf("install.sh refuses this Mac before it reaches the asset directory (it declares a floor of %s)", floor)
	}

	fx := &fixture{dir: t.TempDir()}
	fx.assets = mkdirAll(t, filepath.Join(fx.dir, "assets"))
	fx.home = mkdirAll(t, filepath.Join(fx.dir, "home"))
	fx.stubs = mkdirAll(t, filepath.Join(fx.dir, "stub-bin"))
	fx.reached = filepath.Join(fx.dir, "network-was-reached")

	// An asset that verifies but does not carry the bundle: the script must get
	// far enough to find that out.
	staging := mkdirAll(t, filepath.Join(fx.dir, "staging", "not-the-bundle"))
	if err := os.WriteFile(filepath.Join(staging, "placeholder"), []byte("not a bundle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, fx.dir, "ditto", "-c", "-k", "--keepParent", staging, filepath.Join(fx.assets, "GropiusChat.app.zip"))
	sums := runIn(t, fx.assets, "shasum", "-a", "256", "GropiusChat.app.zip")
	if err := os.WriteFile(filepath.Join(fx.assets, "SHA256SUMS.txt"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}

	// Any reach for the network leaves a trace, and `open` is stubbed so a
	// future change to this test cannot launch anything on a developer's Mac.
	for _, name := range []string{"curl", "gh", "open"} {
		stub := "#!/usr/bin/env bash\ntouch " + strconv.Quote(fx.reached) + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(fx.stubs, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// HOME is sandboxed, but the DESTINATION cannot be: install.sh picks it
	// with `[ -w /Applications ]`, and making that answer differently would
	// mean a second override seam in the script — which is the very thing the
	// seam above is being narrowed for. So the real /Applications is recorded
	// here and compared after every run instead: the fixture is safe by
	// construction (the run dies at "did not contain", before `staged=` is
	// ever evaluated), and this is the tripwire under that reasoning.
	//
	// Names alone would not be a tripwire. On a Mac that ALREADY has Gropius
	// installed — every maintainer's — a fixture edit that supplied a real
	// bundle would OVERWRITE the existing one and leave the name list
	// identical, and this test would pass having clobbered the developer's
	// install. So the two bundles this installer writes are fingerprinted as
	// well; install.sh replaces the whole directory in a staged swap, which
	// changes both the fingerprint's inputs.
	fx.appsBefore = applicationsListing(t)
	return fx
}

// run executes install.sh with the fixture's sandboxed environment plus any
// extra NAME=VALUE settings, and asserts the real /Applications is untouched.
func (fx *fixture) run(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	root := repoRootDir(t)
	cmd := exec.Command("bash", filepath.Join(root, "install.sh"), "client")
	cmd.Dir = fx.dir
	env := append(envWithout(os.Environ(), "PATH", "HOME", "GROPIUS_ASSET_DIR", "GITHUB_ACTIONS"),
		"PATH="+fx.stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+fx.home,
		"GROPIUS_ASSET_DIR="+fx.assets,
	)
	cmd.Env = append(env, extra...)
	out, err := cmd.CombinedOutput()

	if after := applicationsListing(t); !equalStrings(fx.appsBefore, after) {
		t.Errorf("running install.sh changed the real /Applications (%v -> %v); this test must never install "+
			"anything on the machine it runs on", fx.appsBefore, after)
	}
	return string(out), err
}

// applicationsListing is the top-level entries of /Applications, sorted, plus a
// fingerprint of each bundle install.sh would write there. An unreadable or
// absent directory yields only the fingerprints, which compare equal to
// themselves.
//
// The fingerprints are what make this a tripwire on a machine that already has
// the app: a name comparison is blind to a bundle being replaced in place.
func applicationsListing(t *testing.T) []string {
	t.Helper()
	var names []string
	if entries, err := os.ReadDir("/Applications"); err == nil {
		for _, e := range entries {
			if isWritabilityProbe(e.Name()) {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
	}
	for _, bundle := range []string{"Gropius.app", "GropiusChat.app"} {
		names = append(names, bundle+" = "+bundleFingerprint(filepath.Join("/Applications", bundle)))
	}
	return names
}

// isWritabilityProbe reports whether a name is the file the lifecycle
// package's writability probe creates and removes to answer whether this
// account can write a directory (writableDir in internal/lifecycle/doctor.go).
// Choosing the install destination asks that of /Applications, and the test
// that proves the live-environment guard can be opened builds that
// environment — so on a Mac where /Applications is writable, the probe's file
// can exist there for the instant this listing is taken, in a package that
// runs alongside this one. It is not something install.sh writes, and the
// bundle fingerprints beside the listing are what catch an install; the name
// is left out of the comparison rather than read as an install
// (iss-2609120444017291 records the race itself).
func isWritabilityProbe(name string) bool {
	return strings.HasPrefix(name, ".doctor-") && strings.HasSuffix(name, ".tmp")
}

// The listing's one exemption is exactly the probe's name and nothing wider: a
// bundle, a hidden file of another shape, or the probe's name with either end
// changed all still count as a change to /Applications.
func TestTheTripwireExemptsOnlyTheWritabilityProbe(t *testing.T) {
	for name, probe := range map[string]bool{
		".doctor-400466334.tmp": true,
		".doctor-.tmp":          true,
		"Gropius.app":           false,
		".localized":            false,
		".doctor-1.tmp.app":     false,
		"doctor-1.tmp":          false,
		".DS_Store":             false,
	} {
		if got := isWritabilityProbe(name); got != probe {
			t.Errorf("isWritabilityProbe(%q) = %v, want %v", name, got, probe)
		}
	}
}

// bundleFingerprint identifies a bundle directory by its modification time and
// size, or reports it absent. install.sh installs by moving a freshly unpacked
// directory into place, so a reinstall — even over an identical version —
// gives the destination a new mtime.
func bundleFingerprint(path string) string {
	fi, err := os.Lstat(path)
	if err != nil {
		return "absent"
	}
	return fi.ModTime().UTC().Format(time.RFC3339Nano) + " " + strconv.FormatInt(fi.Size(), 10)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// workflowJob returns one job's block from a workflow: its key at two spaces of
// indent, up to the next key at that indent.
func workflowJob(t *testing.T, wf, name string) string {
	t.Helper()
	start := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + `:$`).FindStringIndex(wf)
	if start == nil {
		t.Fatalf("the workflow has no %q job", name)
	}
	rest := wf[start[1]:]
	if end := regexp.MustCompile(`(?m)^  [A-Za-z0-9_-]+:$`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	return rest
}

// workflowJobBlock is one job of a workflow: its name and everything under it.
type workflowJobBlock struct{ name, block string }

// workflowJobs returns every job of a workflow in file order, splitting the
// `jobs:` mapping on its keys at two spaces of indent. Reading one job by name
// — which is what every other helper here does — is blind to a job added beside
// it, and a sibling job with its own publish route was reproduced defeating
// this file's whole first two tests.
func workflowJobs(t *testing.T, wf string) []workflowJobBlock {
	t.Helper()
	start := regexp.MustCompile(`(?m)^jobs:$`).FindStringIndex(wf)
	if start == nil {
		t.Fatal("the workflow declares no `jobs:` mapping; this guard reads every job of it")
	}
	rest := wf[start[1]:]
	heads := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):$`).FindAllStringSubmatchIndex(rest, -1)
	out := make([]workflowJobBlock, 0, len(heads))
	for i, h := range heads {
		end := len(rest)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		out = append(out, workflowJobBlock{name: rest[h[2]:h[3]], block: rest[h[1]:end]})
	}
	return out
}

// ghAPIWritesReleases matches a `gh api` invocation that WRITES: an explicit
// write method, or the field flags that make gh send a POST on their own.
var ghAPIWritesReleases = regexp.MustCompile(`(-X|--method) (POST|PATCH|PUT)|(^| )(-f|-F|--field|--raw-field|--input)[ =]`)

// shellScriptPath matches a path ending in `.sh`. The final segment excludes
// `.` so `github.sha` is not a script.
var shellScriptPath = regexp.MustCompile(`((?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_-]+\.sh)\b`)

// publishRoutesIn names every way the given workflow text puts bytes in front
// of a user, or hands the job to something this guard cannot read.
//
// It replaced three literal `strings.Contains` markers, which were defeated two
// ways, both reproduced creating a real published Release on a run the gate had
// failed: `gh  release  create` with two spaces, and
// `gh api -X POST "repos/${GITHUB_REPOSITORY}/releases" …`, which never spells
// the words at all. Whitespace is normalised before matching, the REST shape is
// recognised, and an invocation of a shell script other than the two this
// workflow is known to run counts as a route on its own — the guard cannot see
// inside a script, so it must not pretend the script is harmless.
//
// This is a list of recognised routes, not a definition of publishing. A route
// nobody has written down yet passes.
func publishRoutesIn(block string) []string {
	var found []string
	seen := map[string]bool{}
	add := func(what string) {
		if !seen[what] {
			seen[what] = true
			found = append(found, what)
		}
	}
	for _, line := range normalisedShellLines(block) {
		for _, r := range []struct{ needle, what string }{
			{"gh release create", "`gh release create`"},
			{"gh release upload", "`gh release upload`"},
			{"gh release edit", "`gh release edit`"},
			{"gh release delete-asset", "`gh release delete-asset`"},
			{"attest-build-provenance", "the build-provenance attestation"},
			{"softprops/action-gh-release", "the action-gh-release publishing action"},
		} {
			if strings.Contains(line, r.needle) {
				add(r.what)
			}
		}
		if strings.Contains(line, "gh api") && strings.Contains(line, "releases") && ghAPIWritesReleases.MatchString(line) {
			add("`gh api` writing to the releases endpoint")
		}
		for _, m := range shellScriptPath.FindAllStringSubmatch(line, -1) {
			switch strings.TrimPrefix(m[1], "./") {
			case "install.sh", "client/build.sh":
				continue
			}
			add("an invocation of " + m[1] + ", which this guard cannot read")
		}
	}
	return found
}

// normalisedShellLines joins line continuations and collapses every run of
// whitespace to a single space, so `gh  release  create` reads as
// `gh release create`. A two-space variant defeated a substring check here, and
// a backslash-newline defeats one just as cheaply.
func normalisedShellLines(block string) []string {
	joined := strings.ReplaceAll(block, "\\\n", " ")
	lines := strings.Split(joined, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	return out
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

// stripHeredocs blanks the CONTENT of every heredoc in a shell body, leaving
// the redirection line, the delimiter line and the line count in place. Text
// inside a heredoc is data, not commands: `cat >/dev/null <<'NOTES'` around the
// whole gate leaves every anchored assertion matching while the body runs
// nothing.
func stripHeredocs(body string) string {
	opener := regexp.MustCompile(`<<(-?)[ \t]*(?:'([^']*)'|"([^"]*)"|\\?([A-Za-z_][A-Za-z0-9_]*))`)
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		m := opener.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		delim, stripTabs := m[2]+m[3]+m[4], m[1] == "-"
		for i+1 < len(lines) {
			i++
			end := lines[i]
			if stripTabs {
				end = strings.TrimLeft(end, "\t")
			}
			if end == delim {
				out = append(out, lines[i])
				break
			}
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

// topLevelCompoundOpeners returns the lines of a dedented shell body that OPEN
// a compound statement at its top level — `if`, `while`, `until`, `for`,
// `case`, `select`, a `{` or `(` group, or a function definition — and start
// before the given offset. A body whose work is wrapped in one of these runs
// none of it unless the wrapper says so.
func topLevelCompoundOpeners(body string, before int) []string {
	opener := regexp.MustCompile(`^(if|while|until|for|case|select)\b|^[{(]|^[A-Za-z_][A-Za-z0-9_]*[ \t]*\([ \t]*\)`)
	var out []string
	off := 0
	for _, line := range strings.Split(body, "\n") {
		start := off
		off += len(line) + 1
		if start >= before {
			break
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if opener.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
}

// workflowStep returns one named step's block from a job, and the offset of its
// `- name:` line within that job. A step starts at `      - ` and runs to the
// next sequence entry at that indent, so the block holds the step's own keys
// and its block scalars and nothing of its neighbours.
func workflowStep(t *testing.T, job, name string) (string, int) {
	t.Helper()
	start := regexp.MustCompile(`(?m)^      - name: ` + regexp.QuoteMeta(name) + `\s*$`).FindStringIndex(job)
	if start == nil {
		t.Fatalf("the release job has no step named %q. This guard anchors every assertion on that step; "+
			"if it was renamed, update gateStepName — if it was removed, nothing proves the tagged tree's "+
			"installer works before the release is published", name)
	}
	block := job[start[0]:]
	// Skip the `- name:` line itself before looking for the next entry.
	nl := strings.IndexByte(block, '\n')
	if nl < 0 {
		return block, start[0]
	}
	if end := regexp.MustCompile(`(?m)^      - `).FindStringIndex(block[nl+1:]); end != nil {
		block = block[:nl+1+end[0]]
	}
	return block, start[0]
}

// stepsNamed returns the offset of every step in a job whose `- name:` line
// carries exactly this name. workflowStep reads the first of them, so more than
// one is a shadowed gate, not a duplicate label.
func stepsNamed(job, name string) []int {
	var at []int
	for _, m := range regexp.MustCompile(`(?m)^      - name: `+regexp.QuoteMeta(name)+`\s*$`).FindAllStringIndex(job, -1) {
		at = append(at, m[0])
	}
	return at
}

// jobStep is one step of a job: its name (empty if it declares none), its block,
// and the offset of the `- ` line that opens it.
type jobStep struct {
	name  string
	block string
	at    int
}

// workflowSteps returns every step of a job in file order. A step opens at
// `      - ` and runs to the next such line, so a step's block holds its own
// keys and its block scalars and nothing of its neighbours — the same slicing
// workflowStep does for one named step.
func workflowSteps(job string) []jobStep {
	starts := regexp.MustCompile(`(?m)^      - `).FindAllStringIndex(job, -1)
	nameOf := regexp.MustCompile(`(?m)^(?:      - |        )name: (.*)$`)
	out := make([]jobStep, 0, len(starts))
	for i, s := range starts {
		end := len(job)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := job[s[0]:end]
		var name string
		if m := nameOf.FindStringSubmatch(block); m != nil {
			name = strings.TrimSpace(m[1])
		}
		out = append(out, jobStep{name: name, block: block, at: s[0]})
	}
	return out
}

// stepRunBody returns the body of a step's `run:` block scalar: the lines
// indented deeper than the step's own mapping, DEDENTED by the block scalar's
// common indentation, which is what YAML hands the shell. Assertions read this
// rather than the step block so that a step's other keys — and its name —
// cannot satisfy a claim about what the step RUNS; and dedenting is what lets
// an assertion say "at the body's top level" rather than "somewhere in it".
func stepRunBody(t *testing.T, step, name string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?m)^        run: [|>][-+]?\s*$`).FindStringIndex(step)
	if loc == nil {
		t.Fatalf("the step %q declares no `run:` block scalar; this guard reads its shell body, and a gate that "+
			"runs no shell installs nothing", name)
	}
	var body []string
	for _, line := range strings.Split(step[loc[1]:], "\n") {
		if strings.TrimSpace(line) == "" {
			body = append(body, "")
			continue
		}
		if leadingSpaces(line) <= 8 {
			break
		}
		body = append(body, line)
	}
	return dedent(body)
}

// dedent removes the common leading indentation of a block scalar's lines,
// which is exactly what YAML strips before the shell sees them: a heredoc
// terminator written flush with the rest of the body is at column 0 for bash,
// and so is the body's top level.
func dedent(lines []string) string {
	common := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := leadingSpaces(line); common < 0 || n < common {
			common = n
		}
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if common > 0 && len(line) >= common {
			line = line[common:]
		}
		out[i] = line
	}
	return strings.Join(out, "\n")
}

// firstCommand returns the first line of a shell body that does anything.
// Comments are already blanked by withoutComments, so a non-blank line is a
// command.
func firstCommand(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// hasStepKey reports whether a step block declares the given key at the step's
// own mapping indent (eight spaces). Anything deeper belongs to a block scalar
// or a nested mapping: the gate's `run:` body is full of shell `if` statements,
// and none of them is a step-level `if:`.
func hasStepKey(step, key string) bool {
	if strings.HasPrefix(step, "      - "+key+":") {
		return true // the key that opens the sequence entry
	}
	return regexp.MustCompile(`(?m)^        ` + regexp.QuoteMeta(key) + `\s*:`).MatchString(step)
}

// withoutComments blanks every YAML/shell comment, leaving the lines in place
// so positions still read in file order.
func withoutComments(block string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		for j := 0; j < len(line); j++ {
			if line[j] != '#' {
				continue
			}
			if j == 0 || line[j-1] == ' ' || line[j-1] == '\t' {
				lines[i] = line[:j]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func hostMacOSMajor(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		t.Skipf("sw_vers: %v", err)
	}
	return majorOf(t, strings.TrimSpace(string(out)))
}

func majorOf(t *testing.T, version string) int {
	t.Helper()
	major, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("%q is not a major.minor version", version)
	}
	return n
}

func mkdirAll(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func runIn(t *testing.T, dir string, name string, arg ...string) string {
	t.Helper()
	cmd := exec.Command(name, arg...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
	return string(out)
}

// envWithout drops the named variables, so the values appended after it are the
// only ones the child can see under either dedup rule.
func envWithout(env []string, names ...string) []string {
	kept := make([]string, 0, len(env))
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		drop := false
		for _, n := range names {
			if name == n {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, e)
		}
	}
	return kept
}
