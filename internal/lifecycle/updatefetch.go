package lifecycle

import (
	"context"
	"fmt"
	"io"
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
	// causeArchiveNotCovered is the one that used to pass.
	causeArchiveNotCovered = "the checksums file carries no line for " + updateArchiveName
)

// maxChecksumsBytes caps what is read back from the checksums file. The real
// one is a few hundred bytes; this read happens only after shasum has already
// refused, and what it is looking at came off the network, so a file that is
// not what it should be must not be read whole into memory to be described.
const maxChecksumsBytes = 1 << 20

// readCapped reads at most limit bytes of a file.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

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
	// The program comes from the same value the argument test asserts on, so
	// that test is about the process this actually starts rather than about a
	// second literal beside it. The absolute path is spelled once, in curlArgs,
	// where the pinning scans read it.
	args := curlArgs(name, dest)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
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
	body, err := readCapped(sums, maxChecksumsBytes)
	if err != nil {
		return fmt.Errorf("%s: the release did not serve one, or it could not be written", causeNoChecksums)
	}

	// THE SCOPE, decided here and not by shasum.
	//
	// `shasum -c` answers for the files the checksums file NAMES. With
	// --ignore-missing, names that are absent are skipped and names that are
	// present and irrelevant are verified and reported as a pass — so a
	// checksums file naming any readable file with known content (a system
	// file, /dev/null) exited 0 with the archive never looked at, and the
	// unverified download went on to be unpacked, executed to read its version,
	// and installed. The one integrity control in the path had no assertion
	// that the artefact it protects was in scope.
	//
	// So the file is narrowed to the line for THIS archive before shasum sees
	// it, and --ignore-missing goes with the narrowing: there is then exactly
	// one name, it is the file just downloaded, and a pass is a statement about
	// that file. Narrowing the input is not making the verdict — the digest is
	// still computed and compared by shasum, which stays the only thing in this
	// product that decides whether bytes match.
	line, ok := checksumLineFor(string(body), updateArchiveName)
	if !ok {
		return fmt.Errorf("%s (%s)", checksumScopeCause(string(body)), flattenOutput(string(body)))
	}
	scoped := filepath.Join(dir, scopedChecksumsName)
	if err := os.WriteFile(scoped, []byte(line+"\n"), 0o600); err != nil {
		return fmt.Errorf("the checksums could not be prepared for verification: %w", err)
	}
	defer os.Remove(scoped)

	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/shasum", "-a", "256", "-c", scopedChecksumsName)
	cmd.Dir = dir
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("/usr/bin/shasum did not answer within %s", verifyTimeout)
	}
	if err != nil {
		return fmt.Errorf("%s (%s)", checksumFailureCause(string(out)), flattenOutput(string(out)))
	}
	// And the pass is read as a pass for THAT file, by its own line, rather
	// than by an exit code that means "nothing I was told about was wrong".
	if !hasSuccessLine(string(out), updateArchiveName) {
		return fmt.Errorf("%s (%s)", causeArchiveNotCovered, flattenOutput(string(out)))
	}
	return nil
}

// scopedChecksumsName is the one-line file shasum is actually pointed at. It
// sits in the same staging directory, which nothing else can write.
const scopedChecksumsName = ".gropius-update-checksum"

// checksumLineFor finds the checksums line for exactly one file name.
//
// A line is `<digest><separator><name>`, where the separator is two spaces for
// a text-mode digest and " *" for a binary one. The name must be exactly the
// archive: a line naming a PATH is refused rather than matched, because
// "/somewhere/Gropius.app.zip" would otherwise verify a file this command never
// downloaded — and would make shasum print a success line that CONTAINS the
// archive's own.
func checksumLineFor(body, name string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		digest, rest, ok := strings.Cut(line, " ")
		if !ok || !isHexDigest(digest) {
			continue
		}
		if strings.TrimPrefix(strings.TrimPrefix(rest, " "), "*") == name {
			return line, true
		}
	}
	return "", false
}

// hasSuccessLine reports whether shasum said this exact file was OK. Compared
// line by line rather than as a substring: a checksums file naming
// "/somewhere/Gropius.app.zip" produces a line ending in the same eighteen
// characters.
func hasSuccessLine(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name+": OK" {
			return true
		}
	}
	return false
}

func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// checksumScopeCause says why no line for the archive was found: an empty file,
// something that is not a checksums file at all, or a real checksums file that
// simply does not cover what was downloaded.
func checksumScopeCause(body string) string {
	switch {
	case strings.TrimSpace(body) == "":
		return causeEmptyChecksums
	case !hasAnyChecksumLine(body):
		return causeNotChecksums
	default:
		return causeArchiveNotCovered
	}
}

func hasAnyChecksumLine(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if digest, _, ok := strings.Cut(strings.TrimRight(line, "\r"), " "); ok && isHexDigest(digest) {
			return true
		}
	}
	return false
}

// checksumFailureCause names which of the causes fired, from what shasum said.
//
// By the time this is reached the file it was pointed at holds exactly one
// line, for exactly the archive, so the shapes it can report are narrow: the
// digests differ, or the file it names is not readable.
func checksumFailureCause(output string) string {
	switch {
	// Checked first: a mismatch ALSO reports that no file was verified, so
	// reading that line first would report every corrupt download as a
	// checksums file naming nothing.
	case strings.Contains(output, ": FAILED"), strings.Contains(output, "did NOT match"):
		return causeMismatch
	case strings.Contains(output, "no file was verified"), strings.Contains(output, "No such file"):
		return causeNamesNothing
	default:
		return "the download could not be verified against the checksums published with the release"
	}
}

// flattenOutput folds a tool's several lines into one, so a failure is one
// sentence rather than a block with a message wrapped around it.
//
// It is NOT redaction, and it is named so it cannot be read as any. What the
// account name is stripped from is the whole report, once, where it is
// assembled — the rule doctor's own redaction sets, for the reason it set it.
func flattenOutput(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
// Opened through an os.Root on the bundle, so EVERY component is checked and
// not only the last one. os.Lstat refuses to follow the final component and
// follows every one above it, so a bundle carrying `Contents` as a symbolic
// link would resolve through it and pass — sending the exec exactly where this
// function says it does not. os.Root is the defence internal/config already
// uses against the same shape of mistake.
func checkStagedBundle(bundle string) error {
	root, err := os.OpenRoot(bundle)
	if err != nil {
		return fmt.Errorf("the downloaded bundle could not be read (%w)", err)
	}
	defer root.Close()

	fi, err := root.Lstat(binaryInBundle)
	if err != nil {
		// os.Root answers this way for a component that is a symbolic link as
		// well as for one that is absent, so the two are reported together:
		// either way there is no program at that path inside this bundle.
		return fmt.Errorf("the downloaded bundle carries no %s that stays inside it "+
			"(a missing program, or a symbolic link on the way to it)", binaryInBundle)
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
