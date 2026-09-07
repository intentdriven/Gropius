package stats

import (
	"testing"
	"time"
)

// BenchmarkLatestAtTheCap is the measurement adr-2609061610107154 names as the
// condition for reconsidering a database: how long one sequential pass over a
// store at the default size cap takes, aggregating the way the usage dashboard
// (itd-2609061521159233) will.
//
// It is a benchmark rather than a test because it writes and reads the whole
// default cap — two hundred megabytes — which is far too much to do on every
// `make test`. Run it deliberately:
//
//	go test -run XXX -bench LatestAtTheCap -benchtime 1x ./internal/stats/
//
// The figure it produced when the store shipped is recorded in
// .abcd/work/DECISIONS.md against that ADR. If a later change to Latest — the
// dashboard's own, most likely — pushes it past a few seconds, the ADR's
// condition is met and the decision not to use a database is due to be
// reconsidered in a new decision record.
func BenchmarkLatestAtTheCap(b *testing.B) {
	if testing.Short() {
		b.Skip("fills the default size cap; run it deliberately")
	}
	dir := b.TempDir()
	s := NewStore(dir, StoreOptions{
		Months: 1200, MaxBytes: defaultMaxBytes, RotateBytes: defaultRotateBytes,
		QueueSize: 8192, PruneEvery: time.Hour,
	})
	b.Cleanup(func() { s.Close() })
	if err := s.SetEnabled(true); err != nil {
		b.Fatal(err)
	}

	// Filled the way it fills in life: one record per request, at a rate the
	// writer keeps up with, until the store is at its cap.
	at := time.Now().UTC().Unix()
	records := 0
	for {
		if err := s.AppendRequest(Record{
			Model: "mlx-community/Qwen3-8B-4bit", At: at + int64(records/10), Class: ClassOK,
			Streamed: true, PromptTokens: 1234, CompletionTokens: 567,
			FirstTokenMS: 210, DurationMS: 4200, QueueWaitMS: 12,
		}); err != nil {
			b.Fatal(err)
		}
		records++
		if records%20000 == 0 {
			if err := s.Flush(); err != nil {
				b.Fatal(err)
			}
			if s.Status().Bytes >= defaultMaxBytes-defaultRotateBytes {
				break
			}
		}
	}
	if err := s.Flush(); err != nil {
		b.Fatal(err)
	}
	held := s.Status()
	b.Logf("store: %d bytes in %d files, %d records offered, %d dropped",
		held.Bytes, held.Files, records, held.Dropped)

	// What the dashboard does: one pass, aggregating by model and by day.
	type key struct {
		model string
		day   int64
	}
	b.ResetTimer()
	for b.Loop() {
		agg := map[key]int64{}
		read := 0
		if err := s.Latest(0, func(l Line) bool {
			read++
			if l.Kind == KindRequest {
				agg[key{l.Request.Model, l.At / 86400}] += int64(l.Request.CompletionTokens)
			}
			return true
		}); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(read), "records")
	}
}
