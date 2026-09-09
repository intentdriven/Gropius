// Package capability assesses whether a model will fit on this machine — both
// on disk to download and within the RAM budget to actually run.
package capability

import "math"

// Machine describes the resources available to local models.
type Machine struct {
	TotalRAM int64 `json:"total_ram"` // installed physical RAM, bytes
	// RAMBudget is the memory Gropius will let loaded models use. It is spelled
	// the way the control plane's own machine object spells it: both reach the
	// same panel, and one number under two names is what this figure was
	// centralized to prevent.
	RAMBudget int64 `json:"budget"`
	FreeDisk  int64 `json:"free_disk"` // free space on the models volume, bytes
}

// diskHeadroom is left free so a download never fills the disk to the brim.
const diskHeadroom = 2 << 30 // 2 GiB

// LoadCost estimates the memory a model of the given size on disk occupies once
// loaded, when nothing is known about the cache it will build: the weights plus
// 20% for the working set a running model needs.
//
// This is the charge for a model whose configuration cannot be read — a model
// in the search results that is not downloaded, a directory whose config.json
// is unreadable. A model Gropius holds is charged by LoadCostOf, which adds
// what that model's own configuration says its attention cache costs.
func LoadCost(diskBytes int64) int64 {
	return diskBytes + diskBytes/5 // 1.2x
}

// KVSafetyFactor multiplies the cache figure a model's configuration implies.
//
// The arithmetic below is a floor, not an estimate: the 2026-09-06
// context-window campaign measured every one of four models holding two to
// seven times what its configuration implies per token (the linear-attention
// states, the activations kept for the batch, allocator granularity). The
// factor is that measured range taken at its top. A model measured holding
// more than seven times its configuration figure would show it wrong.
const KVSafetyFactor = 7

// kvElementBytes is the width of one cached element. The caches are f16
// whatever the weights are quantized to.
const kvElementBytes = 2

// Bounds on what a configuration may claim. A model directory is, in
// shared-cache mode, written by another local account, and these figures
// multiply: an absurd claim must yield no figure at all rather than a charge
// that overflows or refuses every load. Every real model is far inside them.
const (
	maxAttentionLayers = 1024
	maxKVHeads         = 1024
	maxHeadDim         = 1 << 16
)

// KVShape is what a model's own configuration says its attention cache costs
// per token: the layers that attend over the whole prompt, and the size of one
// layer's entry. Layers that hold a fixed-size state per sequence instead —
// linear attention, Mamba — are not counted, which is why the count is of
// full-attention layers rather than of layers.
type KVShape struct {
	// FullAttentionLayers is how many layers keep a per-token cache.
	FullAttentionLayers int64
	// KVHeads and HeadDim size one layer's key and value entries.
	KVHeads, HeadDim int64
	// LatentDim is the width of one cached entry on a model that caches a
	// compressed latent instead of keys and values (multi-head latent
	// attention). When it is set it replaces the heads-and-dimension
	// arithmetic, because such a model caches one vector of this width per
	// layer per token rather than a key and a value per head.
	LatentDim int64
}

// BytesPerToken is the cache one token costs at f16, or 0 when the
// configuration does not say enough to work it out.
func (s KVShape) BytesPerToken() int64 {
	if s.FullAttentionLayers <= 0 || s.FullAttentionLayers > maxAttentionLayers {
		return 0
	}
	if s.LatentDim > 0 {
		if s.LatentDim > maxHeadDim {
			return 0
		}
		return s.FullAttentionLayers * s.LatentDim * kvElementBytes
	}
	if s.KVHeads <= 0 || s.KVHeads > maxKVHeads || s.HeadDim <= 0 || s.HeadDim > maxHeadDim {
		return 0
	}
	// Key and value, hence the 2.
	return s.FullAttentionLayers * s.KVHeads * s.HeadDim * 2 * kvElementBytes
}

// Load is what one model costs this machine to hold, as far as the machine is
// concerned: its weights, and the caches the sequences it serves will build.
type Load struct {
	// DiskBytes is the model directory's size.
	DiskBytes int64
	// KVBytesPerToken is the floor its configuration implies (KVShape).
	// Zero means the configuration could not be read.
	KVBytesPerToken int64
	// Window is the context the pool intends to serve — the model's own
	// declared maximum, since nothing between a client and mlx-lm caps it.
	Window int64
	// Sequences is how many of those windows may be in flight at once: the
	// decode concurrency each model server is launched with. Each sequence
	// holds its own cache.
	Sequences int64
	// Budget is the memory budget the charge will be measured against, and
	// the ceiling on it. Zero means no ceiling.
	Budget int64
}

// LoadCostOf is the memory a loaded model is charged against the budget: its
// weights and their headroom, plus the attention cache the window it serves
// costs, per sequence it admits.
//
// This is the one home of that charge. It is what the process pool charges a
// resident model, what the app measures a pinned set against, and — through a
// test in internal/ui — what the control panel's own copy is held to. It lives
// here because internal/runtime reads this package and not the other way
// round, so a second copy in the pool is what let the filter show a model the
// pool would refuse.
//
// The shape comes from the 2026-09-06 context-window campaign (research note
// 2026-09-06-context-windows): a model's footprint grows linearly with the
// prompt, at a rate that is a property of its architecture and varies
// thirtyfold across four models, from an intercept that is the working set
// prefill needs at any size. The headroom covers that intercept for every
// model measured; the cache term covers the growth.
//
// Two ceilings bound it. A configuration that says nothing about its cache
// yields the flat charge, because a guess would be worse than the figure that
// has always been used. And no single model is charged more than the whole
// budget: the charge decides what may share this Mac's memory, and a model
// that fills the budget by itself is one that loads alone, not one that can
// never load — but never below the flat charge, so a model whose weights do
// not fit is refused as it always was.
func LoadCostOf(l Load) int64 {
	flat := LoadCost(l.DiskBytes)
	cache := mulSaturating(mulSaturating(mulSaturating(l.KVBytesPerToken, KVSafetyFactor), l.Window), l.Sequences)
	if cache <= 0 {
		return flat
	}
	charge := addSaturating(flat, cache)
	if l.Budget > 0 && charge > l.Budget {
		if flat > l.Budget {
			return flat
		}
		return l.Budget
	}
	return charge
}

// mulSaturating multiplies without wrapping: a product that will not fit is
// the largest figure there is, which refuses a load rather than admitting one
// on a negative charge.
func mulSaturating(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// addSaturating adds without wrapping, for the same reason.
func addSaturating(a, b int64) int64 {
	if a > 0 && b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

// Fits reports whether a model of downloadSize bytes can both be stored and run
// on this machine. A zero or negative size means "unknown" and always fits: we
// would rather show a model we cannot measure than hide it.
func (m Machine) Fits(downloadSize int64) bool {
	return m.Reason(downloadSize) == ""
}

// Reason returns a short explanation of why a model does not fit, or "" if it
// does (or its size is unknown).
func (m Machine) Reason(downloadSize int64) string {
	if downloadSize <= 0 {
		return ""
	}
	if m.FreeDisk > 0 && downloadSize+diskHeadroom > m.FreeDisk {
		return "not enough free disk space"
	}
	// The flat charge, not LoadCostOf: a model in the search results is not on
	// this Mac, so there is no configuration to read a cache cost from. The
	// two still agree on the question this filter asks — whether the model can
	// load at all — because no single model is charged more than the whole
	// budget, and the floor under that ceiling is this same flat figure. What
	// the filter cannot say is which models will fit *together*, and it has
	// never claimed to.
	if m.RAMBudget > 0 && LoadCost(downloadSize) > m.RAMBudget {
		return "too large for this Mac's memory"
	}
	return ""
}
