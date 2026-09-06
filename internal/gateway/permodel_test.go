package gateway

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// perModelAfterSave posts a settings body and returns the per-model settings
// the app holds afterwards, read back the way the panel reads them.
func perModelAfterSave(t *testing.T, srv interface {
	Client() *http.Client
}, url, body string, wantStatus int) map[string]config.ModelSettings {
	t.Helper()
	resp, err := srv.Client().Post(url+"/api/settings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST /api/settings status = %d, want %d", resp.StatusCode, wantStatus)
	}

	stResp, err := srv.Client().Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stResp.Body.Close()
	var st State
	if err := json.NewDecoder(stResp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st.Config.PerModel
}

// Switching a per-model setting off is done by saving a Settings form that
// leaves the model out. encoding/json merges keys into a map it decodes into
// rather than replacing it, so a handler that decoded the form over the stored
// settings would leave every switch on forever: a form can add a model but
// never remove one.
func TestSavingSettingsWithoutAModelTurnsItsSwitchOff(t *testing.T) {
	cfg := config.Default()
	cfg.PerModel = map[string]config.ModelSettings{
		"mlx-community/Qwen3-8B-4bit": {MergeSystemMessages: true},
	}
	srv := newTestControl(t, cfg)

	got := perModelAfterSave(t, srv, srv.URL,
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0,"per_model":{}}`,
		http.StatusOK)

	if len(got) != 0 {
		t.Errorf("per-model settings after a save that left the model out = %+v, want none", got)
	}
}

// A form that does not own the per-model settings at all must not wipe them,
// exactly as it must not wipe Preload or Advertise.
func TestSavingSettingsThatOmitsPerModelKeepsIt(t *testing.T) {
	cfg := config.Default()
	cfg.PerModel = map[string]config.ModelSettings{
		"mlx-community/Qwen3-8B-4bit": {MergeSystemMessages: true},
	}
	srv := newTestControl(t, cfg)

	got := perModelAfterSave(t, srv, srv.URL,
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0}`,
		http.StatusOK)

	if !got["mlx-community/Qwen3-8B-4bit"].MergeSystemMessages {
		t.Errorf("per-model settings = %+v, want the stored switch kept by a save that never mentioned it", got)
	}
}

// A key that is not a model id is refused, and the refusal leaves the stored
// settings as they were.
func TestSettingsRejectsAPerModelKeyThatIsNotAModelID(t *testing.T) {
	cfg := config.Default()
	cfg.PerModel = map[string]config.ModelSettings{
		"mlx-community/Qwen3-8B-4bit": {MergeSystemMessages: true},
	}
	srv := newTestControl(t, cfg)

	got := perModelAfterSave(t, srv, srv.URL,
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0,`+
			`"per_model":{"../../etc":{"merge_system_messages":true}}}`,
		http.StatusBadRequest)

	if !got["mlx-community/Qwen3-8B-4bit"].MergeSystemMessages {
		t.Errorf("per-model settings after a refused save = %+v, want the stored settings untouched", got)
	}
	if _, ok := got["../../etc"]; ok {
		t.Error("a refused key reached the stored settings")
	}
}

// A refused save must leave the running settings exactly as they were, and
// Preload is the field that makes that hard: encoding/json decodes an array
// into an existing slice by reusing its backing array, and a Config copy
// shares that array with the running config, so the decode writes through to
// the live settings before anything has validated them. The per-model key
// check is one of the refusals that then returns 400 over settings it has
// already changed.
func TestARefusedSaveDoesNotRewriteTheLivePreloadList(t *testing.T) {
	cfg := config.Default()
	cfg.Preload = []string{"mlx-community/Qwen3-8B-4bit", "mlx-community/Qwen3-0.6B-4bit"}
	srv := newTestControl(t, cfg)

	resp, err := srv.Client().Post(srv.URL+"/api/settings", "application/json", strings.NewReader(
		`{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,"idle_timeout_sec":0,`+
			`"preload":["org/planted"],`+
			`"per_model":{"../../etc":{"merge_system_messages":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	stResp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stResp.Body.Close()
	var st State
	if err := json.NewDecoder(stResp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	want := []string{"mlx-community/Qwen3-8B-4bit", "mlx-community/Qwen3-0.6B-4bit"}
	if !reflect.DeepEqual(st.Config.Preload, want) {
		t.Errorf("preload after a refused save = %v, want it untouched %v", st.Config.Preload, want)
	}
}
