package archtest_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// One per-model structure, held there by the type system rather than by prose.
//
// Gropius carried two per-model maps once — model_sampling and per_model —
// and a pinned list beside them, held to the same rules by comments in three
// files. Each had its own ceiling, its own sanitiser on the file path, its own
// guard in the settings handler and its own canonicalisation, and every one of
// them had to be remembered again for the next per-model setting. Unifying
// them cost a settings-file migration (iss-2609062213413447), and that is a
// price the operator pays, not the maintainer.
//
// This is what stops it being paid twice. A second map keyed by model id is
// how the split comes back, so the count is pinned at one: a new per-model
// setting is a field on config.ModelSettings, beside the ones already there.
func TestConfigHoldsExactlyOnePerModelMap(t *testing.T) {
	var found []string
	rt := reflect.TypeOf(config.Config{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		// A map from a model id to a settings struct. Keyed by string and
		// valued by a struct is the shape, not the field's name: a second one
		// called something else would split the rules exactly as badly.
		if f.Type.Kind() != reflect.Map ||
			f.Type.Key().Kind() != reflect.String ||
			f.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		found = append(found, f.Name+" "+f.Type.String())
	}
	if len(found) != 1 {
		t.Fatalf("config.Config holds %d per-model maps (%s); it holds exactly one, "+
			"and a new per-model setting is a field on config.ModelSettings inside it",
			len(found), strings.Join(found, ", "))
	}
	if found[0] != "Models map[string]config.ModelSettings" {
		t.Errorf("the one per-model map is %q; it is Models map[string]config.ModelSettings", found[0])
	}
}
