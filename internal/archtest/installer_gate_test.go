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
// a report, not a gate), and holds that it is still a GATE: a step carrying
// `continue-on-error: true`, or an `if:` that can turn it off, is a report too,
// and both of those mutations passed the position-only version of this test.
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
	body := stepRunBody(t, step, gateStepName)

	// The body's own fatality. `set -euo pipefail` is the first thing it does,
	// so nothing runs unguarded ahead of it; and nothing later may take the
	// guard back off, stop early with a success, or swallow a failure. Each of
	// `exit 0` after the `set` line, `set +e`, and `|| true` on an invocation
	// was reproduced and left the step green having installed and verified
	// nothing.
	if first := firstCommand(body); first != "set -euo pipefail" {
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
	} {
		if regexp.MustCompile(neuter.pattern).MatchString(body) {
			t.Errorf("the gate's body contains %s: %s", neuter.what, neuter.why)
		}
	}

	// The work itself, matched as whole lines. Both documented invocations plus
	// the piped form, and the asset directory that points all three at the
	// artefacts THIS job built rather than a published Release — of which there
	// is none yet, which is the point of gating here.
	for _, run := range []struct{ pattern, why string }{
		{`(?m)^[ \t]*export GROPIUS_ASSET_DIR="\$PWD"[ \t]*$`,
			"the gate does not point install.sh at the artefacts it built; it would install the PREVIOUS release and pass while the tagged one is broken"},
		{`(?m)^[ \t]*bash -s -- < \./install\.sh[ \t]*$`,
			"the gate never runs the checked-out install.sh in its documented server form"},
		{`(?m)^[ \t]*bash -s -- client < \./install\.sh[ \t]*$`,
			"the gate never runs the checked-out install.sh in its documented client form"},
		{`(?m)^[ \t]*cat \./install\.sh \| bash -s --[ \t]*$`,
			"the gate never runs install.sh through a real pipe, which is the form the README documents (`curl … | bash`)"},
	} {
		if !regexp.MustCompile(run.pattern).MatchString(body) {
			t.Errorf("no line of the gate's body matches %s: %s", run.pattern, run.why)
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
		if !regexp.MustCompile(bind.pattern).MatchString(body) {
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
		for _, marker := range []string{"gh release create", "gh release upload", "attest-build-provenance"} {
			if strings.Contains(s.block, marker) {
				t.Errorf("the step %q runs %s after the installer gate and declares an `if:`; a conditional publish "+
					"is a publish that can outlive the gate's failure", s.name, marker)
			}
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
// script's integrity control, substituted by one environment variable. It is
// reachable only from the release workflow's gate, and refused everywhere else.
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
			names = append(names, e.Name())
		}
		sort.Strings(names)
	}
	for _, bundle := range []string{"Gropius.app", "GropiusChat.app"} {
		names = append(names, bundle+" = "+bundleFingerprint(filepath.Join("/Applications", bundle)))
	}
	return names
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
// indented deeper than the step's own mapping. Assertions read this rather than
// the step block so that a step's other keys — and its name — cannot satisfy a
// claim about what the step RUNS.
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
	return strings.Join(body, "\n")
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
