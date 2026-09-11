package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
)

// The category is published the way every other Gropius extension to the models
// list is: top-level fields under the names the source already uses, absent
// when there is nothing to say. The words are HuggingFace's own.
func TestListModelsPublishesTheHubsCategory(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{{
		RepoID:      "org/chatty",
		State:       registry.StateReady,
		PipelineTag: "text-generation",
		Tags:        []string{"mlx", "conversational"},
	}}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})

	entries, _ := listModelsEntriesFrom(t, g.Handler(), "", "203.0.113.50:9999")
	if len(entries) != 1 {
		t.Fatalf("data = %+v, want exactly one model", entries)
	}
	entry := entries[0]
	if entry["pipeline_tag"] != "text-generation" {
		t.Errorf("pipeline_tag = %v, want text-generation", entry["pipeline_tag"])
	}
	tags, ok := entry["tags"].([]any)
	if !ok || len(tags) != 2 || tags[1] != "conversational" {
		t.Errorf("tags = %v, want the Hub's two", entry["tags"])
	}
	if entry["chat"] != true {
		t.Errorf("chat = %v, want true for a conversational text-generation model", entry["chat"])
	}
}

// A model the Hub did not tag is listed exactly as it is without a category:
// the two fields are absent rather than empty, and the flag is false, which is
// what the shipped rule says about a model nothing is known about.
func TestListModelsOmitsAnUnknownCategory(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{
		{RepoID: "org/quiet", State: registry.StateReady},
	}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})

	entries, body := listModelsEntriesFrom(t, g.Handler(), "", "203.0.113.50:9999")
	entry := entries[0]
	if entry["id"] != "org/quiet" {
		t.Fatalf("the model was not listed: %+v", entry)
	}
	for _, name := range []string{"pipeline_tag", "tags"} {
		if _, ok := entry[name]; ok {
			t.Errorf("%s = %v for a model with no category, want the field absent", name, entry[name])
		}
	}
	if strings.Contains(body, `"tags":[]`) {
		t.Errorf("an empty tag list reached the wire: %s", body)
	}
	if entry["chat"] != false {
		t.Errorf("chat = %v, want false for a model nothing is known about", entry["chat"])
	}
}

// The flag is the server's rule applied to the Hub's words — and the rule is
// the operator's, not a constant. A rule that tests neither half offers
// everything, which is what an operator who cleared both fields asked for.
func TestTheChatFlagFollowsTheServersRule(t *testing.T) {
	fake := mlxtest.Start(mlxtest.Options{ModelArg: "/m"})
	defer fake.Close()

	speech := registry.Model{
		RepoID:      "org/ears",
		State:       registry.StateReady,
		PipelineTag: "automatic-speech-recognition",
		Tags:        []string{"mlx"},
	}
	cases := []struct {
		name string
		rule config.ChatRule
		want bool
	}{
		{name: "the shipped rule says no", want: false},
		{
			name: "a rule that tests neither half says yes",
			rule: config.ChatRule{PipelineTags: []string{}, RequiredTags: []string{}},
			want: true,
		},
		{
			name: "and an operator can name the pipeline they want",
			rule: config.ChatRule{PipelineTags: []string{"automatic-speech-recognition"}, RequiredTags: []string{}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.ChatRule = c.rule
			models := &stubModels{models: []registry.Model{speech}}
			g := New(Options{Config: cfg, Pool: &stubPool{srv: fake}, Models: models})
			entries, _ := listModelsEntriesFrom(t, g.Handler(), "", "203.0.113.50:9999")
			if entries[0]["chat"] != c.want {
				t.Errorf("chat = %v, want %v", entries[0]["chat"], c.want)
			}
		})
	}
}

// The category is advisory and nothing else. A model the rule says cannot chat
// is still loaded and still answers a request that names it — that is the whole
// of what "not an API-side filter" means, and it is asserted in the same test
// as the flag so the two cannot drift apart.
func TestAModelOutsideTheChatRuleIsStillServed(t *testing.T) {
	const modelPath = "/models/org/ears"
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	defer fake.Close()

	models := &stubModels{models: []registry.Model{{
		RepoID:      "org/ears",
		Path:        modelPath,
		State:       registry.StateReady,
		PipelineTag: "automatic-speech-recognition",
	}}}
	g := New(Options{Config: config.Default(), Pool: &stubPool{srv: fake}, Models: models})
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()

	entry := firstModelEntry(t, srv)
	if entry["chat"] != false {
		t.Fatalf("chat = %v, want false — this test is about a model outside the rule", entry["chat"])
	}

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "org/ears",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := json.Marshal(entry)
		t.Fatalf("a model outside the chat rule was refused: status %d (entry %s)", resp.StatusCode, body)
	}
	if got := fake.LastModelField(); got != modelPath {
		t.Errorf("upstream saw model=%q, want the backend path %q", got, modelPath)
	}
}
