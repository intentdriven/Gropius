package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func intp(v int) *int        { return &v }

// The Go bounds exist to stop a saved setting from becoming a machine-wide
// outage: the pinned server validates the *effective* value of every request,
// so a launch flag it rejects makes every request that omits that parameter
// fail with 400. The bounds are therefore not a design choice — they are read
// off mlx-lm 0.31.3's own validate_model_parameters and must never be wider
// than it. See .abcd/development/research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md.
func TestGoRangesMatchThePinnedServerRanges(t *testing.T) {
	// Recorded from mlx_lm/server.py 0.31.3, HTTPHandler.validate_model_parameters.
	want := []SamplingBound{
		{Field: "temperature", Min: 0},
		{Field: "top_p", Min: 0, Max: 1, HasMax: true},
		{Field: "top_k", Min: 0, Integer: true},
		{Field: "min_p", Min: 0, Max: 1, HasMax: true},
		{Field: "max_tokens", Min: 0, Integer: true},
	}
	got := SamplingBounds()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SamplingBounds() = %+v\nwant %+v\n(a bound wider than the pinned server's turns one settings save into a 400 on every request that omits the parameter)", got, want)
	}
}

func TestSamplingValidateNamesTheOffendingField(t *testing.T) {
	cases := []struct {
		name string
		s    Sampling
		want string // substring the error must name
	}{
		{"negative temperature", Sampling{Temperature: f64(-0.5)}, "temperature"},
		{"top_p above one", Sampling{TopP: f64(1.5)}, "top_p"},
		{"negative top_k", Sampling{TopK: intp(-1)}, "top_k"},
		{"min_p above one", Sampling{MinP: f64(2)}, "min_p"},
		{"negative max_tokens", Sampling{MaxTokens: intp(-8)}, "max_tokens"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.s.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error naming %s", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate() = %q, want it to name the field %q", err, c.want)
			}
		})
	}
}

func TestSamplingValidateAcceptsTheServersOwnDefaults(t *testing.T) {
	s := Sampling{
		Temperature: f64(0), TopP: f64(1), TopK: intp(0), MinP: f64(0), MaxTokens: intp(512),
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil — the server's own defaults must be settable", err)
	}
}

// An out-of-range value in a hand-edited config.json is a preference, not a
// serving invariant: dropping it keeps one bad number from locking the whole
// server down to loopback (the fail-closed branch in cmd/gropius).
func TestLoadDropsOutOfRangeSamplingRatherThanRefusingTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"host":"0.0.0.0","port":11535,"decode_concurrency":4,
	         "sampling":{"temperature":0.7,"top_p":9,"max_tokens":8192}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want nil — an out-of-range preference must not lock the server down", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("host = %q, want the file's own value kept", cfg.Host)
	}
	if cfg.Sampling.TopP != nil {
		t.Errorf("top_p = %v, want it dropped", *cfg.Sampling.TopP)
	}
	if cfg.Sampling.Temperature == nil || *cfg.Sampling.Temperature != 0.7 {
		t.Errorf("temperature = %v, want the in-range value kept", cfg.Sampling.Temperature)
	}
	if cfg.Sampling.MaxTokens == nil || *cfg.Sampling.MaxTokens != 8192 {
		t.Errorf("max_tokens = %v, want the in-range value kept", cfg.Sampling.MaxTokens)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "top_p") {
		t.Errorf("dropped = %v, want exactly the top_p field named so main can log it", dropped)
	}
}

func TestLoadDropsAnOverrideWithAnUnusableRepoID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
	         "model_sampling":{"../../etc":{"temperature":0.2},"org/name":{"temperature":0.3}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, dropped, err := Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if _, ok := cfg.ModelSampling["../../etc"]; ok {
		t.Error("an override keyed by a malformed repo id survived Load")
	}
	if _, ok := cfg.ModelSampling["org/name"]; !ok {
		t.Error("the well-formed override was dropped too")
	}
	if len(dropped) == 0 {
		t.Error("dropped = empty, want the rejected key named")
	}
}

// A per-model override replaces the parameters it sets and no others, so an
// override that names only a temperature still gets the machine's token budget.
func TestEffectiveSamplingOverridesPerParameter(t *testing.T) {
	cfg := Default()
	cfg.Sampling = Sampling{Temperature: f64(0.7), MaxTokens: intp(4096)}
	cfg.ModelSampling = map[string]Sampling{
		"org/Thinker": {Temperature: f64(0.2)},
	}

	global := cfg.EffectiveSampling("org/other")
	if global.Temperature == nil || *global.Temperature != 0.7 {
		t.Errorf("a model with no override got %v, want the global 0.7", global.Temperature)
	}

	// The registry folds repo-id case, so an override must match the same way.
	over := cfg.EffectiveSampling("ORG/thinker")
	if over.Temperature == nil || *over.Temperature != 0.2 {
		t.Errorf("temperature = %v, want the override's 0.2", over.Temperature)
	}
	if over.MaxTokens == nil || *over.MaxTokens != 4096 {
		t.Errorf("max_tokens = %v, want the global 4096 to survive a partial override", over.MaxTokens)
	}
}

// Clone must break every alias, or a rejected /api/settings post would mutate
// the live configuration through a shared pointee before Validate ever ran.
func TestCloneSharesNothingWithTheOriginal(t *testing.T) {
	cfg := Default()
	cfg.Preload = []string{"org/one"}
	cfg.Sampling = Sampling{Temperature: f64(0.7)}
	cfg.ModelSampling = map[string]Sampling{"org/one": {TopP: f64(0.9)}}

	clone := cfg.Clone()
	*clone.Sampling.Temperature = 1.5
	clone.Preload[0] = "org/two"
	*clone.ModelSampling["org/one"].TopP = 0.1
	clone.ModelSampling["org/two"] = Sampling{}

	if *cfg.Sampling.Temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7 — the clone wrote through a shared pointer", *cfg.Sampling.Temperature)
	}
	if cfg.Preload[0] != "org/one" {
		t.Errorf("preload = %v, want the original slice untouched", cfg.Preload)
	}
	if *cfg.ModelSampling["org/one"].TopP != 0.9 {
		t.Errorf("override top_p = %v, want 0.9", *cfg.ModelSampling["org/one"].TopP)
	}
	if len(cfg.ModelSampling) != 1 {
		t.Errorf("override map grew to %d entries — the map itself is shared", len(cfg.ModelSampling))
	}
}

// A blank field must round-trip as "unset", never as zero: zero is a real
// temperature (greedy decoding), and the two must stay distinguishable.
func TestBlankSamplingFieldStaysUnsetThroughJSON(t *testing.T) {
	var s Sampling
	if err := json.Unmarshal([]byte(`{"temperature":null,"top_k":0}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Temperature != nil {
		t.Errorf("temperature = %v, want nil for an explicit null", *s.Temperature)
	}
	if s.TopK == nil || *s.TopK != 0 {
		t.Errorf("top_k = %v, want a set zero", s.TopK)
	}

	b, err := json.Marshal(Config{Host: "h", Port: 1, DecodeConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sampling") {
		t.Errorf("an unset sampling block was written to config.json: %s", b)
	}
}
