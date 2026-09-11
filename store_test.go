package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, names ...string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	var ts []TargetCfg
	for _, n := range names {
		ts = append(ts, TargetCfg{Name: n, Type: "icmp", Host: "127.0.0.1", dir: n})
	}
	s, err := NewStore(dir, ts)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func samples(n int, base float64) []float64 {
	ms := make([]float64, n)
	for i := range ms {
		ms[i] = base + float64(i)
	}
	return ms
}

func writeDay(t *testing.T, dir, target, day string, rounds []Round) string {
	t.Helper()
	path := filepath.Join(dir, target, day+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rounds {
		line, _ := json.Marshal(r)
		f.Write(append(line, '\n'))
	}
	return path
}

// The old count cap (1440) held only 6h at pace=fast; the window must be 24h
// regardless of interval.
func TestRingHoldsFullDayAtFastPace(t *testing.T) {
	var ring []Round
	const step = 15
	n := 2 * ringSpan / step // two days of rounds
	for i := 0; i <= n; i++ {
		ring = ringAppend(ring, Round{T: int64(i * step), S: 30, R: 30})
	}
	newest := ring[len(ring)-1].T
	if span := newest - ring[0].T; span != ringSpan {
		t.Fatalf("ring spans %ds, want %ds", span, ringSpan)
	}
	if want := ringSpan/step + 1; len(ring) != want {
		t.Fatalf("ring holds %d rounds, want %d", len(ring), want)
	}
}

func TestRingAppendKeepsOrder(t *testing.T) {
	var ring []Round
	for _, ts := range []int64{100, 200, 300, 250, 50} {
		ring = ringAppend(ring, Round{T: ts})
	}
	for i := 1; i < len(ring); i++ {
		if ring[i-1].T > ring[i].T {
			t.Fatalf("ring out of order: %v", ring)
		}
	}
	if len(ring) != 5 {
		t.Fatalf("lost a round: %v", ring)
	}
}

func TestRingAppendReleasesDroppedSamples(t *testing.T) {
	ring := make([]Round, 0, 8)
	ring = ringAppend(ring, Round{T: 0, MS: samples(20, 1)})
	backing := ring[:1]
	ring = ringAppend(ring, Round{T: ringSpan + 10, MS: samples(20, 1)})
	if len(ring) != 1 || ring[0].T != ringSpan+10 {
		t.Fatalf("old round not dropped: %+v", ring)
	}
	if backing[0].MS != nil {
		t.Fatal("dropped slot still references its samples")
	}
}

func TestReplayRebuildsSortedWindow(t *testing.T) {
	s, dir := newTestStore(t, "A")
	now := time.Now().Unix()
	day := time.Now().Format("2006-01-02")
	writeDay(t, dir, "A", day, []Round{
		{T: now - 120, S: 20, R: 20},
		{T: now - 60, S: 20, R: 20},
		{T: now - 90, S: 20, R: 20}, // reload overlap left one out of place
	})
	yday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	writeDay(t, dir, "A", yday, []Round{{T: now - ringSpan - 600, S: 20, R: 20}})

	s.Replay()
	got := s.Recent("A", 0)
	if len(got) != 3 {
		t.Fatalf("want 3 rounds inside 24h, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].T > got[i].T {
			t.Fatalf("replayed ring not sorted: %v", got)
		}
	}
}

func TestAppendAfterRemoveDoesNotResurrect(t *testing.T) {
	s, _ := newTestStore(t, "A", "B")
	s.RemoveTarget("B")
	if err := s.Append("B", Round{T: time.Now().Unix(), S: 20}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.rings["B"]; ok {
		t.Fatal("late round from a removed target recreated its ring")
	}
	for _, n := range s.Names() {
		if n == "B" {
			t.Fatal("removed target back in Names")
		}
	}
}

func TestReadRangeRingFilesAndClip(t *testing.T) {
	s, _ := newTestStore(t, "A")
	now := time.Now().Unix()
	for i := 5; i >= 1; i-- {
		if err := s.Append("A", Round{T: now - int64(i*60), S: 20, R: 20, MS: samples(20, 10)}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	// inside the ring
	if got := s.ReadRange(ctx, "A", now-150, now); len(got) != 2 {
		t.Fatalf("ring read: want 2, got %d", len(got))
	}
	// older than the ring start: served from the day files, then clipped
	if got := s.ReadRange(ctx, "A", now-3*24*3600, now-200); len(got) != 2 {
		t.Fatalf("file read: want 2 (t-300, t-240), got %d", len(got))
	}
	// nothing there: an empty array, not null (null blanks the chart)
	got := s.ReadRange(ctx, "nope", now-60, now)
	if b, _ := json.Marshal(got); string(b) != "[]" {
		t.Fatalf("empty range serialized as %s", b)
	}
}

func TestReadRangeStopsWhenClientLeaves(t *testing.T) {
	s, _ := newTestStore(t, "A")
	now := time.Now().Unix()
	s.Append("A", Round{T: now, S: 20, R: 20})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := s.ReadRange(ctx, "A", now-300*24*3600, now); len(got) != 0 {
		t.Fatalf("cancelled read returned %d rounds", len(got))
	}
}

// Regression: a day whose first round had <=4 replies was taken as already
// downsampled and skipped forever.
func TestDownsampleDayWithLossyFirstRound(t *testing.T) {
	s, dir := newTestStore(t, "A")
	day := time.Now().AddDate(0, 0, -40)
	writeDay(t, dir, "A", day.Format("2006-01-02"), []Round{
		{T: day.Unix(), S: 20, R: 3, MS: samples(3, 1)},
		{T: day.Unix() + 60, S: 20, R: 20, MS: samples(20, 1)},
	})
	s.Tier(30, 300)
	rounds, _ := s.readDay("A", day.Format("2006-01-02"))
	if len(rounds) != 2 {
		t.Fatalf("rounds lost: %d", len(rounds))
	}
	if len(rounds[0].MS) != 3 || len(rounds[1].MS) != 4 {
		t.Fatalf("want 3 and 4 samples, got %d and %d", len(rounds[0].MS), len(rounds[1].MS))
	}
}

// A day file already downsampled is not read again on later nights.
func TestTierSkipsColdFilesOnLaterRuns(t *testing.T) {
	s, dir := newTestStore(t, "A")
	day := time.Now().AddDate(0, 0, -40)
	name := day.Format("2006-01-02")
	full := []Round{{T: day.Unix(), S: 20, R: 20, MS: samples(20, 1)}}
	writeDay(t, dir, "A", name, full)
	s.Tier(30, 300)
	// rewrite it with raw samples behind Tier's back: a second pass that re-read
	// cold files would downsample it again
	writeDay(t, dir, "A", name, full)
	s.Tier(30, 300)
	rounds, _ := s.readDay("A", name)
	if len(rounds[0].MS) != 20 {
		t.Fatal("cold file was read again on the second pass")
	}
}

func TestTierRetentionRemovesExpiredDays(t *testing.T) {
	s, dir := newTestStore(t, "A")
	old := time.Now().AddDate(0, 0, -301)
	path := writeDay(t, dir, "A", old.Format("2006-01-02"), []Round{{T: old.Unix(), S: 20, R: 20, MS: samples(4, 1)}})
	s.cold[path] = true
	s.Tier(30, 300)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("day past retention still on disk")
	}
	if s.cold[path] {
		t.Fatal("removed file still tracked as cold")
	}
}

func TestDownsampleIsAtomicAndIdempotent(t *testing.T) {
	s, dir := newTestStore(t, "A")
	day := time.Now().AddDate(0, 0, -40)
	name := day.Format("2006-01-02")
	writeDay(t, dir, "A", name, []Round{{T: day.Unix(), S: 20, R: 20, MS: samples(20, 1)}})
	for i := 0; i < 2; i++ {
		if err := s.Downsample("A", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "A", name+".jsonl.tmp")); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
	rounds, _ := s.readDay("A", name)
	if len(rounds) != 1 || len(rounds[0].MS) != 4 {
		t.Fatalf("unexpected content after two passes: %+v", rounds)
	}
}

func TestCalcStats(t *testing.T) {
	st := calcStats([]Round{
		{S: 10, R: 10, MS: samples(10, 1)}, // 1..10
		{S: 10, R: 5, MS: samples(5, 11), B: true},
	})
	if st.Rounds != 2 || st.Bursts != 1 {
		t.Fatalf("counts: %+v", st)
	}
	if st.LossPct != 25 {
		t.Fatalf("loss: want 25%%, got %v", st.LossPct)
	}
	if st.P50 != 8 || st.P99 != 14 {
		t.Fatalf("percentiles over 1..15: p50=%v p99=%v", st.P50, st.P99)
	}
	if empty := calcStats(nil); empty.P50 != 0 || empty.LossPct != 0 {
		t.Fatalf("empty stats: %+v", empty)
	}
}

// Regression: day iteration started at from's time of day, so with to earlier in
// its day than from, the last day's file was never opened.
func TestReadRangeIncludesLastDay(t *testing.T) {
	s, dir := newTestStore(t, "A")
	d1 := time.Now().AddDate(0, 0, -10)
	d1 = time.Date(d1.Year(), d1.Month(), d1.Day(), 0, 0, 0, 0, time.Local)
	d2 := d1.AddDate(0, 0, 1)
	writeDay(t, dir, "A", d1.Format("2006-01-02"), []Round{{T: d1.Add(20 * time.Hour).Unix(), S: 20, R: 20}})
	writeDay(t, dir, "A", d2.Format("2006-01-02"), []Round{{T: d2.Add(5 * time.Hour).Unix(), S: 20, R: 20}})
	from, to := d1.Add(12*time.Hour).Unix(), d2.Add(6*time.Hour).Unix()
	if got := s.ReadRange(context.Background(), "A", from, to); len(got) != 2 {
		t.Fatalf("want rounds from both days, got %d", len(got))
	}
}
