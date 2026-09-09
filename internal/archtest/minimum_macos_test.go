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

// build/Info.plist's LSMinimumSystemVersion is this product's ONE declaration
// of the macOS floor it supports. Every other surface that names a minimum --
// the chat client's bundle, the deployment targets its build script compiles
// against, the installer's refusal, and the requirement sentence in the three
// READMEs and the guide -- is checked against that one value here, so a second
// copy of the number (install.sh's MIN_MACOS_MAJOR, say) cannot drift from it
// unnoticed. Prose that names the version some other way -- the README's
// tested-on line, the client README's SDK requirement -- is not read here and
// still needs a human edit when the floor moves. Drift is the failure this
// guard exists to prevent:
//
//   - a bundle minimum below the floor lets an unsupported Mac install the app
//     and discover the problem at runtime, instead of being refused at launch;
//   - a deployment target above the bundle minimum is worse, because then
//     Launch Services admits a supported Mac and dyld kills the app at exec;
//   - a page stating a different number sends the reader to the wrong Mac.
//
// So raising the floor is one edit to build/Info.plist plus whatever this test
// then reports as out of step. Keep the value a plain "26.0"-style string:
// other tests read it from here as the source of truth.
const (
	minimumSystemVersionKey = "LSMinimumSystemVersion"
	// installerFloorAssignment names the shell variable install.sh gates on, so
	// the installer keeps the major in exactly one place too.
	installerFloorAssignment = "MIN_MACOS_MAJOR"
)

// TestEverySurfaceDeclaresTheSameMacOSFloor holds every declared minimum to the
// one in build/Info.plist.
func TestEverySurfaceDeclaresTheSameMacOSFloor(t *testing.T) {
	root := repoRootDir(t)

	floor := plistString(t, filepath.Join(root, "build", "Info.plist"), minimumSystemVersionKey)
	major, _, found := strings.Cut(floor, ".")
	if !found || major == "" {
		t.Fatalf("build/Info.plist declares %s %q, which is not a major.minor version", minimumSystemVersionKey, floor)
	}

	t.Run("chat client bundle", func(t *testing.T) {
		// The client ships as its own bundle, so it declares the floor twice:
		// here, and in the deployment target below.
		if got := plistString(t, filepath.Join(root, "client", "Info.plist"), minimumSystemVersionKey); got != floor {
			t.Errorf("client/Info.plist declares %s %q; build/Info.plist declares %q",
				minimumSystemVersionKey, got, floor)
		}
	})

	t.Run("chat client deployment targets", func(t *testing.T) {
		raw := readRepoFile(t, root, filepath.Join("client", "build.sh"))
		targets := regexp.MustCompile(`-apple-macos([0-9][0-9.]*)`).FindAllStringSubmatch(raw, -1)
		if len(targets) == 0 {
			t.Fatal("client/build.sh names no -apple-macos deployment target; the floor it compiles against is unchecked")
		}
		for _, m := range targets {
			// A target above the bundle minimum is the dangerous direction: the
			// bundle admits the Mac and the binary then refuses to start on it.
			if m[1] != major+".0" {
				t.Errorf("client/build.sh compiles against %s; build/Info.plist declares %q", m[0], floor)
			}
		}
	})

	t.Run("installer refusal", func(t *testing.T) {
		raw := readRepoFile(t, root, "install.sh")
		gates := regexp.MustCompile(installerFloorAssignment+`=([0-9]+)`).FindAllStringSubmatch(raw, -1)
		if len(gates) == 0 {
			t.Fatalf("install.sh sets no %s; an unsupported Mac is downloaded to and given a firewall rule "+
				"before Launch Services refuses the app", installerFloorAssignment)
		}
		for _, m := range gates {
			if m[1] != major {
				t.Errorf("install.sh gates on %s; build/Info.plist declares %q", m[0], floor)
			}
		}
	})

	t.Run("user-facing prose", func(t *testing.T) {
		// Every page states the requirement in the same words, so a reader who
		// meets it twice meets one number. \b keeps "macOS 26" from being
		// satisfied by "macOS 265".
		phrase := "Requires macOS " + major
		stated := regexp.MustCompile(regexp.QuoteMeta(phrase) + `\b`)
		for _, rel := range []string{
			"README.md",
			filepath.Join("docs", "getting-started.md"),
			filepath.Join("client", "README.md"),
		} {
			if !stated.MatchString(readRepoFile(t, root, rel)) {
				t.Errorf("%s does not state %q; build/Info.plist declares %q", rel, phrase, floor)
			}
		}
	})
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

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// plistString returns the <string> value an XML property list declares for key
// at the top level of its bundle dictionary. It walks the token stream rather
// than unmarshalling into a struct because a plist <dict> is a flat run of
// sibling <key>/<value> pairs -- a shape the xml package cannot express as a
// struct. Only the standard library is used: reading the build assets must not
// cost the test suite a dependency.
func plistString(t *testing.T, path, key string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec := xml.NewDecoder(f)
	var (
		// depth 0 is outside <plist>, 1 inside it, 2 inside the bundle <dict>.
		depth  int
		wanted bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			// Only the bundle dictionary's own keys count. A key of the same
			// name nested inside a value (client/Info.plist's
			// NSAppTransportSecurity dict, say) must not satisfy the lookup
			// while the top-level key is missing.
			if depth == 2 && (elem.Name.Local == "key" || elem.Name.Local == "string") {
				var v string
				if err := dec.DecodeElement(&v, &elem); err != nil {
					t.Fatalf("parsing %s: %v", path, err)
				}
				// DecodeElement consumed the closing tag, so depth is unchanged.
				if elem.Name.Local == "key" {
					wanted = strings.TrimSpace(v) == key
				} else if wanted {
					return strings.TrimSpace(v)
				}
				continue
			}
			if depth == 2 {
				// Any other element is this key's value, so a key whose value
				// is not a string can never pick up a later, unrelated one.
				wanted = false
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	t.Fatalf("%s declares no top-level %s", path, key)
	return ""
}

// plistStringArray returns the <array> of <string> values an XML property list
// declares for key at the top level of its bundle dictionary. It is the array
// sibling of plistString and walks the token stream the same way, for the same
// reason: a plist <dict> is a flat run of sibling <key>/<value> pairs, not a
// shape the xml package can unmarshal into a struct.
func plistStringArray(t *testing.T, path, key string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec := xml.NewDecoder(f)
	var (
		// depth 0 is outside <plist>, 1 inside it, 2 inside the bundle <dict>.
		depth  int
		wanted bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			// As in plistString, only the bundle dictionary's own keys count,
			// so a key of the same name nested inside some other value cannot
			// satisfy the lookup while the top-level key is missing.
			if depth == 2 && elem.Name.Local == "key" {
				var v string
				if err := dec.DecodeElement(&v, &elem); err != nil {
					t.Fatalf("parsing %s: %v", path, err)
				}
				wanted = strings.TrimSpace(v) == key
				continue
			}
			if depth == 2 && elem.Name.Local == "array" && wanted {
				var arr struct {
					Values []string `xml:"string"`
				}
				if err := dec.DecodeElement(&arr, &elem); err != nil {
					t.Fatalf("parsing %s: %v", path, err)
				}
				for i, v := range arr.Values {
					arr.Values[i] = strings.TrimSpace(v)
				}
				return arr.Values
			}
			if depth == 2 {
				// Any other element is the current key's value, so a key whose
				// value is not an array can never pick up a later, unrelated one.
				wanted = false
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	t.Fatalf("%s declares no top-level %s array", path, key)
	return nil
}
