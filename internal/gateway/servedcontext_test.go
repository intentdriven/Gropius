package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
)

const servedModel = "mlx-community/Qwen3-8B-4bit"

// servedGateway is the ordinary test gateway with a declared window on its
// model, so the served window has a default to fall back to.
func servedGateway(t *testing.T, cfg config.Config, declared int64) (string, *stubPool) {
	t.Helper()
	const modelPath = "/models/" + servedModel
	fake := mlxtest.Start(mlxtest.Options{ModelArg: modelPath, Reply: "GROPIUS OK"})
	t.Cleanup(fake.Close)
	models := &stubModels{models: []registry.Model{{
		RepoID: servedModel, Path: modelPath, State: registry.StateReady,
		ContextLength: declared,
	}}}
	pool := &stubPool{srv: fake}
	g := New(Options{Config: cfg, Pool: pool, Models: models})
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return srv.URL, pool
}

// A prompt estimated to be larger than the window the model is served at is
// refused before anything is loaded, with a 400 in the shape an OpenAI client
// parses, naming the window and the estimate. Streaming makes no difference:
// the check runs before either path is chosen.
func TestAPromptOverTheServedContextIsRefused(t *testing.T) {
	cfg := config.Default()
	cfg.Models = map[string]config.ModelSettings{servedModel: {ServedContext: 1000}}
	srv, pool := servedGateway(t, cfg, 262144)

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			// Four bytes to the token, so 40,000 bytes of prompt is about
			// 10,000 tokens against a window of 1,000.
			body := fmt.Sprintf(`{"model":%q,"stream":%v,"messages":[{"role":"user","content":%q}]}`,
				servedModel, stream, strings.Repeat("a", 40000))
			resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			var out struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if out.Error.Type != "invalid_request_error" {
				t.Errorf("error.type = %q, want invalid_request_error", out.Error.Type)
			}
			for _, want := range []string{"1,000", "10,0"} {
				if !strings.Contains(out.Error.Message, want) {
					t.Errorf("message %q does not name %q (the window and the estimate)", out.Error.Message, want)
				}
			}
		})
	}
	// And nothing was loaded for a request that could never be served: a
	// refusal that acquires first is a refusal that can evict another model.
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.acquired) != 0 {
		t.Errorf("the pool was asked for %v; an over-long prompt must be refused before anything loads", pool.acquired)
	}
}

// max_tokens is generated into the same cache, so it counts against the same
// window: a prompt that fits on its own and not with its answer is refused.
func TestMaxTokensCountsAgainstTheServedContext(t *testing.T) {
	cfg := config.Default()
	cfg.Models = map[string]config.ModelSettings{servedModel: {ServedContext: 1000}}
	srv, _ := servedGateway(t, cfg, 262144)

	// About 500 tokens of prompt, which fits, plus 900 of answer, which does not.
	body := fmt.Sprintf(`{"model":%q,"max_tokens":900,"messages":[{"role":"user","content":%q}]}`,
		servedModel, strings.Repeat("a", 2000))
	resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: the prompt fits but the answer does not", resp.StatusCode)
	}
}

// With no setting the model is served at the window its configuration
// declares, and a prompt inside that window is served as it always was.
func TestWithNoSettingTheDeclaredWindowIsServed(t *testing.T) {
	srv, pool := servedGateway(t, config.Default(), 262144)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}]}`,
		servedModel, strings.Repeat("a", 40000))
	resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: 10,000 tokens is well inside a 262,144-token window", resp.StatusCode)
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.acquired) != 1 {
		t.Errorf("acquired = %v, want the one model", pool.acquired)
	}
}

// A model with no declared window and no setting has no window to enforce, so
// nothing is refused on its account. Nothing else refuses it either — such a
// prompt reaches mlx-lm unbounded, as every prompt did before this — which is
// why a window is what the memory budget charges.
func TestAModelWithNoWindowAtAllRefusesNothing(t *testing.T) {
	srv, _ := servedGateway(t, config.Default(), 0)
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}]}`,
		servedModel, strings.Repeat("a", 40000))
	resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// The listing carries the window Gropius will serve beside the one the model
// declares, so a client can size its prompts to what this Mac will accept
// rather than to what the model was built for.
func TestModelsListCarriesTheServedContext(t *testing.T) {
	cfg := config.Default()
	cfg.Models = map[string]config.ModelSettings{servedModel: {ServedContext: 32768}}
	srv, _ := servedGateway(t, cfg, 262144)

	resp, err := http.Get(srv + "/v1/models")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("data = %v, want one model", out.Data)
	}
	if got, want := out.Data[0]["served_context"], float64(32768); got != want {
		t.Errorf("served_context = %v, want %v", got, want)
	}
	if got, want := out.Data[0]["context_length"], float64(262144); got != want {
		t.Errorf("context_length = %v, want the declared window %v", got, want)
	}
}

// The size a request is measured at is arithmetic on figures the client
// supplies, so it must not wrap. A max_tokens of the largest integer there is
// used to make the sum negative, which passed the check and reached the pool.
func TestAnAbsurdMaxTokensDoesNotWrapPastTheWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Models = map[string]config.ModelSettings{servedModel: {ServedContext: 1000}}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		t.Run(field, func(t *testing.T) {
			srv, pool := servedGateway(t, cfg, 262144)
			body := fmt.Sprintf(`{"model":%q,%q:9223372036854775807,"messages":[{"role":"user","content":%q}]}`,
				servedModel, field, strings.Repeat("a", 40000))
			resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: the estimate wrapped negative", resp.StatusCode)
			}
			pool.mu.Lock()
			defer pool.mu.Unlock()
			if len(pool.acquired) != 0 {
				t.Errorf("the pool was asked for %v", pool.acquired)
			}
		})
	}
}

// JSON has one number type, so a client that sends 1e9 or 1000.0 is asking for
// exactly what 1000000000 and 1000 ask for. Read as an integer only, both were
// silently ignored and the request was served.
func TestAFloatMaxTokensCountsAgainstTheWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Models = map[string]config.ModelSettings{servedModel: {ServedContext: 1000}}
	for _, spelling := range []string{"1e9", "1000.0", "1e300"} {
		t.Run(spelling, func(t *testing.T) {
			srv, _ := servedGateway(t, cfg, 262144)
			// A short prompt: what refuses this request is the answer it asks for.
			body := fmt.Sprintf(`{"model":%q,"max_tokens":%s,"messages":[{"role":"user","content":"hi"}]}`,
				servedModel, spelling)
			resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s tokens of answer is past a 1,000-token window", resp.StatusCode, spelling)
			}
		})
	}
	// And a fractional figure inside the window is still served: the window is
	// what refuses a request, not the spelling of the number.
	srv, _ := servedGateway(t, cfg, 262144)
	body := fmt.Sprintf(`{"model":%q,"max_tokens":10.0,"messages":[{"role":"user","content":"hi"}]}`, servedModel)
	resp, err := http.Post(srv+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
