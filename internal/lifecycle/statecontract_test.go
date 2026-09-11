package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/gateway"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// status decodes the control plane's snapshot structurally rather than by
// importing the gateway's type, because nothing on the control plane's path may
// see this package and the dependency may not run the other way either. What
// that costs is a decode bound to nothing: a json tag renamed in the gateway
// would leave this package reading a field that no longer arrives, the suite
// green, and `gropius status` reporting no address and no models against a
// server that is answering.
//
// This is the test that pays that cost back. It is a TEST-ONLY import of the
// gateway — the closure in internal/archtest/lifecycle_boundary_test.go reads
// the package's own dependencies, which test files are not part of — so the
// contract is checked at build time here and nothing ships pointing the wrong
// way.
var statusReads = []string{
	"config.host",
	"config.port",
	"bind.mode",
	"bind.selected",
	"resident.repo_id",
	"resident.state",
}

// Every field status decodes is a field the control plane publishes, under the
// same name.
func TestEveryFieldStatusReadsIsOneTheControlPlanePublishes(t *testing.T) {
	published := reflect.TypeOf(gateway.State{})
	for _, path := range statusReads {
		if !jsonPathExists(published, path) {
			t.Errorf("status reads %q, which gateway.State does not carry under that name — the snapshot moved and the decode did not", path)
		}
	}
}

// And the other direction, so the list above cannot drift from the struct it
// describes: every field ServerState declares is one of the paths named, and
// every path named is a field ServerState declares.
func TestServerStateDeclaresExactlyWhatItSaysItReads(t *testing.T) {
	declared := jsonPaths(reflect.TypeOf(ServerState{}), "")
	want := map[string]bool{}
	for _, p := range statusReads {
		want[p] = true
	}
	for _, p := range declared {
		if !want[p] {
			t.Errorf("ServerState decodes %q, which is not in the list this file checks against the gateway — add it there so it is checked", p)
		}
		delete(want, p)
	}
	for p := range want {
		t.Errorf("this file says status reads %q and ServerState does not decode it", p)
	}
}

// The golden fixtures stand in for a running server, so the values in them have
// to be values a running server could produce. A residency state that is not
// one of the pool's own would make the contract fixture a fiction.
func TestTheGoldenResidencyStatesAreStatesThePoolReports(t *testing.T) {
	real := map[string]bool{
		string(runtime.ResidencyNotLoaded): true,
		string(runtime.ResidencyLoading):   true,
		string(runtime.ResidencyLoaded):    true,
	}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "status_") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var s Status
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		for _, m := range s.Resident {
			if !real[m.State] {
				t.Errorf("%s reports the model state %q, which the pool never reports (it says %q, %q or %q)",
					e.Name(), m.State, runtime.ResidencyNotLoaded, runtime.ResidencyLoading, runtime.ResidencyLoaded)
			}
		}
	}
}

// jsonPathExists walks a dotted JSON path through a struct's json tags,
// descending into pointers and slices, so "resident.repo_id" resolves. A field
// tagged "-" is not on the wire and does not count. It is the walker
// internal/ui/posture_test.go uses on the same snapshot, for the same reason.
func jsonPathExists(typ reflect.Type, path string) bool {
	for _, seg := range strings.Split(path, ".") {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return false
		}
		found := false
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == seg && name != "-" {
				typ = f.Type
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// jsonPaths is every dotted leaf path a struct decodes, in declaration order.
func jsonPaths(typ reflect.Type, prefix string) []string {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return []string{strings.TrimSuffix(prefix, ".")}
	}
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, jsonPaths(f.Type, prefix+name+".")...)
	}
	return out
}
