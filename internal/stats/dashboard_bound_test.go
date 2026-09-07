//go:build !race

package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The dashboard's promise is that the panel stays responsive on a full store,
// so the bound is checked rather than asserted in prose: a store at the
// default 200 MB size cap is aggregated, and the pass has to finish inside
// aggregateBound.
//
// It is excluded from the race build, because the race detector multiplies
// wall-clock by something that varies with the machine and measures nothing
// about this code; `make test` runs with -race, so the bound is checked by the
// plain `go test ./...` that continuous integration also runs. It skips under
// -short, because it writes and reads two hundred megabytes.
//
// The bound is deliberately loose against the measurement it was set from: a
// store of 881,521 records in 209,723,172 bytes aggregates — read, grouped by
// day, model and hour, and reduced to percentiles — in 1.4 s on an Apple M4
// Max, which is the figure the intent's own two-second claim rests on. A
// shared build machine is slower than a desktop one, and a bound that failed
// on a busy runner would be the half-maintained statistics the research note
// warns cost trust. What this catches is a change that makes the pass an order
// of magnitude slower — a second decode, a read that is no longer bounded —
// not a machine having a bad afternoon.
const aggregateBound = 10 * time.Second

func TestAStoreAtTheSizeCapAggregatesWithinTheBound(t *testing.T) {
	if testing.Short() {
		t.Skip("fills the default size cap; run it deliberately")
	}
	dir := t.TempDir()
	end := time.Now().UTC().Truncate(time.Hour)
	records := writeCapSizedStore(t, dir, end)

	s := NewStore(dir, StoreOptions{Months: 1200, MaxBytes: defaultMaxBytes})
	t.Cleanup(func() { s.Close() })

	started := time.Now()
	h, err := Aggregate(t.Context(), s, end.AddDate(0, 0, -30), end, time.Local)
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("aggregated %d records of %d written in %v", h.Records, records, took)

	if h.Records < records {
		t.Errorf("the pass read %d of the %d records written", h.Records, records)
	}
	if h.Truncated {
		t.Errorf("a store at the default cap holds %d records, which is past the bound of %d",
			h.Records, MaxHistoryRecords)
	}
	if took > aggregateBound {
		t.Errorf("aggregating a store at the default size cap took %v, want under %v", took, aggregateBound)
	}
}

// writeCapSizedStore fills a directory with the store's own files until they
// come to the default size cap, and returns how many records it wrote.
//
// The files are written directly rather than through the store's writer: what
// is being measured is the reading, and driving a million records through a
// buffered channel to measure a read would spend most of the test on the half
// that is not under test.
func writeCapSizedStore(t *testing.T, dir string, end time.Time) int {
	t.Helper()
	models := []string{
		"mlx-community/Qwen3-8B-4bit",
		"mlx-community/Llama-3.2-3B-Instruct-4bit",
		"mlx-community/Mistral-7B-Instruct-v0.3-8bit",
	}
	written, num, total := 0, 0, int64(0)
	for total < defaultMaxBytes {
		num++
		var buf []byte
		for int64(len(buf)) < defaultRotateBytes {
			// Spread over the thirty days the default range covers, so the
			// day table has its rows and the range filter does real work.
			at := end.Add(-time.Duration(written%(30*24*60*60)) * time.Second).Unix()
			var line []byte
			var err error
			switch written % 500 {
			case 0:
				line, err = json.Marshal(eventLine{V: SchemaVersion, Kind: KindLoad, Event: Event{
					At: at, Model: models[written%len(models)], Kind: EventLoad, DurationMS: 4200,
				}})
			case 1:
				line, err = json.Marshal(eventLine{V: SchemaVersion, Kind: KindRemoved, Event: Event{
					At: at, Model: models[written%len(models)], Kind: EventRemoved, Reason: ReasonEvicted,
				}})
			default:
				line, err = json.Marshal(requestLine{V: SchemaVersion, Kind: KindRequest, Record: Record{
					Model: models[written%len(models)], At: at, Class: ClassOK, Streamed: true,
					PromptTokens: 1234 + written%97, CompletionTokens: 567 + written%53,
					FirstTokenMS: int64(120 + written%900), DurationMS: int64(2000 + written%3000),
					QueueWaitMS: int64(written % 40),
				}})
			}
			if err != nil {
				t.Fatal(err)
			}
			buf = append(append(buf, line...), '\n')
			written++
		}
		name := fmt.Sprintf("stats-%s-%03d.jsonl", end.Format("20060102"), num)
		if err := os.WriteFile(filepath.Join(dir, name), buf, 0o600); err != nil {
			t.Fatal(err)
		}
		total += int64(len(buf))
	}
	t.Logf("fixture store: %d bytes in %d files, %d records", total, num, written)
	return written
}
