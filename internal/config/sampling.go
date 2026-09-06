package config

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Sampling holds machine-wide defaults for the sampling parameters the pinned
// mlx-lm server takes as start-up options. They are handed to each model
// server as launch flags, so the server itself owns the default: a request
// that omits a parameter is served with the value here, and a request that
// carries its own value replaces it for that request alone. The completion
// path never sees these values.
//
// Every field is a pointer because a blank field and a zero are different
// answers. Zero is a real temperature — greedy decoding — while nil means
// "pass no flag, let the model server use its own default".
//
// The set is exactly the parameters mlx-lm 0.31.3 exposes as launch flags;
// anything it reads per request only (repetition and presence penalties, the
// xtc parameters, logit_bias, logprobs, seed) cannot be defaulted this way.
type Sampling struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	MinP        *float64 `json:"min_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

// SamplingBound is the range one sampling parameter accepts, and the value the
// model server itself uses when Gropius holds none.
type SamplingBound struct {
	// Field is the parameter's name, as it appears in config.json and in the
	// error a refused settings save reports.
	Field string
	Min   float64
	Max   float64
	// HasMax is false where the model server sets no upper bound.
	HasMax bool
	// Integer reports whether the parameter is a whole number.
	Integer bool
	// ServerDefault is what the model server applies when it is passed no flag
	// for this parameter — which is what a blank field in Settings means. The
	// panel's placeholder and the reference page both have to say this figure,
	// so it is held here rather than written out in each of them.
	ServerDefault float64
}

// DefaultText renders the model server's own default as the panel and the
// documentation must show it.
func (b SamplingBound) DefaultText() string { return formatBound(b.ServerDefault, b.Integer) }

// samplingParam couples one bound to the field it governs, so validation,
// sanitizing and the range test all read the same table.
type samplingParam struct {
	bound SamplingBound
	// get returns the field's value and whether it is set, as a float64 so one
	// bounds check serves every parameter.
	get func(Sampling) (float64, bool)
	// getInt returns a whole-number field's value without going through a
	// float64. Set for every parameter whose bound says Integer.
	getInt func(Sampling) (int, bool)
	// clear unsets the field.
	clear func(*Sampling)
}

// samplingParams records what mlx-lm 0.31.3's own request validation accepts,
// read from its validate_model_parameters (see the research note
// .abcd/development/research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md).
//
// These bounds are not a matter of taste. The server validates the *effective*
// value of every request — the body's value where there is one, the launch
// flag's otherwise — and it does not refuse politely: the check raises
// uncaught, so the connection is closed with no response and the gateway
// answers 502. A default this package accepts and the server rejects therefore
// breaks every request that omits the parameter, which is the whole population
// this feature exists for. Gropius must never accept a value wider than the
// table below.
// MaxTopK is Gropius' own ceiling on top-k, deliberately narrower than the
// request check, which accepts any non-negative integer.
//
// The sampler imposes a second limit the request check does not: it refuses a
// top-k at or above the model's vocabulary size, and it raises inside
// generation rather than at start-up — so an oversized default leaves the
// process healthy and fails every request that omits top_k, silently and
// across restarts. Gropius has no vocabulary size to compare against when the
// value is saved, so it caps top-k far below the smallest vocabulary an MLX
// model ships (tens of thousands of tokens). Nothing is lost: keeping more
// than a thousand candidates is indistinguishable from keeping all of them.
const MaxTopK = 1024

// MaxCompletionTokens is Gropius' own ceiling on the completion-token default.
//
// The model server sets none: it takes any non-negative integer. But this is a
// default applied to every request that omits the parameter, so a figure above
// any real context window does not mean "a generous budget", it means "keep
// generating until the model stops" — one request holding a model for as long
// as it likes. A million tokens is beyond the longest context any MLX model
// serves today, and it keeps the value inside the range where converting it is
// not a question about the architecture.
const MaxCompletionTokens = 1 << 20

var samplingParams = []samplingParam{
	{
		bound: SamplingBound{Field: "temperature", Min: 0, ServerDefault: 0},
		get:   func(s Sampling) (float64, bool) { return deref(s.Temperature) },
		clear: func(s *Sampling) { s.Temperature = nil },
	},
	{
		bound: SamplingBound{Field: "top_p", Min: 0, Max: 1, HasMax: true, ServerDefault: 1},
		get:   func(s Sampling) (float64, bool) { return deref(s.TopP) },
		clear: func(s *Sampling) { s.TopP = nil },
	},
	{
		bound:  SamplingBound{Field: "top_k", Min: 0, Max: MaxTopK, HasMax: true, Integer: true, ServerDefault: 0},
		get:    func(s Sampling) (float64, bool) { return derefInt(s.TopK) },
		getInt: func(s Sampling) (int, bool) { return derefIntExact(s.TopK) },
		clear:  func(s *Sampling) { s.TopK = nil },
	},
	{
		bound: SamplingBound{Field: "min_p", Min: 0, Max: 1, HasMax: true, ServerDefault: 0},
		get:   func(s Sampling) (float64, bool) { return deref(s.MinP) },
		clear: func(s *Sampling) { s.MinP = nil },
	},
	{
		bound: SamplingBound{
			Field: "max_tokens", Min: 0, Max: MaxCompletionTokens, HasMax: true, Integer: true,
			ServerDefault: 512,
		},
		get:    func(s Sampling) (float64, bool) { return derefInt(s.MaxTokens) },
		getInt: func(s Sampling) (int, bool) { return derefIntExact(s.MaxTokens) },
		clear:  func(s *Sampling) { s.MaxTokens = nil },
	},
}

func deref(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

func derefInt(p *int) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}

func derefIntExact(p *int) (int, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// SamplingValue is one sampling parameter that is set, ready to be rendered.
//
// A whole-number parameter is carried in Int and a fractional one in Number.
// They are separate fields rather than one float64 because narrowing a float64
// back to an integer is implementation-defined once the value is out of range:
// the same code saturates on one architecture and wraps to a negative on
// another, and a negative token budget on a model server's command line is
// read as a value, accepted, and then refused on every request that omits the
// parameter.
type SamplingValue struct {
	Field   string
	Number  float64
	Int     int
	Integer bool
}

// Values returns the parameters that are set, in SamplingBounds order.
//
// The caller renders these; iterating the same table validation and
// sanitizing use means a parameter cannot be added to the type and quietly
// left out of the command line.
func (s Sampling) Values() []SamplingValue {
	out := make([]SamplingValue, 0, len(samplingParams))
	for _, p := range samplingParams {
		v, ok := p.get(s)
		if !ok {
			continue
		}
		val := SamplingValue{Field: p.bound.Field, Number: v, Integer: p.bound.Integer}
		if p.bound.Integer {
			// Straight from the field, never through the float64 the bounds
			// are compared in.
			val.Int, _ = p.getInt(s)
		}
		out = append(out, val)
	}
	return out
}

// SamplingBounds returns the accepted range of every sampling parameter, in
// the order the launch flags are rendered.
func SamplingBounds() []SamplingBound {
	out := make([]SamplingBound, 0, len(samplingParams))
	for _, p := range samplingParams {
		out = append(out, p.bound)
	}
	return out
}

// IsZero reports whether nothing is set, so an untouched block is left out of
// config.json entirely rather than written as a row of nulls.
func (s Sampling) IsZero() bool { return s == Sampling{} }

// Validate refuses any value the model server would reject, naming the field.
func (s Sampling) Validate() error {
	for _, p := range samplingParams {
		v, ok := p.get(s)
		if !ok {
			continue
		}
		// NaN passes every comparison below (NaN < min and NaN > max are both
		// false) and an infinity passes the ones with no ceiling, so neither is
		// caught by the bounds. JSON cannot express either today, but Sampling
		// and Validate are exported and the guarantee must not rest on that.
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("sampling %s must be a real number (got %v)", p.bound.Field, v)
		}
		if v < p.bound.Min {
			return fmt.Errorf("sampling %s must be at least %s (got %s)",
				p.bound.Field, formatBound(p.bound.Min, p.bound.Integer), formatBound(v, p.bound.Integer))
		}
		if p.bound.HasMax && v > p.bound.Max {
			return fmt.Errorf("sampling %s must be at most %s (got %s)",
				p.bound.Field, formatBound(p.bound.Max, p.bound.Integer), formatBound(v, p.bound.Integer))
		}
	}
	return nil
}

func formatBound(v float64, integer bool) string {
	if integer {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

// Sanitized returns a copy with every out-of-range value dropped, and the
// names of the fields it dropped. It is how a preference read from a file
// becomes safe to use: the value simply does not reach a launch flag, which
// is the same as never having been set.
func (s Sampling) Sanitized() (Sampling, []string) {
	out := s.Clone()
	var dropped []string
	for _, p := range samplingParams {
		v, ok := p.get(out)
		if !ok {
			continue
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < p.bound.Min || (p.bound.HasMax && v > p.bound.Max) {
			p.clear(&out)
			dropped = append(dropped, p.bound.Field)
		}
	}
	return out, dropped
}

// Clone returns a copy that shares no pointer with the original.
func (s Sampling) Clone() Sampling {
	out := Sampling{}
	if s.Temperature != nil {
		v := *s.Temperature
		out.Temperature = &v
	}
	if s.TopP != nil {
		v := *s.TopP
		out.TopP = &v
	}
	if s.TopK != nil {
		v := *s.TopK
		out.TopK = &v
	}
	if s.MinP != nil {
		v := *s.MinP
		out.MinP = &v
	}
	if s.MaxTokens != nil {
		v := *s.MaxTokens
		out.MaxTokens = &v
	}
	return out
}

// Equal compares by value, not by pointer identity — two configurations that
// name the same numbers describe the same model server.
func (s Sampling) Equal(o Sampling) bool {
	return eqF(s.Temperature, o.Temperature) &&
		eqF(s.TopP, o.TopP) &&
		eqI(s.TopK, o.TopK) &&
		eqF(s.MinP, o.MinP) &&
		eqI(s.MaxTokens, o.MaxTokens)
}

func eqF(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func eqI(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// merge lays over on top of s field by field: a parameter the override does
// not name keeps the machine-wide value, so an override that says only
// "temperature 0.2" still gets the machine's token budget.
func (s Sampling) merge(over Sampling) Sampling {
	out := s.Clone()
	o := over.Clone()
	if o.Temperature != nil {
		out.Temperature = o.Temperature
	}
	if o.TopP != nil {
		out.TopP = o.TopP
	}
	if o.TopK != nil {
		out.TopK = o.TopK
	}
	if o.MinP != nil {
		out.MinP = o.MinP
	}
	if o.MaxTokens != nil {
		out.MaxTokens = o.MaxTokens
	}
	return out
}

// EffectiveSampling returns the defaults a model server for repoID is launched
// with: the machine-wide set, with any per-model override laid over it.
//
// Override keys are matched the way the registry matches repo ids — folded to
// lower case, because HuggingFace resolves ids case-insensitively and two
// spellings are one model. Entries keep the spelling they were saved under.
func (c Config) EffectiveSampling(repoID string) Sampling {
	if len(c.ModelSampling) == 0 {
		return c.Sampling.Clone()
	}
	want := strings.ToLower(repoID)
	// Sorted, not a map range: validateSampling and sanitizeSampling both
	// guarantee at most one entry folds to any one id, and iterating in a
	// fixed order means a map that somehow held two could still not make one
	// model load at different temperatures on different starts.
	for _, k := range sortedKeys(c.ModelSampling) {
		if strings.ToLower(k) == want {
			return c.Sampling.merge(c.ModelSampling[k])
		}
	}
	return c.Sampling.Clone()
}

// MaxModelSampling caps how many per-model overrides may be held.
//
// Everything saved is written to config.json, which Load refuses above
// MaxConfigBytes — and a config.json that cannot be read sends the next start
// into its fail-closed loopback-only branch, taking the LAN endpoint with it.
// A bounded number of overrides keeps this field from being the lever for
// that, whether it is filled from the control plane or by another local
// account editing the file in shared-cache mode. Nobody has hundreds of
// models on one Mac.
const MaxModelSampling = 256

// validateSampling checks the machine-wide set and every override, naming the
// model an offending override belongs to.
func (c Config) validateSampling() error {
	if err := c.Sampling.Validate(); err != nil {
		return err
	}
	if len(c.ModelSampling) > MaxModelSampling {
		return fmt.Errorf("at most %d per-model sampling overrides are allowed, got %d",
			MaxModelSampling, len(c.ModelSampling))
	}
	seen := map[string]string{}
	for _, id := range sortedKeys(c.ModelSampling) {
		if !ValidRepoID(id) {
			return fmt.Errorf("sampling override %q is not a well-formed model id", id)
		}
		// Two spellings of one repo id are two entries in the map but one
		// model, so the effective set would depend on which the lookup reached
		// first. sanitizeSampling drops the duplicate on the file path; here,
		// where a human is waiting for an answer, say so instead.
		folded := strings.ToLower(id)
		if first, ok := seen[folded]; ok {
			return fmt.Errorf("sampling overrides %q and %q name the same model", first, id)
		}
		seen[folded] = id
		if err := c.ModelSampling[id].Validate(); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	return nil
}

// sanitizeSampling drops every sampling value the model server would reject,
// and every override that is not addressable, returning what it dropped.
//
// This is the file path, not the settings path. A configuration file can be
// hand-edited (and, in shared-cache mode, is writable by another local
// account), and refusing the whole file over one out-of-range preference
// would send the server into its fail-closed loopback-only mode — a
// machine-wide outage caused by a number that only ever wanted to be ignored.
// The strict, refusing check lives at /api/settings instead.
func (c *Config) sanitizeSampling() []string {
	var dropped []string
	sanitized, names := c.Sampling.Sanitized()
	c.Sampling = sanitized
	for _, n := range names {
		dropped = append(dropped, "sampling."+n)
	}
	if len(c.ModelSampling) == 0 {
		return dropped
	}
	kept := make(map[string]Sampling, len(c.ModelSampling))
	seen := map[string]string{} // folded id -> the spelling kept
	for _, id := range sortedKeys(c.ModelSampling) {
		if !ValidRepoID(id) {
			dropped = append(dropped, "model_sampling["+id+"]")
			continue
		}
		// Two spellings of one repo id would make the effective set depend on
		// map iteration order. Keep the first in sorted order so the outcome
		// is the same on every start.
		folded := strings.ToLower(id)
		if first, ok := seen[folded]; ok {
			dropped = append(dropped, "model_sampling["+id+"] (duplicate of "+first+")")
			continue
		}
		if len(kept) >= MaxModelSampling {
			dropped = append(dropped, "model_sampling["+id+"] (beyond the "+
				strconv.Itoa(MaxModelSampling)+"-override ceiling)")
			continue
		}
		seen[folded] = id
		s, names := c.ModelSampling[id].Sanitized()
		for _, n := range names {
			dropped = append(dropped, "model_sampling["+id+"]."+n)
		}
		kept[id] = s
	}
	if len(kept) == 0 {
		kept = nil
	}
	c.ModelSampling = kept
	return dropped
}

func sortedKeys(m map[string]Sampling) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
