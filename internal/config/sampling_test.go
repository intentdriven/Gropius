package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func intp(v int) *int        { return &v }

// serverBounds is what mlx-lm 0.31.3 itself accepts, read off its own source
// and recorded in
// .abcd/development/research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md:
// validate_model_parameters for the request check, and sample_utils for the
// sampler's own limits.
var serverBounds = []SamplingBound{
	{Field: "temperature", Min: 0, ServerDefault: 0},
	{Field: "top_p", Min: 0, Max: 1, HasMax: true, ServerDefault: 1},
	{Field: "top_k", Min: 0, Integer: true, ServerDefault: 0}, // plus a sampler limit of vocab_size
	{Field: "min_p", Min: 0, Max: 1, HasMax: true, ServerDefault: 0},
	{Field: "max_tokens", Min: 0, Integer: true, ServerDefault: 512},
}

// The Go bounds exist to stop a saved setting from becoming a machine-wide
// outage: the value becomes the model server's own default, so anything it
// refuses breaks every request that omits that parameter while requests
// carrying their own value carry on working. The bounds are therefore not a
// design choice, and must never be wider than the server's own.
func TestGoRangesAreNeverWiderThanThePinnedServers(t *testing.T) {
	got := SamplingBounds()
	if len(got) != len(serverBounds) {
		t.Fatalf("SamplingBounds() has %d entries, the recorded server table has %d", len(got), len(serverBounds))
	}
	for i, g := range got {
		s := serverBounds[i]
		if g.Field != s.Field || g.Integer != s.Integer {
			t.Fatalf("bound %d = %+v, want the same parameter as %+v", i, g, s)
		}
		// A blank field means "pass no flag", so what applies is the server's
		// own default. Recording a different figure would make every place
		// that shows it — the panel's placeholder, the reference page — lie.
		if g.ServerDefault != s.ServerDefault {
			t.Errorf("%s records the model server's default as %v, but it is %v",
				g.Field, g.ServerDefault, s.ServerDefault)
		}
		if g.Min < s.Min {
			t.Errorf("%s accepts values down to %v, below the server's %v", g.Field, g.Min, s.Min)
		}
		if s.HasMax && (!g.HasMax || g.Max > s.Max) {
			t.Errorf("%s accepts values up to %v, above the server's %v", g.Field, g.Max, s.Max)
		}
	}
}

// Narrower is safe; drifting is not. This pins the bounds exactly, including
// the one place Gropius is deliberately stricter than the request check.
func TestGoRangesAreExactlyThese(t *testing.T) {
	want := []SamplingBound{
		{Field: "temperature", Min: 0, ServerDefault: 0},
		{Field: "top_p", Min: 0, Max: 1, HasMax: true, ServerDefault: 1},
		// The request check accepts any non-negative top_k, but the sampler
		// refuses one at or above the model's vocabulary size — and it raises
		// inside generation, so the process starts healthy and then fails
		// every request that omits top_k. Gropius cannot know a vocabulary
		// size when the value is saved, so it caps top_k far below the
		// smallest an MLX model ships.
		{Field: "top_k", Min: 0, Max: 1024, HasMax: true, Integer: true, ServerDefault: 0},
		{Field: "min_p", Min: 0, Max: 1, HasMax: true, ServerDefault: 0},
		// The request check takes any non-negative budget. A default above any
		// real context window means "generate until the model stops" on every
		// request that omits the parameter, and it is also the value that put
		// an integer conversion out of range.
		{
			Field: "max_tokens", Min: 0, Max: MaxCompletionTokens, HasMax: true, Integer: true,
			ServerDefault: 512,
		},
	}
	if got := SamplingBounds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SamplingBounds() = %+v\nwant %+v", got, want)
	}
}

// A top-k the sampler will not take must be refused at the door, not
// discovered on the first request that omits the parameter.
func TestTopKAboveTheSamplerCeilingIsRefused(t *testing.T) {
	if err := (Sampling{TopK: intp(200000)}).Validate(); err == nil {
		t.Fatal("a top_k above every model's vocabulary size was accepted; it makes the sampler raise on every request that omits top_k")
	}
	if err := (Sampling{TopK: intp(1024)}).Validate(); err != nil {
		t.Errorf("top_k 1024 = %v, want it accepted", err)
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

	cfg, notices, err := Load(path)
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
	if len(notices.All()) != 1 || !strings.Contains(notices.All()[0], "top_p") {
		t.Errorf("dropped = %v, want exactly the top_p field named so main can log it", notices.All())
	}
}

func TestLoadDropsAnOverrideWithAnUnusableRepoID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"host":"127.0.0.1","port":11535,"decode_concurrency":4,
	         "models":{"../../etc":{"sampling":{"temperature":0.2}},"org/name":{"sampling":{"temperature":0.3}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, notices, err := Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if _, ok := cfg.Models["../../etc"]; ok {
		t.Error("an override keyed by a malformed repo id survived Load")
	}
	if _, ok := cfg.Models["org/name"]; !ok {
		t.Error("the well-formed override was dropped too")
	}
	if len(notices.All()) == 0 {
		t.Error("dropped = empty, want the rejected key named")
	}
}

// A per-model override replaces the parameters it sets and no others, so an
// override that names only a temperature still gets the machine's token budget.
func TestEffectiveSamplingOverridesPerParameter(t *testing.T) {
	cfg := Default()
	cfg.Sampling = Sampling{Temperature: f64(0.7), MaxTokens: intp(4096)}
	cfg.Models = map[string]ModelSettings{
		"org/Thinker": {Sampling: Sampling{Temperature: f64(0.2)}},
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
	cfg.Models = map[string]ModelSettings{"org/one": {Sampling: Sampling{TopP: f64(0.9)}}}

	clone := cfg.Clone()
	*clone.Sampling.Temperature = 1.5
	clone.Preload[0] = "org/two"
	*clone.Models["org/one"].Sampling.TopP = 0.1
	clone.Models["org/two"] = ModelSettings{}

	if *cfg.Sampling.Temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7 — the clone wrote through a shared pointer", *cfg.Sampling.Temperature)
	}
	if cfg.Preload[0] != "org/one" {
		t.Errorf("preload = %v, want the original slice untouched", cfg.Preload)
	}
	if *cfg.Models["org/one"].Sampling.TopP != 0.9 {
		t.Errorf("override top_p = %v, want 0.9", *cfg.Models["org/one"].Sampling.TopP)
	}
	if len(cfg.Models) != 1 {
		t.Errorf("per-model map grew to %d entries — the map itself is shared", len(cfg.Models))
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

// The override map is written from a file another local account can edit in
// shared-cache mode, and everything saved lands in config.json — which Load
// refuses above MaxConfigBytes, sending the next start into its fail-closed
// loopback-only branch. The count is bounded so this field cannot be the
// lever for that.
func TestTooManyOverridesIsRefusedAndDroppedOnLoad(t *testing.T) {
	cfg := Default()
	cfg.Models = map[string]ModelSettings{}
	for i := range MaxModels + 5 {
		cfg.Models[fmt.Sprintf("org/m%d", i)] = ModelSettings{Sampling: Sampling{Temperature: f64(0.5)}}
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted more overrides than the ceiling allows")
	}

	// The same file must still start the server.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, notices, err := Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want nil — an oversized override map must not lock the server down", err)
	}
	if len(loaded.Models) != MaxModels {
		t.Errorf("kept %d entries, want the ceiling of %d", len(loaded.Models), MaxModels)
	}
	if len(notices.All()) != 5 {
		t.Errorf("dropped %d, want the 5 beyond the ceiling named", len(notices.All()))
	}
}

// EffectiveSampling must not depend on map iteration order even if a map with
// two spellings of one id ever reached it.
func TestEffectiveSamplingIsDeterministic(t *testing.T) {
	cfg := Default()
	cfg.Models = map[string]ModelSettings{
		"org/Model": {Sampling: Sampling{Temperature: f64(0.1)}},
		"ORG/model": {Sampling: Sampling{Temperature: f64(0.9)}},
	}
	first := cfg.EffectiveSampling("org/model")
	for range 200 {
		if !cfg.EffectiveSampling("org/model").Equal(first) {
			t.Fatal("EffectiveSampling returned different values across calls")
		}
	}
}

// TestCloneSharesNothingWithTheOriginal names the fields it knows about, so a
// reference-typed field added to Config later would slip past it. This walks
// the type instead: every pointer, slice and map reachable from a Config must
// point somewhere else after Clone, and a field this test forgets to populate
// is reported rather than skipped.
func TestCloneCopiesEveryReferenceInTheType(t *testing.T) {
	cfg := Default()
	cfg.Preload = []string{"org/one"}
	full := Sampling{
		Temperature: f64(0.7), TopP: f64(0.9), TopK: intp(40),
		MinP: f64(0.05), MaxTokens: intp(4096),
	}
	cfg.Sampling = full.Clone()
	cfg.Models = map[string]ModelSettings{
		"org/one": {MergeSystemMessages: true, Pinned: true, Sampling: full.Clone()},
	}
	cfg.ChatRule = ChatRule{PipelineTags: []string{"text-generation"}, RequiredTags: []string{"conversational"}}

	before := map[string]uintptr{}
	collectRefs(t, "Config", reflect.ValueOf(cfg), before)
	after := map[string]uintptr{}
	collectRefs(t, "Config", reflect.ValueOf(cfg.Clone()), after)

	if len(before) == 0 {
		t.Fatal("no reference-typed fields were found, so this test proves nothing")
	}
	for path, addr := range before {
		other, ok := after[path]
		if !ok {
			t.Errorf("Clone dropped %s entirely", path)
			continue
		}
		if addr == other {
			t.Errorf("Clone shares %s with the original — a rejected settings post could write through it", path)
		}
	}
}

// collectRefs records the address every pointer, slice and map under v refers
// to, keyed by its path within the type.
func collectRefs(t *testing.T, path string, v reflect.Value, out map[string]uintptr) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.IsNil() {
			t.Errorf("this test leaves %s nil, so aliasing there goes untested — populate it", path)
			return
		}
		out[path] = v.Pointer()
		switch v.Kind() {
		case reflect.Pointer:
			collectRefs(t, path+".*", v.Elem(), out)
		case reflect.Map:
			for _, k := range v.MapKeys() {
				collectRefs(t, path+"["+k.String()+"]", v.MapIndex(k), out)
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			collectRefs(t, path+"."+v.Type().Field(i).Name, v.Field(i), out)
		}
	}
}

// Everything the settings endpoint accepts is written to config.json, and a
// config.json Load will not read sends the next start into its fail-closed
// loopback-only branch — the LAN endpoint gone until someone edits the file by
// hand. Save is the last place that can be prevented.
func TestSaveRefusesAConfigTooLargeToLoadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Default()
	cfg.APIKey = strings.Repeat("k", MaxConfigBytes)
	if err := Save(path, cfg); err == nil {
		t.Fatal("Save wrote a config.json that Load will refuse to read")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a refused Save left a file behind: %v", err)
	}

	// The same config within the limit still saves and loads.
	cfg.APIKey = "bh_short"
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// A repo id is a key in the override map and a directory name on disk. Bounding
// its length is what makes "at most MaxModels models" a bound on
// bytes rather than only on entries.
func TestValidRepoIDBoundsLength(t *testing.T) {
	long := strings.Repeat("a", 200)
	if ValidRepoID(long + "/name") {
		t.Error("an unbounded organization name was accepted")
	}
	if ValidRepoID("org/" + long) {
		t.Error("an unbounded repository name was accepted")
	}
	if !ValidRepoID("mlx-community/Qwen3-8B-4bit") {
		t.Error("a real repo id was refused")
	}
	if !ValidRepoID(strings.Repeat("a", MaxRepoComponent) + "/" + strings.Repeat("b", MaxRepoComponent)) {
		t.Error("a repo id at the length limit was refused")
	}
}

// The two bounds together: a full override map, at the longest ids allowed,
// beside the other settings, must still round-trip through the file.
func TestAFullOverrideMapStillFitsTheConfigFile(t *testing.T) {
	cfg := Default()
	cfg.Models = map[string]ModelSettings{}
	for i := range MaxModels {
		id := fmt.Sprintf("%s%03d/%s", strings.Repeat("o", MaxRepoComponent-3), i, strings.Repeat("n", MaxRepoComponent))
		cfg.Models[id] = ModelSettings{
			MergeSystemMessages: true,
			Pinned:              true,
			Sampling: Sampling{
				Temperature: f64(0.7), TopP: f64(0.95), TopK: intp(40),
				MinP: f64(0.05), MaxTokens: intp(8192),
			},
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v — a legal override map does not fit the file", err)
	}
	loaded, notices, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(notices.All()) != 0 {
		t.Errorf("dropped %v from a config within every limit", notices.All())
	}
	if len(loaded.Models) != MaxModels {
		t.Errorf("loaded %d entries, want %d", len(loaded.Models), MaxModels)
	}
}

// Validate is exported, so its guarantee cannot rest on JSON being the only
// way in. NaN passes every comparison — NaN < min and NaN > max are both
// false — and would reach the model server's sampler as a launch flag.
func TestSamplingValidateRefusesNaNAndInfinity(t *testing.T) {
	for name, v := range map[string]float64{
		"NaN":               math.NaN(),
		"positive infinity": math.Inf(1),
		"negative infinity": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := (Sampling{Temperature: &v}).Validate(); err == nil {
				t.Errorf("Validate accepted a temperature of %v", v)
			}
			s, dropped := Sampling{Temperature: &v}.Sanitized()
			if s.Temperature != nil {
				t.Errorf("Sanitized kept a temperature of %v", v)
			}
			if len(dropped) != 1 {
				t.Errorf("dropped = %v, want the field named", dropped)
			}
		})
	}
}

// A whole-number setting must survive as a whole number. Widening it to a
// float64 and narrowing back is a conversion Go leaves to the platform once the
// value is out of range: on one architecture it saturates, on another it wraps
// to a negative — which would then be rendered onto a model server's command
// line as a negative token budget.
func TestIntegerSamplingValuesAreCarriedAsIntegers(t *testing.T) {
	s := Sampling{TopK: intp(1024), MaxTokens: intp(MaxCompletionTokens)}
	got := map[string]int{}
	for _, v := range s.Values() {
		if !v.Integer {
			t.Errorf("%s is not carried as an integer", v.Field)
			continue
		}
		got[v.Field] = v.Int
	}
	if got["top_k"] != 1024 {
		t.Errorf("top_k = %d, want 1024", got["top_k"])
	}
	if got["max_tokens"] != MaxCompletionTokens {
		t.Errorf("max_tokens = %d, want %d", got["max_tokens"], MaxCompletionTokens)
	}
}

// A token budget above any real context window is not a preference, it is a
// number that stopped meaning anything — and it was the lever that let an
// integer conversion go out of range.
func TestMaxTokensAboveTheCeilingIsRefusedAndDropped(t *testing.T) {
	if err := (Sampling{MaxTokens: intp(MaxCompletionTokens)}).Validate(); err != nil {
		t.Errorf("max_tokens at the ceiling = %v, want it accepted", err)
	}
	for _, v := range []int{MaxCompletionTokens + 1, 1<<63 - 1} {
		if err := (Sampling{MaxTokens: intp(v)}).Validate(); err == nil {
			t.Errorf("Validate accepted max_tokens %d", v)
		}
	}

	// And in a hand-edited file it is dropped, not fatal.
	path := filepath.Join(t.TempDir(), "config.json")
	raw := fmt.Sprintf(`{"host":"0.0.0.0","port":11535,"decode_concurrency":4,
		"sampling":{"max_tokens":%d}}`, 1<<63-1)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, notices, err := Load(path)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Sampling.MaxTokens != nil {
		t.Errorf("max_tokens = %d, want it dropped", *cfg.Sampling.MaxTokens)
	}
	if len(notices.All()) != 1 || !strings.Contains(notices.All()[0], "max_tokens") {
		t.Errorf("dropped = %v, want max_tokens named", notices.All())
	}
}
