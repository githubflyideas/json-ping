package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestParseListLine(t *testing.T) {
	tt, err := parseListLine("59.43.247.1 HK CN2 pace=fast", "icmp")
	if err != nil || tt.Name != "HK CN2" || tt.Pace != "fast" {
		t.Fatalf("icmp line: %+v %v", tt, err)
	}
	tt, err = parseListLine("10.0.0.5:443 gw interval=30", "tcp")
	if err != nil || tt.Host != "10.0.0.5" || tt.Port != 443 || tt.IntervalSec != 30 {
		t.Fatalf("tcp line: %+v %v", tt, err)
	}
	if _, err := parseListLine("noport name", "tcp"); err == nil {
		t.Fatal("tcp without port should fail")
	}
}

func TestProbeParams(t *testing.T) {
	g := ProbeCfg{IntervalSec: 60, Packets: 20}
	if iv, pk := probeParams(TargetCfg{Pace: "fast"}, g); iv.Seconds() != 15 || pk != 30 {
		t.Fatalf("fast: %v %d", iv, pk)
	}
	if iv, _ := probeParams(TargetCfg{Pace: "fast", IntervalSec: 7}, g); iv.Seconds() != 7 {
		t.Fatalf("explicit interval wins: %v", iv)
	}
}

func TestRobustZ(t *testing.T) {
	base := make([]float64, 60)
	for i := range base {
		base[i] = float64(i % 3) // 0,1,2 loss pattern
	}
	if z := robustZ(9, base); z < 3 {
		t.Fatalf("9 losses vs 0-2 baseline should be anomalous, z=%v", z)
	}
}

func BenchmarkRobustZ(b *testing.B) {
	base := make([]float64, 240) // 4h 基线 @60s
	for i := range base {
		base[i] = float64(i % 3)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		robustZ(9, base)
	}
}

func BenchmarkCalcStats24h(b *testing.B) {
	rounds := make([]Round, 1440) // 24h @60s
	for i := range rounds {
		ms := make([]float64, 20)
		for j := range ms {
			ms[j] = 38 + float64(j%7)
		}
		rounds[i] = Round{T: int64(i * 60), S: 20, R: 20, MS: ms}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		calcStats(rounds)
	}
}

func FuzzParseListLine(f *testing.F) {
	f.Add("1.2.3.4 name pace=fast", "icmp")
	f.Add("10.0.0.5:443 gw interval=30", "tcp")
	f.Add("host:99999 x", "tcp")
	f.Add("::1 v6", "icmp")
	f.Fuzz(func(t *testing.T, line, typ string) {
		if typ != "icmp" && typ != "tcp" {
			typ = "icmp"
		}
		if len(line) == 0 || line[0] == '#' {
			return
		}
		// 只要求不 panic、不接受空 host
		tt, err := parseListLine(line, typ)
		if err == nil && tt.Host == "" {
			t.Fatalf("accepted empty host: %q", line)
		}
	})
}

func FuzzRobustZ(f *testing.F) {
	f.Add(float64(5), []byte{1, 2, 3, 0, 1})
	f.Fuzz(func(t *testing.T, x float64, raw []byte) {
		series := make([]float64, len(raw))
		for i, b := range raw {
			series[i] = float64(b)
		}
		z := robustZ(x, series) // 不 panic、不产出 NaN 即可
		if z != z {
			t.Fatalf("NaN: x=%v series=%v", x, series)
		}
	})
}

func TestDownsampleRound(t *testing.T) {
	ms := make([]float64, 20)
	for i := range ms {
		ms[i] = float64(i + 1) // 1..20
	}
	in := Round{T: 1234567890, S: 20, R: 18, MS: ms, B: true, Z: 3.42}
	out := downsampleRound(in)

	if len(out.MS) != 4 {
		t.Fatalf("want 4 samples, got %d", len(out.MS))
	}
	// min / median / p90 / max preserved in order
	if out.MS[0] != 1 || out.MS[3] != 20 {
		t.Fatalf("min/max wrong: %v", out.MS)
	}
	if out.MS[1] < out.MS[0] || out.MS[2] < out.MS[1] || out.MS[3] < out.MS[2] {
		t.Fatalf("not sorted: %v", out.MS)
	}
	// everything else must survive untouched — this is what keeps one code path
	if out.T != in.T || out.S != in.S || out.R != in.R || out.B != in.B || out.Z != in.Z {
		t.Fatalf("metadata lost: %+v", out)
	}
	// idempotent: downsampling twice changes nothing
	if again := downsampleRound(out); len(again.MS) != 4 || again.MS[0] != out.MS[0] {
		t.Fatalf("not idempotent: %v", again.MS)
	}
}

func TestTierDownsamplesOldDays(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, []TargetCfg{{Name: "X", Type: "icmp", Host: "1.1.1.1", dir: "X"}})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -40).Format("2006-01-02")
	recent := time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	ms := make([]float64, 20)
	for i := range ms {
		ms[i] = float64(i + 30)
	}
	for _, day := range []string{old, recent} {
		f, _ := os.Create(dir + "/X/" + day + ".jsonl")
		for i := 0; i < 10; i++ {
			line, _ := json.Marshal(Round{T: time.Now().Unix() - int64(i*60), S: 20, R: 20, MS: ms})
			f.Write(append(line, byte(10)))
		}
		f.Close()
	}
	s.Tier(30, 300)
	oldRounds, _ := s.readDay("X", old)
	newRounds, _ := s.readDay("X", recent)
	if len(oldRounds) == 0 || len(oldRounds[0].MS) != 4 {
		t.Fatalf("old day should be downsampled to 4 samples, got %d", len(oldRounds[0].MS))
	}
	if len(newRounds) == 0 || len(newRounds[0].MS) != 20 {
		t.Fatalf("recent day must keep all samples, got %d", len(newRounds[0].MS))
	}
	if len(oldRounds) != 10 {
		t.Fatalf("round count must survive downsampling: %d", len(oldRounds))
	}
}
