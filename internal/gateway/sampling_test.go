package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/runtime"
)

func f64(v float64) *float64 { return &v }
func intp(v int) *int        { return &v }

// The default lives in the model server process, not in the request. The
// completion path must stay a relay: a configured default adds no key to what
// the client sent, so nothing about the body depends on settings.
func TestConfiguredDefaultAddsNothingToTheRelayedBody(t *testing.T) {
	cfg := config.Default()
	cfg.Sampling = config.Sampling{Temperature: f64(0.7), MaxTokens: intp(8192)}
	srv, _, fake := newTestGateway(t, cfg)

	resp := post(t, srv, "/v1/chat/completions", map[string]any{
		"model":    "mlx-community/Qwen3-8B-4bit",
		"messages": []any{map[string]string{"role": "user", "content": "hi"}},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body := fake.LastBody()
	for _, key := range []string{"temperature", "top_p", "top_k", "min_p", "max_tokens"} {
		if _, ok := body[key]; ok {
			t.Errorf("the gateway injected %q into the relayed body — the default belongs to the model server process, "+
				"so a request that omits a parameter must reach the server omitting it", key)
		}
	}
}

// What a request carries always wins, and nothing else about it changes.
func TestClientSamplingValuesReachTheModelServerUnchanged(t *testing.T) {
	cfg := config.Default()
	cfg.Sampling = config.Sampling{Temperature: f64(0.7)}
	srv, _, fake := newTestGateway(t, cfg)

	sent := map[string]any{
		"model":       "mlx-community/Qwen3-8B-4bit",
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"temperature": 0.3,
		"top_p":       0.8,
		"max_tokens":  64.0,
		"stream":      false,
	}
	resp := post(t, srv, "/v1/chat/completions", sent, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	got := fake.LastBody()
	if got["temperature"] != 0.3 {
		t.Errorf("temperature = %v, want the client's 0.3 — a request's own value wins over the default", got["temperature"])
	}
	// The model field is deliberately rewritten (it is a load instruction to
	// mlx-lm); every other field must arrive with the value the client sent.
	// Byte identity is not the bar: the handler re-marshals the body, which
	// reorders keys and escapes output.
	for key, want := range sent {
		if key == "model" {
			continue
		}
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
}

// A default that is only in the file is a default nobody can change: the
// settings endpoint has to accept the whole set.
func TestSettingsSavesSamplingDefaults(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	resp := postJSON(t, srv, "/api/settings", `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":0.7,"top_p":0.95,"top_k":40,"min_p":0.05,"max_tokens":8192}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got := a.Config().Sampling
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7", got.Temperature)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 8192 {
		t.Errorf("max_tokens = %v, want 8192", got.MaxTokens)
	}

	// A blank field is sent as null and must land as unset, not as zero.
	resp2 := postJSON(t, srv, "/api/settings", `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":null,"top_p":null,"top_k":null,"min_p":null,"max_tokens":null}}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	if s := a.Config().Sampling; !s.IsZero() {
		t.Errorf("sampling = %+v, want every field unset after a blank save", s)
	}
}

// A value the model server would reject must never reach the running or the
// stored configuration: it would answer 400 to every request that omits the
// parameter, on every model, from one save.
func TestOutOfRangeSamplingIsRefusedAndChangesNothing(t *testing.T) {
	srv, a := newTestControlApp(t, config.Default())

	ok := postJSON(t, srv, "/api/settings", `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":0.7}}`)
	ok.Body.Close()

	// Clone: a shallow copy would share the pointee the handler could write
	// through, and the comparison below would then pass while the live
	// configuration had already been changed.
	before := a.Config().Clone()
	fileBefore, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv, "/api/settings", `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":-3}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBody.Error.Message, "temperature") {
		t.Errorf("error = %q, want it to name the offending field", errBody.Error.Message)
	}

	if !reflect.DeepEqual(before, a.Config()) {
		t.Errorf("the running configuration changed on a refused save:\nbefore %+v\nafter  %+v",
			*before.Sampling.Temperature, *a.Config().Sampling.Temperature)
	}
	fileAfter, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(fileBefore) != string(fileAfter) {
		t.Errorf("config.json was rewritten by a refused save")
	}
}

// Saving a default does not reach a model that is already loaded — the value
// is a launch flag. The panel has to say which models that leaves stale, or
// the change looks as though it did nothing.
func TestSamplingReloadsNamesOnlyTheModelsWhoseValueMoved(t *testing.T) {
	before := config.Default()
	before.Sampling = config.Sampling{Temperature: f64(0.7)}
	before.ModelSampling = map[string]config.Sampling{
		"org/pinned": {Temperature: f64(0.1)},
	}
	after := before.Clone()
	after.Sampling = config.Sampling{Temperature: f64(1.2)}

	resident := []runtime.Resident{
		{RepoID: "org/plain"},
		{RepoID: "org/pinned"},
	}

	got := samplingReloads(before, after, resident)
	want := []string{"org/plain"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reload_models = %v, want %v — the overridden model's effective value did not move", got, want)
	}

	if got := samplingReloads(before, before.Clone(), resident); len(got) != 0 {
		t.Errorf("reload_models = %v, want empty when nothing changed", got)
	}
}

func TestSettingsResponseCarriesReloadModels(t *testing.T) {
	srv, _ := newTestControlApp(t, config.Default())

	resp := postJSON(t, srv, "/api/settings", `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":0.7}}`)
	defer resp.Body.Close()

	var body struct {
		ReloadModels []string `json:"reload_models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ReloadModels == nil {
		t.Error("the settings response carries no reload_models list, so the panel cannot say which models are stale")
	}
}
