package stats

import (
	"encoding/json"
	"testing"
)

func TestMeasureWidest(t *testing.T) {
	s := Summary{
		At: 1767009600, Day: "2026-01-10", TZOffsetMin: -720,
		Model:    "mlx-community/DeepSeek-R1-Distill-Qwen-32B-8bit-mlx-experimental",
		Requests: 999999, PromptTokens: 999999999, CompletionTokens: 999999999,
		DurationMSTotal: 999999999, QueueWaitMSTotal: 999999999, LoadWaitMSTotal: 999999999,
		FirstTokenMSTotal: 999999999, FirstTokenRequests: 999999,
		Loads: 999999, FailedLoads: 999999,
		ByClass: map[Class]int64{}, Removals: map[string]int64{},
	}
	for _, c := range OutcomeClasses() {
		s.ByClass[c] = 999999
	}
	for _, r := range RemovalReasons() {
		s.Removals[r] = 999999
	}
	b, _ := json.Marshal(summaryLine{V: SchemaVersion, Kind: KindSummary, Summary: s})
	t.Logf("widest = %d bytes; model id %d chars", len(b), len(s.Model))
	t.Logf("%s", b)
}
