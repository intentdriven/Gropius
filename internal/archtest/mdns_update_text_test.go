package archtest_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// No Gropius code may call dnssd's ServiceHandle.UpdateText.
//
// It is the one call in that library that is not safe to make: it assigns
// Service.Text with no locking at all, while the responder goroutine reads that
// same field under a mutex private to the library. Nothing in this repository
// can hold that mutex, so the write and the read are unsynchronized whenever a
// peer is browsing at the moment the advertised hints change — a data race with
// no local fix (iss-12). internal/discovery therefore publishes a change by
// withdrawing the advertisement and registering it afresh, and its registration
// interface deliberately offers no way to edit a live one.
//
// The seam's own tests cannot see this: a fake registration proves what the
// refresh loop does with the interface it is given, not what the production
// implementation does with the library behind it. The library's README
// documents UpdateText as the ordinary way to do this, so the pull back towards
// it is real, and a source check is what keeps the decision from being undone
// by someone reading those docs rather than this one.
func TestNothingCallsDNSSDUpdateText(t *testing.T) {
	// internal/ and cmd/ are every directory that holds Go this repository
	// builds. Neither contains a vendored copy of the library — the module
	// cache does, and is not walked.
	roots := []string{"..", filepath.Join("..", "..", "cmd")}

	found := false
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			// This file names the call in order to look for it.
			if filepath.Base(path) == "mdns_update_text_test.go" {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(body), "\n") {
				code, _, _ := strings.Cut(line, "//")
				if !strings.Contains(code, "UpdateText(") {
					continue
				}
				found = true
				t.Errorf("%s:%s calls UpdateText: %s\n"+
					"\tdnssd writes Service.Text there with no lock while its responder reads it;\n"+
					"\tpublish a TXT change by withdrawing the advertisement and registering it afresh",
					filepath.ToSlash(path), strconv.Itoa(i+1), strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if found {
		t.Log("see internal/discovery/announcer.go for the interface that exists to make this unreachable")
	}
}
