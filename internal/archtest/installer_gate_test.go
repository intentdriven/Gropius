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
	"strconv"
	"strings"
	"testing"
)

// TestTheReleaseGateRunsTheTaggedTreesInstallerBeforeItPublishes holds the
// gate's POSITION, which is the whole of its value: a gate after the publish is
// a report, not a gate.
func TestTheReleaseGateRunsTheTaggedTreesInstallerBeforeItPublishes(t *testing.T) {
	root := repoRootDir(t)
	// Comments are stripped before the ordering below reads positions: this
	// workflow explains itself at length, and a step's own comment naming a
	// later step would otherwise reorder the file as this test sees it.
	job := withoutComments(workflowJob(t, readRepoFile(t, root, filepath.Join(".github", "workflows", "release.yml")), "release"))

	// The gate runs the script out of the checkout. `./install.sh` is the
	// handle: the job checks out the released commit, so the script it runs is
	// the tagged tree's own — the copy on the default branch is a different
	// file exactly when this matters.
	gate := strings.Index(job, "./install.sh")
	if gate < 0 {
		t.Fatal("the release job never runs ./install.sh; the artefacts it publishes are built but not exercised")
	}
	if strings.Contains(job, "raw.githubusercontent.com") {
		t.Error("the release job fetches install.sh over the network; it must run the tagged tree's own copy, " +
			"which is the copy that differs from the default branch when a repair is in flight")
	}
	// And against the artefacts this job just built, not a published Release:
	// there is none yet, which is the point of gating here.
	if !strings.Contains(job, "GROPIUS_ASSET_DIR") {
		t.Error("the release job does not point install.sh at the artefacts it built (GROPIUS_ASSET_DIR); " +
			"it would install the PREVIOUS release and pass while the tagged one is broken")
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

	// The two halves must not drift apart: a gate that sets GROPIUS_ASSET_DIR
	// against a script that ignores it would install the previous release and
	// pass.
	if !strings.Contains(readRepoFile(t, root, "install.sh"), "GROPIUS_ASSET_DIR") {
		t.Error("install.sh reads no GROPIUS_ASSET_DIR; the release gate's assets would be ignored and the " +
			"PREVIOUS release installed instead")
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
	if runtime.GOOS != "darwin" {
		t.Skip("install.sh refuses anything but macOS, and it needs ditto and shasum")
	}
	root := repoRootDir(t)
	floor := plistString(t, filepath.Join(root, "build", "Info.plist"), minimumSystemVersionKey)
	if hostMacOSMajor(t) < majorOf(t, floor) {
		t.Skipf("install.sh refuses this Mac before it reaches the asset directory (it declares a floor of %s)", floor)
	}

	dir := t.TempDir()
	assets := mkdirAll(t, filepath.Join(dir, "assets"))
	home := mkdirAll(t, filepath.Join(dir, "home"))
	stubs := mkdirAll(t, filepath.Join(dir, "stub-bin"))
	reached := filepath.Join(dir, "network-was-reached")

	// An asset that verifies but does not carry the bundle: the script must get
	// far enough to find that out.
	staging := mkdirAll(t, filepath.Join(dir, "staging", "not-the-bundle"))
	if err := os.WriteFile(filepath.Join(staging, "placeholder"), []byte("not a bundle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, dir, "ditto", "-c", "-k", "--keepParent", staging, filepath.Join(assets, "GropiusChat.app.zip"))
	sums := runIn(t, assets, "shasum", "-a", "256", "GropiusChat.app.zip")
	if err := os.WriteFile(filepath.Join(assets, "SHA256SUMS.txt"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}

	// Any reach for the network leaves a trace, and `open` is stubbed so a
	// future change to this test cannot launch anything on a developer's Mac.
	for _, name := range []string{"curl", "gh", "open"} {
		stub := "#!/usr/bin/env bash\ntouch " + strconv.Quote(reached) + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(stubs, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", filepath.Join(root, "install.sh"), "client")
	cmd.Dir = dir
	cmd.Env = append(envWithout(os.Environ(), "PATH", "HOME", "GROPIUS_ASSET_DIR"),
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GROPIUS_ASSET_DIR="+assets,
	)
	out, err := cmd.CombinedOutput()
	got := string(out)
	if err == nil {
		t.Fatalf("install.sh succeeded against an asset carrying no bundle:\n%s", got)
	}

	if _, err := os.Stat(reached); err == nil {
		t.Errorf("install.sh reached for the network although GROPIUS_ASSET_DIR named the assets:\n%s", got)
	}
	for _, want := range []struct{ reached, marker string }{
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
		if !strings.Contains(got, want.marker) {
			t.Errorf("install.sh never %s (no %q in its output):\n%s", want.reached, want.marker, got)
		}
	}
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
