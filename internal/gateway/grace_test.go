package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// Grace is applied to the running pool at the moment it is saved, so the panel
// must not tell the operator to restart for it.
func TestSavingTheEvictionGraceNeedsNoRestart(t *testing.T) {
	a, srv := newBudgetControl(t, config.Default(), 128*gb, nil)

	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":0,"eviction_grace":true,"eviction_grace_sec":45,` +
		`"eviction_max_wait_sec":90}`
	resp := postJSON(t, srv, "/api/settings", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Restart bool `json:"restart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Restart {
		t.Error("saving the eviction grace reported restart=true, but the pool is told at once")
	}
	grace, maxWait := a.Pool.EvictionGrace()
	if grace != 45*time.Second || maxWait != 90*time.Second {
		t.Errorf("the pool holds %s/%s, want the 45s/90s just saved", grace, maxWait)
	}
}

// The idle reaper would unload the model a wait is protecting, so the two
// figures are refused together, with both of them named.
func TestSettingsRefusesAGraceLongerThanTheIdleTimeout(t *testing.T) {
	a, srv := newBudgetControl(t, config.Default(), 128*gb, nil)

	body := `{"host":"0.0.0.0","port":11535,"api_key":"","decode_concurrency":4,` +
		`"idle_timeout_sec":60,"eviction_grace":true,"eviction_grace_sec":300,` +
		`"eviction_max_wait_sec":600}`
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
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"60", "300"} {
		if !strings.Contains(out.Error.Message, want) {
			t.Errorf("the refusal %q does not name %s", out.Error.Message, want)
		}
	}
	if grace, _ := a.Pool.EvictionGrace(); grace != 0 {
		t.Errorf("the refused save reached the pool: grace = %s", grace)
	}
}

// A queue of requests waiting for room is the one thing about grace the panel
// cannot see any other way: a waiter holds no model, so it appears nowhere in
// the resident list, and "nothing loading, nothing refused" would otherwise be
// indistinguishable from a queue that is not draining.
func TestStateReportsHowManyRequestsAreWaitingForRoom(t *testing.T) {
	cfg := config.Default()
	cfg.MaxResidentBytes = 250 // LoadCost is 1.2x, so it holds one of the two
	cfg.EvictionGrace = true
	cfg.EvictionGraceSec = 30
	cfg.EvictionMaxWaitSec = 60
	a, srv := newBudgetControl(t, cfg, 128*gb, map[string]int64{"org/a": 200, "org/b": 200})

	if got := stateOf(t, srv).Waiting; got != 0 {
		t.Fatalf("state.waiting = %d before anything was asked for, want 0", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, release, err := a.Pool.Acquire(ctx, "org/a")
	if err != nil {
		t.Fatalf("Acquire(org/a): %v", err)
	}
	release() // idle, and inside its grace

	queued, giveUp := context.WithCancel(context.Background())
	defer giveUp()
	go func() {
		if _, rel, err := a.Pool.Acquire(queued, "org/b"); err == nil {
			rel()
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if stateOf(t, srv).Waiting == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state.waiting = %d while a request was queued for room, want 1",
		stateOf(t, srv).Waiting)
}
