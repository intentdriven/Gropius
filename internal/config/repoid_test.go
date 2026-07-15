package config

import "testing"

func TestValidRepoID(t *testing.T) {
	valid := []string{
		"mlx-community/Qwen3-8B-4bit",
		"mlx-community/Llama-3.3-70B-Instruct-8bit",
		"org_name/model.v2",
		"a/b",
	}
	for _, s := range valid {
		if !ValidRepoID(s) {
			t.Errorf("ValidRepoID(%q) = false, want true", s)
		}
	}

	// Each of these, joined onto the models dir, could escape it or is malformed.
	invalid := []string{
		"",
		"noslash",
		"../etc",
		"a/../../..",
		"../../../../etc/passwd",
		"a/b/c", // too many components
		"org/",  // empty name
		"/name", // empty org
		".././x",
		"org/..",
		"org/.",
		"..",
		"a/b\x00c",  // NUL
		"org/na me", // space
		"org/na/me",
	}
	for _, s := range invalid {
		if ValidRepoID(s) {
			t.Errorf("ValidRepoID(%q) = true, want false — this could escape the models dir", s)
		}
	}
}

// The concrete exploit: a bad id must never produce a path outside Models.
func TestModelDirCannotEscapeWithValidatedID(t *testing.T) {
	p := NewPaths("/data")
	// The validator is the gate; confirm that everything it accepts stays inside.
	for _, id := range []string{"org/model", "mlx-community/Qwen3-8B-4bit"} {
		if !ValidRepoID(id) {
			t.Fatalf("test precondition: %q should be valid", id)
		}
		dir := p.ModelDir(id)
		if dir[:len(p.Models)] != p.Models {
			t.Errorf("ModelDir(%q) = %q escaped %q", id, dir, p.Models)
		}
	}
}
