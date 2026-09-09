package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/registry"
	"github.com/intentdriven/Gropius/internal/runtime"
)

const gb = int64(1) << 30

// newBudgetControl is newWiredControl on a machine of a size the test chooses,
// with models of sizes it chooses: every claim Settings makes about the memory
// budget is a claim about this Mac, and a test that reads the real one asserts
// nothing.
func newBudgetControl(t *testing.T, cfg config.Config, ram int64, models map[string]int64) (*app.App, *httptest.Server) {
	t.Helper()

	paths := config.NewPaths(t.TempDir())
	l := &recordingLauncher{specs: map[string]runtime.Spec{}}
	a, err := app.New(app.Options{
		Paths: paths, Config: cfg, Launcher: l,
		PhysicalMemory: func() int64 { return ram },
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	for id, size := range models {
		dir := paths.ModelDir(id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "weights.safetensors"), []byte("w"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := a.Registry.Put(registry.Model{
			RepoID: id, Path: dir, State: registry.StateReady, Bytes: size,
		}); err != nil {
			t.Fatal(err)
		}
	}

	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return a, srv
}

// stateOf reads the control plane's whole picture.
func stateOf(t *testing.T, srv *httptest.Server) State {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// Settings shows the budget, what share of this Mac it is, and whether it is
// the default — none of which the panel can say without being told.
func TestStateReportsTheMachineAndTheBudget(t *testing.T) {
	_, srv := newBudgetControl(t, config.Default(), 128*gb, nil)

	m := stateOf(t, srv).Machine
	if m.TotalRAM != 128*gb {
		t.Errorf("machine.total_ram = %d, want %d", m.TotalRAM, 128*gb)
	}
	if want := capability.DefaultBudget(128 * gb); m.Budget != want {
		t.Errorf("machine.budget = %d, want the default %d", m.Budget, want)
	}
	if !m.BudgetIsDefault {
		t.Error("machine.budget_is_default = false on a fresh install, which stores no budget")
	}
	if m.WarnAbove <= 0 || m.WarnAbove >= m.TotalRAM {
		t.Errorf("machine.warn_above = %d, want a share of this Mac's memory", m.WarnAbove)
	}
	if m.ResidentBytes != 0 || m.OverBudget {
		t.Errorf("machine reports %d bytes resident, over budget %v, with nothing loaded",
			m.ResidentBytes, m.OverBudget)
	}
}

// A Mac whose memory cannot be read says so by saying nothing: no total, no
// share to warn above. The panel shows the budget without a percentage.
func TestStateReportsNoShareOfAMachineItCannotMeasure(t *testing.T) {
	_, srv := newBudgetControl(t, config.Default(), 0, nil)

	m := stateOf(t, srv).Machine
	if m.TotalRAM != 0 {
		t.Errorf("machine.total_ram = %d, want 0 for a Mac that cannot be measured", m.TotalRAM)
	}
	if m.WarnAbove != 0 {
		t.Errorf("machine.warn_above = %d, want 0 — there is no share to take", m.WarnAbove)
	}
	if m.Budget != capability.DefaultBudget(0) {
		t.Errorf("machine.budget = %d, want the unmeasured default %d", m.Budget, capability.DefaultBudget(0))
	}
}

// A budget the operator lowers under what is already loaded unloads nothing.
// The panel says the machine is over budget until those models go.
func TestLoweringTheBudgetLeavesTheModelsAndReportsOverBudget(t *testing.T) {
	a, srv := newBudgetControl(t, config.Default(), 128*gb, map[string]int64{
		"org/writer": 4 * gb, "org/reviewer": 4 * gb,
	})
	for _, id := range []string{"org/writer", "org/reviewer"} {
		_, release, err := a.Pool.Acquire(context.Background(), id)
		if err != nil {
			t.Fatalf("Acquire(%s): %v", id, err)
		}
		release()
	}
	charge := 2 * capability.LoadCost(4*gb)

	body := fmt.Sprintf(`{"host":"127.0.0.1","port":11535,"api_key":"","decode_concurrency":4,`+
		`"idle_timeout_sec":0,"max_resident_bytes":%d}`, charge-gb)
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — lowering the budget is allowed", resp.StatusCode)
	}

	st := stateOf(t, srv)
	if len(st.Resident) != 2 {
		t.Errorf("%d models resident after the budget was lowered, want both left alone", len(st.Resident))
	}
	if st.Machine.ResidentBytes != charge {
		t.Errorf("machine.resident_bytes = %d, want the charged total %d", st.Machine.ResidentBytes, charge)
	}
	if !st.Machine.OverBudget {
		t.Error("machine.over_budget = false while the models in memory cost more than the budget")
	}
	if st.Machine.BudgetIsDefault {
		t.Error("machine.budget_is_default = true after an explicit budget was saved")
	}
}

// The budget reaches the pool on save, so the answer must not tell the
// operator to restart for a change that has already taken effect.
func TestSavingTheMemoryBudgetNeedsNoRestart(t *testing.T) {
	a, srv := newBudgetControl(t, config.Default(), 128*gb, nil)

	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":0,"max_resident_bytes":103079215104}` // 96 GB
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Restart bool   `json:"restart"`
		Warning string `json:"warning"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Restart {
		t.Error("saving the memory budget reported restart=true, but the pool is told at once")
	}
	if out.Warning != "" {
		t.Errorf("warning = %q, want none for a budget well under this Mac's memory", out.Warning)
	}
	if got := a.Pool.MemoryBudget(); got != 96*gb {
		t.Errorf("the pool holds %d, want the budget just saved", got)
	}
}

// A budget larger than the Mac is refused on the wire too, naming what the
// machine has.
func TestSettingsRefusesABudgetLargerThanThisMac(t *testing.T) {
	_, srv := newBudgetControl(t, config.Default(), 16*gb, nil)

	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":0,"max_resident_bytes":549755813888}` // 512 GB
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if !strings.Contains(out.Error.Message, runtime.HumanBytes(16*gb)) {
		t.Errorf("error = %q, want it to name this Mac's memory", out.Error.Message)
	}
}

// A budget that claims most of the Mac is saved and answered with advice, and
// the panel keeps saying so for as long as it stands.
func TestAHighBudgetIsSavedWithAWarning(t *testing.T) {
	_, srv := newBudgetControl(t, config.Default(), 100*gb, nil)

	body := `{"host":"127.0.0.1","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":0,"max_resident_bytes":102005473280}` // 95 GB
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a high budget is advice, not an error", resp.StatusCode)
	}
	var out struct {
		Warning string `json:"warning"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Warning == "" {
		t.Error("a budget claiming most of the Mac was saved with no warning")
	}

	st := stateOf(t, srv)
	var found bool
	for _, w := range st.Warnings {
		if strings.Contains(w, "memory budget") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want the high budget among them", st.Warnings)
	}
}

// The search tab hides what the pool would refuse, so it has to read the
// budget the operator set rather than a share of the machine of its own.
func TestSearchFollowsTheConfiguredBudget(t *testing.T) {
	// Small enough that the disk half of the "fits" check cannot decide the
	// outcome: capability.Assess measures the real volume, and a fixture sized
	// in tens of gigabytes would make this test a test of the host's free space.
	const modelSize = 64 << 20
	if free := capability.Assess(t.TempDir(), 128*gb, gb).FreeDisk; free > 0 && free < 4*gb {
		t.Skipf("this volume has %d bytes free; the search filter's disk check would decide the outcome", free)
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/models/org/big/tree/"):
			fmt.Fprintf(w, `[{"path":"model.safetensors","size":%d}]`, modelSize)
		case r.URL.Path == "/api/models":
			fmt.Fprint(w, `[{"id":"org/big"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	cfg := config.Default()
	cfg.MaxResidentBytes = 32 << 20 // too small for a 64 MB model charged 1.2x
	a, srv := newBudgetControl(t, cfg, 128*gb, nil)
	a.Hub.BaseURL = hub.URL

	search := func() (results int, hidden int) {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + "/api/search?q=big")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Results []map[string]any `json:"results"`
			Hidden  int              `json:"hidden"`
			Machine capability.Machine
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if out.Machine.RAMBudget != a.Pool.MemoryBudget() {
			t.Errorf("search measured against a budget of %d, want the configured %d",
				out.Machine.RAMBudget, a.Pool.MemoryBudget())
		}
		return len(out.Results), out.Hidden
	}

	if results, hidden := search(); results != 0 || hidden != 1 {
		t.Errorf("search showed %d results and hid %d under a budget too small for the model, want 0 and 1",
			results, hidden)
	}
	// Named, so a failure above cannot be read as the disk check firing.
	if reason := capability.Assess(t.TempDir(), 128*gb, a.Pool.MemoryBudget()).Reason(modelSize); !strings.Contains(reason, "memory") {
		t.Errorf("the model is hidden for %q, want the memory budget", reason)
	}

	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":0,"max_resident_bytes":1073741824}` // 1 GB
	resp := postJSON(t, srv, "/api/settings", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if results, hidden := search(); results != 1 || hidden != 0 {
		t.Errorf("search showed %d results and hid %d under a raised budget, want 1 and 0 — with no restart",
			results, hidden)
	}
}

// The panel has to be able to say what clearing the field would give, which is
// a figure only this Mac knows: the default share of its memory.
func TestStateCarriesTheDefaultBudgetTheMachineWouldUse(t *testing.T) {
	_, srv := newBudgetControl(t, config.Config{
		Host: "127.0.0.1", Port: 11535, DecodeConcurrency: 4,
		MaxResidentBytes: 96 * gb,
	}, 128*gb, nil)

	m := stateOf(t, srv).Machine
	if want := capability.DefaultBudget(128 * gb); m.DefaultBudget != want {
		t.Errorf("machine.default_budget = %d, want %d", m.DefaultBudget, want)
	}
	if m.Budget != 96*gb || m.BudgetIsDefault {
		t.Errorf("machine.budget = %d (default %v), want the saved figure", m.Budget, m.BudgetIsDefault)
	}
}

// The control plane answers from one reading of this Mac. Two — one cached at
// start-up for Settings, one taken per search — is how the Search tab comes to
// print a machine size the Settings tab says cannot be read.
func TestSearchAndStateAgreeAboutTheMachine(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	}))
	defer hub.Close()

	// A Mac whose memory could not be read at start-up: the reading the app
	// holds is 0, whatever a later sysctl would say.
	a, srv := newBudgetControl(t, config.Default(), 0, nil)
	a.Hub.BaseURL = hub.URL

	resp, err := srv.Client().Get(srv.URL + "/api/search?q=anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Machine capability.Machine `json:"machine"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Machine.TotalRAM != 0 {
		t.Errorf("search reports %d bytes of memory, want the 0 the rest of the panel reports",
			out.Machine.TotalRAM)
	}
	if got := stateOf(t, srv).Machine.TotalRAM; got != out.Machine.TotalRAM {
		t.Errorf("search says %d and state says %d about the same Mac", out.Machine.TotalRAM, got)
	}
}

// One name for one number. The search payload and the state snapshot both
// carry a "machine" object, and a budget spelled two ways across them is the
// drift this record removed from the snapshot in the first place.
func TestTheMachineObjectsSpellTheBudgetOneWay(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	}))
	defer hub.Close()

	a, srv := newBudgetControl(t, config.Default(), 128*gb, nil)
	a.Hub.BaseURL = hub.URL

	resp, err := srv.Client().Get(srv.URL + "/api/search?q=anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Machine map[string]any `json:"machine"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Machine["ram_budget"]; ok {
		t.Errorf("the search machine spells the budget ram_budget; the state machine spells it budget: %v", out.Machine)
	}
	budget, ok := out.Machine["budget"].(float64)
	if !ok {
		t.Fatalf("the search machine carries no budget: %v", out.Machine)
	}
	if int64(budget) != a.Pool.MemoryBudget() {
		t.Errorf("search measured against %v, want the budget in force %d", budget, a.Pool.MemoryBudget())
	}
}
