package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Fetching a release and verifying it, exactly as the bootstrap does.
//
// WHY THE SAME TOOLS AND NOT net/http AND crypto/sha256. The bootstrap and this
// verb must agree on what "verified" means. One verification path is one thing
// to keep right; two are two that can drift, and the one that drifts is the one
// nobody runs. The behaviours /usr/bin/curl and /usr/bin/shasum already have
// here — a curlrc that cannot re-point the connection, HTTPS pinned across
// redirects, `--ignore-missing` proven against that exact binary — are
// behaviours this verb inherits rather than re-establishes. The cost is two
// subprocesses, both named by absolute path.
//
// WHY THERE IS NO ASSET-DIRECTORY SEAM. The bootstrap has one, refused outside
// CI, because the release workflow has to run the installer against artefacts
// it has just built. A verb a person types on a Mac has no CI case at all, so
// the origin is a constant in this file: no environment variable and no flag
// can point the download, or the checksums that verify it, at anywhere else. A
// caller who could set one variable would otherwise substitute the whole
// integrity control silently, because the checksums would be read from the same
// place as the bundle and the verification would prove only that a directory is
// self-consistent.

// The release, and the two assets an update reads from it.
const (
	// releaseAssetBase is where the current release's assets live. `latest`
	// rather than a version, because only the current release is published and
	// there is nothing else to ask for.
	releaseAssetBase = "https://github.com/intentdriven/Gropius/releases/latest/download/"
	// updateArchiveName is the server bundle. The client bundle is the
	// bootstrap's to place; a verb on the server binary cannot be the remedy
	// for a Mac that carries only the client.
	updateArchiveName = "Gropius.app.zip"
	// checksumsName is the digest file published beside it, in the same
	// release.
	checksumsName = "SHA256SUMS.txt"
)

// The causes a verification can fail for, in the words the failure uses. They
// are constants because a person acts on the difference: a mismatch is a
// corrupt or tampered download, while a checksums file that is not one is
// usually a proxy or a captive portal answering with a page.
const (
	causeNoChecksums    = "the checksums file was not downloaded"
	causeEmptyChecksums = "the checksums file is empty"
	causeNotChecksums   = "what arrived where the checksums were asked for is not a checksums file"
	causeMismatch       = "the download does not match the checksums published with the release"
	causeNamesNothing   = "the checksums file names no file that was downloaded"
)

// The two bounded tools this file starts. Neither waits on a person.
const (
	fetchTimeout   = 5 * time.Minute
	verifyTimeout  = 2 * time.Minute
	unpackTimeout  = 2 * time.Minute
	versionTimeout = 30 * time.Second
)

// curlArgs is the whole command line the fetch runs, as a value, so the
// origin and the transport can be asserted without starting anything.
func curlArgs(name, dest string) []string {
	return []string{
		"/usr/bin/curl",
		// -q first: ignore any curlrc that could re-point the connection while
		// the URL still reads github.com.
		"-q",
		// HTTPS pinned end to end, redirects included. The asset and the
		// checksums that verify it come from this same origin, so the
		// transport is the thing to pin.
		"--proto", "=https",
		"--proto-redir", "=https",
		"-fsSL",
		"-o", dest,
		releaseAssetBase + name,
	}
}

// fetchAsset downloads one release asset. The origin comes from curlArgs and
// from nowhere else.
func fetchAsset(name, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	args := curlArgs(name, dest)
	cmd := exec.CommandContext(ctx, "/usr/bin/curl", args[1:]...)
	// Nothing reads standard input, here or anywhere in this package.
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err := toolError(ctx, "/usr/bin/curl", fetchTimeout, out, err); err != nil {
		return fmt.Errorf("%s could not be downloaded from the current release: %w", name, err)
	}
	return nil
}

// verifyChecksums checks the downloaded archive against the checksums
// published beside it, and refuses on anything but a match.
//
// The VERDICT is /usr/bin/shasum's, always: this function reads the checksums
// file only after that verdict is in, and only to say which of the causes
// fired. A classification that could pass an input shasum refused would be a
// second integrity control, and a worse one.
func verifyChecksums(dir string) error {
	sums := filepath.Join(dir, checksumsName)
	if _, err := os.Stat(sums); err != nil {
		return fmt.Errorf("%s: the release did not serve one, or it could not be written", causeNoChecksums)
	}

	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()
	// --ignore-missing skips the lines for the other assets of the same
	// release, which is why the file can name every one of them.
	cmd := exec.CommandContext(ctx, "/usr/bin/shasum", "-a", "256", "-c", "--ignore-missing", checksumsName)
	cmd.Dir = dir
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("/usr/bin/shasum did not answer within %s", verifyTimeout)
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s (%s)", checksumFailureCause(string(out), sums), strings.TrimSpace(redactNewlines(string(out))))
}

// checksumFailureCause names which of the causes fired, from what shasum said
// and — only to tell an empty file from a page that is not a checksums file at
// all — from the file itself.
func checksumFailureCause(output, sums string) string {
	switch {
	// Checked first: a mismatch ALSO reports that no file was verified, so
	// reading that line first would report every corrupt download as a
	// checksums file naming nothing.
	case strings.Contains(output, ": FAILED"), strings.Contains(output, "did NOT match"):
		return causeMismatch
	case strings.Contains(output, "no properly formatted"):
		if body, err := os.ReadFile(sums); err == nil && strings.TrimSpace(string(body)) == "" {
			return causeEmptyChecksums
		}
		return causeNotChecksums
	case strings.Contains(output, "no file was verified"):
		return causeNamesNothing
	default:
		return "the download could not be verified against the checksums published with the release"
	}
}

// redactNewlines folds a tool's several lines into one, so a failure is one
// sentence rather than a block with a message wrapped around it.
func redactNewlines(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// unpackArchive extracts the verified archive and clears the quarantine
// attribute on what came out, in that order and no other: a bundle is
// un-quarantined only once it is known to be the exact artefact the release
// workflow built.
func unpackArchive(archive, into string) error {
	ctx, cancel := context.WithTimeout(context.Background(), unpackTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", archive, into)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err := toolError(ctx, "/usr/bin/ditto", unpackTimeout, out, err); err != nil {
		return fmt.Errorf("%s could not be unpacked: %w", filepath.Base(archive), err)
	}
	return nil
}

// clearQuarantine removes the quarantine attribute from a verified bundle. A
// curl download is usually not quarantined at all, but a proxy or an earlier
// run may have tagged it, and a tagged bundle will not launch.
func clearQuarantine(bundle string) {
	ctx, cancel := context.WithTimeout(context.Background(), unpackTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/xattr", "-dr", "com.apple.quarantine", bundle)
	cmd.Stdin = nil
	// Tolerated: there is nothing to remove on most Macs, and xattr says so
	// with a non-zero status. The bootstrap does the same for the same reason.
	_ = cmd.Run()
}

// checkStagedBundle refuses a downloaded bundle this verb must not run.
//
// `ditto -x -k` restores symbolic links from the archive and `-x` follows one,
// so a link where the program belongs would send the one exec in this path
// somewhere outside the directory that was verified. Only a compromised release
// can plant one — which is what the checksums are for — and the refusal costs a
// line and does not depend on that being true.
func checkStagedBundle(bundle string) error {
	program := filepath.Join(bundle, binaryInBundle)
	fi, err := os.Lstat(program)
	if err != nil {
		return fmt.Errorf("the downloaded bundle carries no %s", binaryInBundle)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("the downloaded bundle carries a symbolic link where %s should be", binaryInBundle)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("the downloaded bundle has something other than a program at %s", binaryInBundle)
	}
	return nil
}

// versionLine is how a build spells its own version: the word this command is
// called by, and then the build.
var versionLine = regexp.MustCompile(`^gropius\s+(\S+)$`)

// stagedVersion is the version being installed, read by running the staged
// build's OWN version verb inside the directory that was just verified.
//
// The bootstrap's rule, for the bootstrap's reason: execute only from the
// directory that was verified, never from the bundle on the Mac. What it buys
// is that the version reported is the one that build reports about itself, in
// the same vocabulary `status` and `doctor` use, rather than a string this
// command derived from the release it asked for.
func stagedVersion(program string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "version")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("the downloaded build did not answer its own version verb within %s", versionTimeout)
	}
	if err != nil {
		// Exit 2 is how a build older than the verb refuses an argument it has
		// never heard of. That is a version that is not known, and it is
		// reported as unknown rather than guessed.
		return "", fmt.Errorf("the downloaded build refused its own version verb (%v)", err)
	}
	m := versionLine.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", fmt.Errorf("the downloaded build answered its version verb with something this build cannot read")
	}
	return m[1], nil
}
