package capability

import "testing"

// The four models the 2026-09-06 context-window campaign measured on a 128 GB
// Mac (research note 2026-09-06-context-windows and the evidence beside it).
// Every figure here is a reading from that campaign or from the model's own
// config.json, and this table is what the charge is held to.
//
// Bytes are exact where the evidence is exact (the weights, the configuration
// arithmetic, the verified prompt) and gigabytes where the reading is `top`'s
// MEM column, which rounds to whole gigabytes above 10 GB. Those gigabytes are
// decimal, which is the unit the note states a model's weights in (44.9 GB for
// 44,855,964,256 bytes).
type measuredModel struct {
	name string
	// weights is the model directory's size on disk, in bytes.
	weights int64
	// kv is the KV bytes per token its config.json implies at f16 — the floor,
	// not an estimate: every model measured above it — and latent says whether
	// that figure is a compressed latent cache rather than keys and values per
	// head, which is what decides the safety factor.
	kv     int64
	latent bool
	// window is the largest prompt verified through the gateway, in tokens.
	window int64
	// slope is the measured growth per thousand tokens, in bytes, and
	// intercept the working set prefill needs at any size.
	slope, intercept int64
	// peak is the process footprint at the verified window.
	peak int64
}

// measuredGB is the campaign's gigabyte: decimal, as the note states it.
const measuredGB = 1_000_000_000

var measured = []measuredModel{
	{
		name: "Qwen3-Coder-Next 4bit", weights: 44855964256, kv: 24576,
		window: 221743, slope: 110 * measuredGB / 1000, intercept: 10 * measuredGB / 10, peak: 68 * measuredGB,
	},
	{
		name: "Nemotron-3.5-Lightning 30B-A3B 4bit", weights: 17792583132, kv: 6144,
		window: 253106, slope: 11 * measuredGB / 1000, intercept: 34 * measuredGB / 10, peak: 28 * measuredGB,
	},
	{
		name: "GLM-4.7-Flash 8bit", weights: 31841402692, kv: 54144, latent: true,
		window: 81100, slope: 337 * measuredGB / 1000, intercept: 15 * measuredGB / 10, peak: 59 * measuredGB,
	},
	{
		name: "Qwen3.8-27B 8bit", weights: 29531519752, kv: 65536,
		window: 91673, slope: 191 * measuredGB / 1000, intercept: 34 * measuredGB / 10, peak: 49 * measuredGB,
	},
}

// The charge a model is admitted on has to cover the memory it was measured
// taking at the window it was measured serving. One sequence, because that is
// what the campaign measured: one long request at a time.
//
// The envelope is two-sided. Under the peak is an over-admission — the fault
// this charge exists to fix — and far above it is a budget that refuses loads
// this Mac could serve, so the charge must also stay within twice the reading.
func TestChargeCoversWhatEachMeasuredModelTook(t *testing.T) {
	for _, m := range measured {
		got := LoadCostOf(Load{
			DiskBytes:        m.weights,
			KVChargePerToken: m.shape().ChargedBytesPerToken(),
			Window:           m.window,
			Sequences:        1,
		})
		if got < m.peak {
			t.Errorf("%s: charge %d under-charges the %d it was measured taking at %d tokens",
				m.name, got, m.peak, m.window)
		}
		if got > 2*m.peak {
			t.Errorf("%s: charge %d is more than twice the %d it was measured taking",
				m.name, got, m.peak)
		}
	}
}

// The flat 1.2x is what this replaces, and the reason it had to be replaced is
// that it under-charges a model with an expensive cache at the window that
// model actually serves. The 27B is the one the record names.
func TestTheFlatChargeUnderChargesTheDenseModelAtItsWindow(t *testing.T) {
	m := measured[3]
	if got := LoadCost(m.weights); got >= m.peak {
		t.Fatalf("the flat charge for %s is %d, which already covers the measured %d — "+
			"this test guards the reason the measured charge exists", m.name, got, m.peak)
	}
}

// The safety factor is what makes a configuration figure a usable floor. The
// record's finding is that every measured slope is two to seven times the
// arithmetic its configuration implies, so the factor is that range taken at
// its top; a model whose measured slope exceeded it would show it wrong.
func TestTheSafetyFactorCoversEveryMeasuredSlope(t *testing.T) {
	for _, m := range measured {
		charged := m.shape().ChargedBytesPerToken() * 1000
		if charged < m.slope {
			t.Errorf("%s: the charged slope %d B per 1K tokens is under the measured %d",
				m.name, charged, m.slope)
		}
	}
}

// Two factors, because the four models divide cleanly in two. The three that
// cache keys and values per head measured 2.0, 3.1 and 4.8 times what their
// configurations imply; the one that caches a compressed latent measured 6.7.
// One factor for both would charge the first three half again as much as their
// own evidence supports.
func TestTheSafetyFactorIsTheOneItsCacheKindEarned(t *testing.T) {
	gqa := KVShape{FullAttentionLayers: 1, KVHeads: 1, HeadDim: 1}
	if got, want := gqa.ChargedBytesPerToken(), 5*gqa.BytesPerToken(); got != want {
		t.Errorf("a key-and-value cache is charged %d, want %d (five times its configuration)", got, want)
	}
	latent := KVShape{FullAttentionLayers: 1, LatentDim: 1}
	if got, want := latent.ChargedBytesPerToken(), 7*latent.BytesPerToken(); got != want {
		t.Errorf("a latent cache is charged %d, want %d (seven times its configuration)", got, want)
	}
	// Nothing to work from stays nothing: a factor times zero must not become
	// a cache cost of its own.
	if got := (KVShape{}).ChargedBytesPerToken(); got != 0 {
		t.Errorf("an unreadable shape is charged %d, want 0", got)
	}
}

// shape is the model's configuration as the charge reads it. Only the cache
// kind and the resulting per-token figure matter here, so the layers are
// folded into one and the figure carried whole.
func (m measuredModel) shape() KVShape {
	if m.latent {
		return KVShape{FullAttentionLayers: 1, LatentDim: m.kv / 2}
	}
	return KVShape{FullAttentionLayers: 1, KVHeads: 1, HeadDim: m.kv / 4}
}

// The weights and their fifth stand in for the working set prefill needs
// whatever the prompt's size — the intercept of each measured fit. It covers
// every model measured, which is what lets the charge keep one headroom term
// rather than carry a second constant nobody can read off a model.
func TestTheHeadroomCoversEveryMeasuredIntercept(t *testing.T) {
	for _, m := range measured {
		if headroom := m.weights / 5; headroom < m.intercept {
			t.Errorf("%s: headroom %d is under the measured intercept %d", m.name, headroom, m.intercept)
		}
	}
}

// A model whose configuration cannot be read is charged what it has always
// been charged. There is nothing to work a cache cost out from, and refusing
// to load it — or guessing — would be worse than the flat figure.
func TestAModelWithNoConfigurationKeepsTheFlatCharge(t *testing.T) {
	const size = 10 * gb
	cases := []struct {
		name string
		load Load
	}{
		{"no cache figure", Load{DiskBytes: size, Window: 100000, Sequences: 4}},
		{"no window", Load{DiskBytes: size, KVChargePerToken: 24576, Sequences: 4}},
		{"no sequences", Load{DiskBytes: size, KVChargePerToken: 24576, Window: 100000}},
	}
	for _, c := range cases {
		if got, want := LoadCostOf(c.load), LoadCost(size); got != want {
			t.Errorf("%s: LoadCostOf = %d, want the flat charge %d", c.name, got, want)
		}
	}
}

// Each sequence the pool admits holds its own cache, so the cache term is
// charged per sequence — the campaign measured a second concurrent 64K prompt
// costing more than the first one's own growth, not less.
func TestEachAdmittedSequenceIsChargedItsOwnCache(t *testing.T) {
	l := Load{DiskBytes: 10 * gb, KVChargePerToken: 24576, Window: 100000, Sequences: 1}
	one := LoadCostOf(l)
	l.Sequences = 4
	four := LoadCostOf(l)
	cache := one - LoadCost(10*gb)
	if want := one + 3*cache; four != want {
		t.Errorf("four sequences charge %d, want %d (one sequence plus three more caches)", four, want)
	}
}

// No ceiling. A charge is what the model will cost, however large, because a
// charge held down to the budget is a figure the machine does not support: the
// pool would believe a model cost whatever the budget happened to be and admit
// the next one against it. A model that does not fit is refused by the pool,
// which says what would fit — the charge itself never lies about the cost.
func TestNothingHoldsAChargeDownToTheBudget(t *testing.T) {
	l := Load{DiskBytes: 10 * gb, KVChargePerToken: 65536, Window: 262144, Sequences: 4}
	want := LoadCost(10*gb) + 4*65536*262144
	if got := LoadCostOf(l); got != want {
		t.Errorf("charge = %d, want the honest %d", got, want)
	}
}

// The cache arithmetic is the one the configurations declare: full-attention
// layers times KV heads times head dimension, K and V, two bytes each. The
// four measured models are the sample, and the latent form is what a
// multi-head-latent model caches instead.
func TestKVShapeMatchesTheConfigurationArithmetic(t *testing.T) {
	cases := []struct {
		name  string
		shape KVShape
		want  int64
	}{
		{"Qwen3-Coder-Next: 12 full-attention layers of 48", KVShape{FullAttentionLayers: 12, KVHeads: 2, HeadDim: 256}, 24576},
		{"Nemotron: 6 full-attention layers of 52", KVShape{FullAttentionLayers: 6, KVHeads: 2, HeadDim: 128}, 6144},
		{"Qwen3.8-27B: 16 full-attention layers of 64", KVShape{FullAttentionLayers: 16, KVHeads: 4, HeadDim: 256}, 65536},
		{"GLM-4.7-Flash: 47 latent layers", KVShape{FullAttentionLayers: 47, KVHeads: 20, HeadDim: 256, LatentDim: 576}, 54144},
		{"nothing to work from", KVShape{}, 0},
		{"layers but no heads", KVShape{FullAttentionLayers: 12}, 0},
	}
	for _, c := range cases {
		if got := c.shape.BytesPerToken(); got != c.want {
			t.Errorf("%s: BytesPerToken = %d, want %d", c.name, got, c.want)
		}
	}
}
