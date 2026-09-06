package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// recordingLauncher stands in for the real one: it answers on the port the
// pool allocated, so a model genuinely becomes resident, and it keeps the Spec
// each model was launched with — which is the only place the sampling defaults
// a user saved can be observed as the model server would receive them.
type recordingLauncher struct {
	mu    sync.Mutex
	specs map[string]runtime.Spec
	count int
}

type fakeProcess struct {
	srv  *mlxtest.Server
	done chan struct{}
	once sync.Once
}

func (p *fakeProcess) Stop(context.Context) error {
	p.once.Do(func() { p.srv.Close(); close(p.done) })
	return nil
}
func (p *fakeProcess) Done() <-chan struct{} { return p.done }
func (p *fakeProcess) Err() error            { return nil }
func (p *fakeProcess) Pid() int              { return 4242 }

func (l *recordingLauncher) Precheck(runtime.Spec) error { return nil }

func (l *recordingLauncher) Launch(_ context.Context, spec runtime.Spec) (runtime.Process, error) {
	srv := mlxtest.Start(mlxtest.Options{ModelArg: spec.ModelPath, Port: spec.Port})
	l.mu.Lock()
	l.specs[spec.RepoID] = spec
	l.count++
	l.mu.Unlock()
	return &fakeProcess{srv: srv, done: make(chan struct{})}, nil
}

func (l *recordingLauncher) specFor(repoID string) runtime.Spec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.specs[repoID]
}

func (l *recordingLauncher) launches() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

// newWiredControl builds the whole composition root — App, pool, control plane
// — over a launcher that records what it was asked to start, with the named
// models already downloaded.
func newWiredControl(t *testing.T, cfg config.Config, models ...string) (*app.App, *recordingLauncher, http.Handler) {
	t.Helper()

	paths := config.NewPaths(t.TempDir())
	l := &recordingLauncher{specs: map[string]runtime.Spec{}}
	a, err := app.New(app.Options{Paths: paths, Config: cfg, Launcher: l})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	for _, id := range models {
		dir := paths.ModelDir(id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "weights.safetensors"), []byte("w"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := a.Registry.Put(registry.Model{
			RepoID: id, Path: dir, State: registry.StateReady, Bytes: 1 << 20,
		}); err != nil {
			t.Fatal(err)
		}
	}

	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	return a, l, mux
}

// serve exposes a mux over loopback for the duration of the test.
func serve(t *testing.T, mux http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func loadModel(t *testing.T, a *app.App, repoID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, release, err := a.Pool.Acquire(ctx, repoID)
	if err != nil {
		t.Fatalf("Acquire(%s): %v", repoID, err)
	}
	release()
}

// The chain from a saved setting to a model server's command line runs through
// the composition root, and every link either side of it was tested in
// isolation. This spans it: delete the SamplingFor closure in app.New and the
// feature is inert on every machine, so this must be what turns red.
func TestSavedSamplingReachesTheLaunchedProcess(t *testing.T) {
	a, l, mux := newWiredControl(t, config.Default(), "org/plain", "org/special")
	srv := serve(t, mux)

	resp := postJSON(t, srv, "/api/settings", `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":0.7,"max_tokens":8192},
		"model_sampling":{"org/special":{"temperature":0.1}}}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	loadModel(t, a, "org/plain")
	loadModel(t, a, "org/special")

	plain := l.specFor("org/plain").Sampling
	if plain.Temperature == nil || *plain.Temperature != 0.7 {
		t.Errorf("org/plain was launched with temperature %v, want the saved 0.7 — "+
			"nothing carries a saved setting to a model server's command line", plain.Temperature)
	}
	if plain.MaxTokens == nil || *plain.MaxTokens != 8192 {
		t.Errorf("org/plain was launched with max_tokens %v, want the saved 8192", plain.MaxTokens)
	}
	special := l.specFor("org/special").Sampling
	if special.Temperature == nil || *special.Temperature != 0.1 {
		t.Errorf("org/special was launched with temperature %v, want its override 0.1", special.Temperature)
	}
	if special.MaxTokens == nil || *special.MaxTokens != 8192 {
		t.Errorf("org/special was launched with max_tokens %v, want the machine-wide 8192 under a partial override",
			special.MaxTokens)
	}
}

// The criterion is about what the panel tells Alice, so it is asserted where
// Alice sees it: the response to her save, with models actually resident.
func TestSettingsNamesExactlyTheResidentModelsThatMustLoadAgain(t *testing.T) {
	cfg := config.Default()
	cfg.Sampling = config.Sampling{Temperature: f64(0.7)}
	cfg.ModelSampling = map[string]config.Sampling{"org/pinned": {Temperature: f64(0.1)}}
	a, l, mux := newWiredControl(t, cfg, "org/plain", "org/pinned")
	srv := serve(t, mux)

	loadModel(t, a, "org/plain")
	loadModel(t, a, "org/pinned")
	launched := l.launches()

	// Move the machine-wide default. org/pinned's own override shadows it, so
	// nothing about how that model is served changes.
	resp := postJSON(t, srv, "/api/settings", `{"host":"0.0.0.0","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":1.2},
		"model_sampling":{"org/pinned":{"temperature":0.1}}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		ReloadModels []string `json:"reload_models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if want := []string{"org/plain"}; !reflect.DeepEqual(body.ReloadModels, want) {
		t.Errorf("reload_models = %v, want %v", body.ReloadModels, want)
	}

	// Saving must not restart anything: the point of naming the models is that
	// they are still running with the values they started with.
	if l.launches() != launched {
		t.Errorf("saving settings launched %d more processes; a resident model must keep serving with what it started with",
			l.launches()-launched)
	}
	if got := l.specFor("org/plain").Sampling.Temperature; got == nil || *got != 0.7 {
		t.Errorf("the resident model's launch arguments changed under it: %v", got)
	}

	// A save that changes nothing about sampling names nobody.
	resp2 := postJSON(t, srv, "/api/settings", `{"host":"0.0.0.0","port":11535,"decode_concurrency":4,
		"sampling":{"temperature":1.2},
		"model_sampling":{"org/pinned":{"temperature":0.1}}}`)
	defer resp2.Body.Close()
	var body2 struct {
		ReloadModels []string `json:"reload_models"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	if len(body2.ReloadModels) != 0 {
		t.Errorf("reload_models = %v, want empty when nothing moved", body2.ReloadModels)
	}
}

// Pinning protects a model against other clients' requests, not against the
// operator's own hand: every loopback caller is by design the administrator.
// The unload succeeds, and the pin stays, so the model is protected again the
// moment anything loads it.
func TestUnloadingAPinnedModelLeavesThePinInPlace(t *testing.T) {
	cfg := config.Default()
	cfg.Pinned = []string{"org/keeper"}
	a, _, mux := newWiredControl(t, cfg, "org/keeper")
	srv := serve(t, mux)

	loadModel(t, a, "org/keeper")

	resp, err := srv.Client().Post(srv.URL+"/api/models/unload", "application/json",
		strings.NewReader(`{"model":"org/keeper"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unload of a pinned model = %d, want 200", resp.StatusCode)
	}
	if got := a.Pool.Resident(); len(got) != 0 {
		t.Errorf("Resident() = %+v after the unload, want none", got)
	}
	if got := a.Config().Pinned; len(got) != 1 || got[0] != "org/keeper" {
		t.Errorf("Pinned = %v after the unload, want the pin left in place", got)
	}
}
