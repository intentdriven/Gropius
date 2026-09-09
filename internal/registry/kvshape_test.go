package registry

import (
	"os"
	"path/filepath"
	"testing"
)

// The cache cost per token is read off the same config.json every other
// question about a model is answered from. These are the four shapes the
// 2026-09-06 campaign measured, plus the fallbacks a configuration that says
// less falls to. The expected figures are the ones the campaign recorded in
// its nominal-caps evidence.
func TestKVBytesPerTokenReadsTheConfigurationsShape(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   int64
	}{
		{
			// Qwen3-Coder-Next: 12 full-attention layers of 48, declared as
			// an interval.
			name: "an attention interval names the full-attention layers",
			config: `{"model_type":"qwen3_next","num_hidden_layers":48,"full_attention_interval":4,
			          "num_key_value_heads":2,"head_dim":256}`,
			want: 24576,
		},
		{
			// The same layout declared as a list, which is how transformers
			// spells it.
			name: "a layer-type list names them one by one",
			config: `{"model_type":"qwen3_next","num_hidden_layers":4,
			          "layer_types":["linear_attention","linear_attention","linear_attention","full_attention"],
			          "num_key_value_heads":2,"head_dim":256}`,
			want: 2048,
		},
		{
			// Nemotron-3.5-Lightning: 6 attention layers of 52, declared as a
			// pattern string where * is attention, M a Mamba block and - an
			// MLP.
			name: "a hybrid pattern names them by letter",
			config: `{"model_type":"nemotron_h","num_hidden_layers":52,
			          "hybrid_override_pattern":"M-M-M-*-M-M-M-*-M-M-M-*-M-M-M-*-M-M-M-*-M-M-M-*-M-M-M-M",
			          "num_key_value_heads":2,"head_dim":128}`,
			want: 6144,
		},
		{
			// GLM-4.7-Flash: multi-head latent attention on all 47 layers,
			// which caches one latent per layer per token rather than keys
			// and values per head.
			name: "a latent cache is charged as a latent cache",
			config: `{"model_type":"glm4_moe_lite","num_hidden_layers":47,"kv_lora_rank":512,
			          "qk_rope_head_dim":64,"num_key_value_heads":20,"v_head_dim":256}`,
			want: 54144,
		},
		{
			// Qwen3.8-27B: 16 full-attention layers of 64.
			name: "an interval of four over sixty-four layers",
			config: `{"model_type":"qwen3_5_text","num_hidden_layers":64,"full_attention_interval":4,
			          "num_key_value_heads":4,"head_dim":256}`,
			want: 65536,
		},
		{
			// The conservative floor: a configuration that says nothing about
			// hybrid layers is charged as though every layer attends over the
			// whole prompt, which is the most any model of that shape costs.
			name:   "no hybrid key at all charges every layer",
			config: `{"model_type":"llama","num_hidden_layers":4,"num_key_value_heads":2,"head_dim":128}`,
			want:   4 * 2 * 128 * 2 * 2,
		},
		{
			name:   "no head dimension takes it from the hidden size",
			config: `{"model_type":"llama","num_hidden_layers":2,"num_attention_heads":8,"hidden_size":1024}`,
			want:   2 * 8 * 128 * 2 * 2,
		},
		{
			name:   "no key-value head count means every attention head keeps one",
			config: `{"model_type":"llama","num_hidden_layers":2,"num_attention_heads":8,"head_dim":64}`,
			want:   2 * 8 * 64 * 2 * 2,
		},
		{
			name: "a composite configuration is read at its text level",
			config: `{"model_type":"qwen2_vl","text_config":{"num_hidden_layers":2,
			          "num_key_value_heads":2,"head_dim":128}}`,
			want: 2 * 2 * 128 * 2 * 2,
		},
		{
			name:   "a configuration with no layers says nothing",
			config: `{"model_type":"test"}`,
			want:   0,
		},
		{
			name:   "an absurd claim says nothing rather than an absurd charge",
			config: `{"model_type":"test","num_hidden_layers":1e9,"num_key_value_heads":1e9,"head_dim":1e9}`,
			want:   0,
		},
		{
			name:   "a figure that is not a number says nothing",
			config: `{"model_type":"test","num_hidden_layers":"48","num_key_value_heads":2,"head_dim":256}`,
			want:   0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(c.config), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := ReadModelFacts(dir).KVBytesPerToken; got != c.want {
				t.Errorf("KVBytesPerToken = %d, want %d", got, c.want)
			}
		})
	}
}

// An unreadable directory is charged nothing, which is what makes the pool
// fall back to the flat charge rather than admit a model on a cache cost of
// zero.
func TestKVBytesPerTokenOfAnUnreadableDirectoryIsNothing(t *testing.T) {
	if got := ReadModelFacts(filepath.Join(t.TempDir(), "nothing-here")); got != (ModelFacts{}) {
		t.Errorf("ReadModelFacts = %+v, want nothing", got)
	}
}

// The figure has to reach the pool the way the context length does: recorded
// on the model the moment the directory is scanned, from the one decode of
// config.json the scan already makes.
func TestRescanRecordsTheCacheCost(t *testing.T) {
	r, dir := newTestRegistry(t)
	writeModelDirWithConfig(t, dir, "org", "hybrid",
		`{"model_type":"qwen3_next","max_position_embeddings":262144,"num_hidden_layers":48,
		  "full_attention_interval":4,"num_key_value_heads":2,"head_dim":256}`, 1024)
	if err := r.Rescan(dir); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	m, err := r.Get("org/hybrid")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.KVBytesPerToken != 24576 {
		t.Errorf("KVBytesPerToken = %d, want 24576", m.KVBytesPerToken)
	}
	if m.ContextLength != 262144 {
		t.Errorf("ContextLength = %d, want 262144", m.ContextLength)
	}
}

// A model directory is, in shared-cache mode, a file another local account can
// write, and the cache figure decides how much of this Mac's memory a model is
// charged. A claim past what any model could mean yields nothing at the one
// place the figure is worked out — not at the one place it is read back —
// because a figure the scan accepts is charged for the whole of this session
// and only falls to nothing at the next restart, which is a difference an
// operator would see as the machine quietly changing its mind.
func TestAnImplausibleCacheFigureNeverEntersTheRegistry(t *testing.T) {
	const hostile = `{"model_type":"test","num_hidden_layers":1024,"num_key_value_heads":1024,
	                  "head_dim":65536,"max_position_embeddings":262144}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(hostile), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadModelFacts(dir).KVBytesPerToken; got != 0 {
		t.Errorf("KVBytesPerToken = %d, want 0: %d is past the plausible ceiling %d",
			got, got, int64(MaxKVBytesPerToken))
	}

	r, root := newTestRegistry(t)
	writeModelDirWithConfig(t, root, "org", "hostile", hostile, 1024)
	if err := r.Rescan(root); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	m, err := r.Get("org/hostile")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.KVBytesPerToken != 0 {
		t.Errorf("the scan recorded %d, want 0 — the figure is charged from the moment it is scanned",
			m.KVBytesPerToken)
	}
}

// An attention interval wider than the model is deep would count one layer in
// a model where every layer attends: a 64-fold under-charge, and one that is
// charged rather than refused. An interval that cannot be one falls back to
// the floor every configuration that says nothing gets.
func TestAnAttentionIntervalWiderThanTheModelChargesEveryLayer(t *testing.T) {
	dir := t.TempDir()
	config := `{"model_type":"test","num_hidden_layers":64,"full_attention_interval":1000,
	            "num_key_value_heads":4,"head_dim":256}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := ReadModelFacts(dir).KVBytesPerToken, int64(64*4*256*2*2); got != want {
		t.Errorf("KVBytesPerToken = %d, want %d — every layer, as for a configuration that declares no layout", got, want)
	}
}
