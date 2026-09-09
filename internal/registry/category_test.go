package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The category a download recorded survives a restart: it is HuggingFace's own
// pipeline tag and tags, fetched once when the model arrived, and nothing on
// disk can tell us again.
func TestCategoryRoundTripsThroughTheIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put(Model{
		RepoID:      "mlx-community/Qwen3-8B-4bit",
		Path:        filepath.Join(dir, "m"),
		State:       StateReady,
		PipelineTag: "text-generation",
		Tags:        []string{"mlx", "conversational"},
	}); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := again.Get("mlx-community/Qwen3-8B-4bit")
	if err != nil {
		t.Fatal(err)
	}
	if m.PipelineTag != "text-generation" {
		t.Errorf("pipeline tag = %q, want text-generation", m.PipelineTag)
	}
	if strings.Join(m.Tags, ",") != "mlx,conversational" {
		t.Errorf("tags = %v, want [mlx conversational]", m.Tags)
	}
}

// A model the Hub did not tag carries no category, and the index says nothing
// about it rather than saying it is empty: an absent field is what every client
// reads as "not known".
func TestAnUntaggedModelCarriesNoCategory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put(Model{RepoID: "org/quiet", Path: dir, State: StateReady}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pipeline_tag", "tags"} {
		if strings.Contains(string(b), name) {
			t.Errorf("the index carries %q for a model with no category: %s", name, b)
		}
	}
}

// registry.json lives in a group-writable directory in shared-cache mode, and
// what it says about a model is republished to the LAN. A planted or corrupt
// category is bounded on the way in and on the way back out, the way the
// context length is.
func TestTheCategoryIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	long := strings.Repeat("x", MaxTagBytes+1)
	many := make([]string, MaxTags+10)
	for i := range many {
		many[i] = fmt.Sprintf("tag-%d", i)
	}
	planted := []Model{{
		RepoID:      "org/planted",
		Path:        dir,
		State:       StateReady,
		PipelineTag: long,
		Tags:        append([]string{"mlx", long, "with\x00nul", "   ", "mlx"}, many...),
	}}
	b, err := json.Marshal(planted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/planted")
	if err != nil {
		t.Fatal(err)
	}
	if m.PipelineTag != "" {
		t.Errorf("pipeline tag = %q, want it dropped: it is longer than the bound", m.PipelineTag)
	}
	if len(m.Tags) != MaxTags {
		t.Errorf("tags = %d entries, want the %d the bound allows", len(m.Tags), MaxTags)
	}
	for _, tag := range m.Tags {
		if len(tag) > MaxTagBytes || strings.ContainsRune(tag, 0) || strings.TrimSpace(tag) == "" {
			t.Errorf("an unusable tag %q survived the bound", tag)
		}
	}
	if m.Tags[0] != "mlx" {
		t.Errorf("tags = %v, want the usable ones kept in order", m.Tags)
	}

	// And the same bound on the write path, which is where a download's
	// metadata arrives.
	if err := r.Put(Model{RepoID: "org/put", Path: dir, State: StateReady, PipelineTag: "with\x00nul"}); err != nil {
		t.Fatal(err)
	}
	put, err := r.Get("org/put")
	if err != nil {
		t.Fatal(err)
	}
	if put.PipelineTag != "" {
		t.Errorf("pipeline tag = %q on the write path, want it dropped", put.PipelineTag)
	}
}

// A rescan re-derives from the directory everything the directory can tell it.
// The category is not on the disk — it is the Hub's word, fetched when the
// model was downloaded — so a rescan must leave it exactly as it found it,
// rather than clearing it at every start-up.
func TestRescanKeepsAStoredCategory(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models")
	modelDir := filepath.Join(models, "org", "tagged")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeReadyModelDir(t, modelDir)

	r, err := Open(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put(Model{
		RepoID:      "org/tagged",
		Path:        modelDir,
		State:       StateReady,
		PipelineTag: "text-generation",
		Tags:        []string{"conversational"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Rescan(models); err != nil {
		t.Fatal(err)
	}
	m, err := r.Get("org/tagged")
	if err != nil {
		t.Fatal(err)
	}
	if m.PipelineTag != "text-generation" || strings.Join(m.Tags, ",") != "conversational" {
		t.Errorf("after a rescan the category is %q/%v, want it untouched", m.PipelineTag, m.Tags)
	}
}

// writeReadyModelDir lays down the minimum a rescan accepts as a ready model.
func writeReadyModelDir(t *testing.T, dir string) {
	t.Helper()
	cfg := `{"model_type":"qwen3","max_position_embeddings":40960}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
}
