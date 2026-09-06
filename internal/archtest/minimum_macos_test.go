package archtest_test

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The macOS floor this product supports. Every surface that declares a minimum
// has to say the same thing, in the same words: a bundle that declares less
// lets an unsupported Mac install the app and discover the problem at runtime,
// instead of being refused at launch by Launch Services.
const (
	// minimumSystemVersion is the literal LSMinimumSystemVersion string. Keep
	// it a plain "26.0": the landing-page test compares the page's stated
	// requirement against this value verbatim.
	minimumSystemVersion = "26.0"
	// swiftDeploymentTarget is the same floor in swiftc's -target spelling.
	swiftDeploymentTarget = "26.0"
	// requirementSentence is how the user-facing docs say it, word for word.
	requirementSentence = "Requires macOS 26"
)

// The shipped app bundle must refuse a Mac the product does not support.
func TestAppBundleDeclaresTheSupportedMacOSFloor(t *testing.T) {
	root := repoRootDir(t)
	if got := plistString(t, filepath.Join(root, "build", "Info.plist"), "LSMinimumSystemVersion"); got != minimumSystemVersion {
		t.Errorf("build/Info.plist declares LSMinimumSystemVersion %q, want %q — "+
			"a lower minimum lets an unsupported Mac install the app and fail later "+
			"instead of being refused at launch", got, minimumSystemVersion)
	}
}

// The chat client ships as its own bundle, so it declares the floor twice: in
// its Info.plist and in the deployment target its build script compiles
// against. Both must agree with the server bundle.
func TestChatClientDeclaresTheSupportedMacOSFloor(t *testing.T) {
	root := repoRootDir(t)
	if got := plistString(t, filepath.Join(root, "client", "Info.plist"), "LSMinimumSystemVersion"); got != minimumSystemVersion {
		t.Errorf("client/Info.plist declares LSMinimumSystemVersion %q, want %q", got, minimumSystemVersion)
	}

	script := filepath.Join(root, "client", "build.sh")
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	targets := regexp.MustCompile(`-apple-macos([0-9][0-9.]*)`).FindAllStringSubmatch(string(raw), -1)
	if len(targets) == 0 {
		t.Fatal("client/build.sh names no -apple-macos deployment target; the floor it compiles against is now unchecked")
	}
	for _, m := range targets {
		if m[1] != swiftDeploymentTarget {
			t.Errorf("client/build.sh compiles against %s, want -apple-macos%s", m[0], swiftDeploymentTarget)
		}
	}
}

// The requirement is only useful to a reader if every page states it, and the
// landing page copies its wording from here, so the phrasing is fixed.
func TestUserFacingDocsStateTheRequirement(t *testing.T) {
	root := repoRootDir(t)
	for _, rel := range []string{"README.md", filepath.Join("docs", "getting-started.md")} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), requirementSentence) {
			t.Errorf("%s does not contain %q — every surface states the requirement in the same words", rel, requirementSentence)
		}
	}
}

// repoRootDir is the checkout root, two levels up from internal/archtest.
func repoRootDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// plistString returns the <string> value an XML property list declares for key.
// It walks the token stream rather than unmarshalling into a struct because a
// plist <dict> is a flat run of sibling <key>/<value> pairs — a shape the xml
// package cannot express as a struct. Only the standard library is used: the
// build assets must stay readable by the test suite with no extra dependency.
func plistString(t *testing.T, path, key string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec := xml.NewDecoder(f)
	var wanted bool
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var k string
			if err := dec.DecodeElement(&k, &start); err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			wanted = strings.TrimSpace(k) == key
		case "string":
			var v string
			if err := dec.DecodeElement(&v, &start); err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			if wanted {
				return strings.TrimSpace(v)
			}
		default:
			// Any other element ends the key's value, so a key whose value is
			// not a string can never pick up a later, unrelated <string>.
			wanted = false
		}
	}
	t.Fatalf("%s declares no %s", path, key)
	return ""
}
