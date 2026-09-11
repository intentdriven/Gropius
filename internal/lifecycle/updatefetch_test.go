package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fetch and the verification, which are the only integrity control in the
// whole update path.
//
// The verification runs FOR REAL here — /usr/bin/shasum, on real files in a
// temporary directory — because that is the thing being relied on. The
// download goes through a fake release server on loopback, so no test reaches
// the network and every one of the four bad inputs can be served deliberately.

// fakeRelease is a release server: it serves the asset bytes a test gives it,
// under the names the real release publishes.
type fakeRelease struct {
	assets map[string][]byte
	server *httptest.Server
	asked  []string
}

func newFakeRelease(t *testing.T, assets map[string][]byte) *fakeRelease {
	t.Helper()
	r := &fakeRelease{assets: assets}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/")
		r.asked = append(r.asked, name)
		body, ok := r.assets[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.server.Close)
	return r
}

// fetch is the seam a test hands to the update environment in place of curl.
func (r *fakeRelease) fetch(name, dest string) error {
	resp, err := http.Get(r.server.URL + "/" + name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errStatus(resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	body := make([]byte, 0, 1<<16)
	buf := make([]byte, 1<<15)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	_, err = f.Write(body)
	return err
}

type errStatus string

func (e errStatus) Error() string { return string(e) }

// sums renders a checksums file the way the release workflow's shasum does.
func sums(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }

// digestOf is the digest of some bytes, spelled as a checksums line.
//
// Computed here rather than by anything in the package: the production side has
// no digest code at all, on purpose. The bootstrap and this verb must agree on
// what "verified" means, so there is ONE verification path — /usr/bin/shasum —
// rather than two that can drift, and a Go implementation beside it would be
// the second.
func digestOf(t *testing.T, name string, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) + "  " + name
}

// Every bad input fails closed, and the failure says which of them it was.
//
// The four the intent names, plus the checksums file that never arrived at all.
// A checksum check that stops checking is worse than none, because it still
// prints reassurance — so each row proves the verification REFUSED, and the
// good row proves the whole apparatus can still say yes.
func TestTheChecksumVerificationFailsClosedOnEveryBadInput(t *testing.T) {
	good := []byte("this is what the release workflow built")

	for _, tc := range []struct {
		name   string
		assets map[string][]byte
		// skipSums leaves the checksums file undownloaded entirely.
		skipSums  bool
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
			name: "an archive truncated in transit",
			assets: map[string][]byte{
				updateArchiveName: good[:10],
				checksumsName:     sums(digestOf(t, updateArchiveName, good)),
			},
			wantCause: causeMismatch,
		},
		{
			name: "a checksums file naming no downloaded file",
			assets: map[string][]byte{
				updateArchiveName: good,
				checksumsName:     sums(digestOf(t, "GropiusChat.app.zip", good)),
			},
			wantCause: causeNamesNothing,
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
			name: "an error page served where the checksums were asked for",
			assets: map[string][]byte{
				updateArchiveName: good,
				checksumsName:     []byte("<!DOCTYPE html>\n<html><body>404 Not Found</body></html>\n"),
			},
			wantCause: causeNotChecksums,
		},
		{
			name:      "no checksums file at all",
			assets:    map[string][]byte{updateArchiveName: good},
			skipSums:  true,
			wantCause: causeNoChecksums,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := newFakeRelease(t, tc.assets)
			dir := t.TempDir()

			if err := release.fetch(updateArchiveName, filepath.Join(dir, updateArchiveName)); err != nil {
				t.Fatalf("the fake release would not serve the archive: %v", err)
			}
			if !tc.skipSums {
				if err := release.fetch(checksumsName, filepath.Join(dir, checksumsName)); err != nil {
					t.Fatalf("the fake release would not serve the checksums: %v", err)
				}
			}

			err := verifyChecksums(dir)
			if err == nil {
				t.Fatal("the verification passed an input it must refuse")
			}
			if !strings.Contains(err.Error(), tc.wantCause) {
				t.Errorf("the failure does not name which cause fired:\ngot:  %v\nwant: %s", err, tc.wantCause)
			}
		})
	}

	// And the control: the same apparatus says yes to what the release
	// actually published, so the rows above are refusals rather than a
	// verification that never works.
	t.Run("what the release published verifies", func(t *testing.T) {
		release := newFakeRelease(t, map[string][]byte{
			updateArchiveName: good,
			checksumsName:     sums(digestOf(t, updateArchiveName, good), digestOf(t, "GropiusChat.app.zip", good)),
		})
		dir := t.TempDir()
		for _, name := range []string{updateArchiveName, checksumsName} {
			if err := release.fetch(name, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := verifyChecksums(dir); err != nil {
			t.Errorf("the published assets did not verify: %v", err)
		}
	})
}

// The origin is fixed in the binary. The bootstrap has a CI seam that points
// the download at a local directory; a verb a person types on a Mac has no CI
// case at all, so there is no variable and no flag, and the argument list is
// the proof.
func TestTheAssetOriginIsFixedInTheBinary(t *testing.T) {
	dest := filepath.Join(t.TempDir(), updateArchiveName)
	before := strings.Join(curlArgs(updateArchiveName, dest), " ")

	for _, name := range []string{
		"GROPIUS_ASSET_DIR", "GROPIUS_ROOT", "GITHUB_ACTIONS", "GROPIUS_RELEASE",
		"GROPIUS_UPDATE_URL", "http_proxy", "HTTPS_PROXY",
	} {
		t.Setenv(name, "https://somewhere-else.invalid/")
	}
	if after := strings.Join(curlArgs(updateArchiveName, dest), " "); after != before {
		t.Errorf("an environment variable changed where the update fetches from:\nbefore: %s\nafter:  %s", before, after)
	}
	if !strings.Contains(before, releaseAssetBase) {
		t.Errorf("the fetch does not name the release origin: %s", before)
	}
}

// And the transport is pinned the way the bootstrap pins it, because the
// bundle and the checksums that verify it come from the same origin: the
// transport is the thing to pin.
func TestTheFetchPinsTheTransportTheWayTheBootstrapDoes(t *testing.T) {
	args := curlArgs(checksumsName, "/tmp/somewhere")

	if args[0] != "/usr/bin/curl" {
		t.Errorf("the fetch runs %q rather than curl by absolute path", args[0])
	}
	// -q first, so a curlrc cannot re-point the connection while the URL
	// still reads github.com.
	if args[1] != "-q" {
		t.Errorf("-q is not the first argument (%v), so a curlrc is read before the flags that pin the transport", args)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"--proto =https", "--proto-redir =https", "-fsSL"} {
		if !strings.Contains(line, want) {
			t.Errorf("the fetch does not carry %q: %s", want, line)
		}
	}
	if !strings.HasSuffix(line, releaseAssetBase+checksumsName) {
		t.Errorf("the fetch does not end at the release asset: %s", line)
	}
}

// A bundle whose program is a symbolic link is refused before it is run, which
// is the one exec this whole path exists to reach. Only a compromised release
// can plant one — which is what the checksums above are for — and the refusal
// costs a line and does not depend on that being true.
func TestAStagedBundleWhoseProgramIsASymbolicLinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, bundleName)
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(bundle, binaryInBundle)
	if err := os.Symlink("/bin/sh", program); err != nil {
		t.Fatal(err)
	}

	err := checkStagedBundle(bundle)
	if err == nil {
		t.Fatal("a bundle carrying a symbolic link where its program belongs was accepted")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("the refusal does not say what it found: %v", err)
	}

	// A real program in the same place is accepted, so the refusal above is
	// about the link rather than about the path.
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkStagedBundle(bundle); err != nil {
		t.Errorf("an ordinary bundle was refused: %v", err)
	}
}

// The version installed is the one that BUILD reports about itself, read by
// running the staged program's own version verb inside the directory that was
// just verified — never the bundle already on the Mac, and never a guess.
func TestTheInstalledVersionIsReadFromTheStagedBuild(t *testing.T) {
	t.Run("a build that answers", func(t *testing.T) {
		program := writeFakeProgram(t, "#!/bin/sh\necho \"gropius 0.5.0\"\n")
		got, err := stagedVersion(program)
		if err != nil {
			t.Fatalf("stagedVersion: %v", err)
		}
		if got != "0.5.0" {
			t.Errorf("stagedVersion = %q, want the version the build reports", got)
		}
	})

	// A build older than the verb refuses an argument it has never heard of
	// with exit 2. That is a version that is unknown, and it is reported as
	// unknown rather than guessed from the release it came out of.
	t.Run("a build that refuses the verb", func(t *testing.T) {
		program := writeFakeProgram(t, "#!/bin/sh\necho 'gropius: unknown argument \"version\"' >&2\nexit 2\n")
		if _, err := stagedVersion(program); err == nil {
			t.Error("a build that refused the version verb was read as a version")
		}
	})
}

func writeFakeProgram(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gropius")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
