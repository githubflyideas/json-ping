package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubProbes records started probe loops instead of running them and restores
// the real starter and the global running set afterwards.
func stubProbes(t *testing.T) *probeLog {
	t.Helper()
	pl := &probeLog{}
	orig := startProbe
	startProbe = func(tg TargetCfg, _ ProbeCfg, _ *Store, _ *Detector, stop chan struct{}) {
		pl.mu.Lock()
		pl.started = append(pl.started, tg.Name)
		pl.mu.Unlock()
	}
	t.Cleanup(func() {
		startProbe = orig
		clear(runningSig)
	})
	return pl
}

type probeLog struct {
	mu      sync.Mutex
	started []string
}

func (p *probeLog) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.started
	p.started = nil
	sort.Strings(out)
	return out
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestApplyTargetsDiff(t *testing.T) {
	pl := stubProbes(t)
	s, _ := newTestStore(t)
	cfg := defaultConfig()
	det := NewDetector(s)
	mgr := map[string]chan struct{}{}
	a := TargetCfg{Name: "A", Type: "icmp", Host: "10.0.0.1", dir: "A"}
	b := TargetCfg{Name: "B", Type: "icmp", Host: "10.0.0.2", dir: "B"}

	applyTargets(cfg, []TargetCfg{a, b}, s, det, mgr)
	if got := pl.take(); strings.Join(got, ",") != "A,B" {
		t.Fatalf("initial start: %v", got)
	}
	chA, chB := mgr["A"], mgr["B"]

	// same set again: nothing restarts
	applyTargets(cfg, []TargetCfg{a, b}, s, det, mgr)
	if got := pl.take(); len(got) != 0 || isClosed(chA) {
		t.Fatalf("unchanged targets restarted: %v", got)
	}

	// A changes pace, B goes away, C arrives
	a.Pace = "fast"
	c := TargetCfg{Name: "C", Type: "tcp", Host: "10.0.0.3", Port: 443, dir: "C"}
	applyTargets(cfg, []TargetCfg{a, c}, s, det, mgr)
	if got := pl.take(); strings.Join(got, ",") != "A,C" {
		t.Fatalf("restart/start after change: %v", got)
	}
	if !isClosed(chA) || !isClosed(chB) {
		t.Fatal("old loops for changed/removed targets not stopped")
	}
	if _, ok := mgr["B"]; ok {
		t.Fatal("removed target still managed")
	}
	names := s.Names()
	sort.Strings(names)
	if strings.Join(names, ",") != "A,C" {
		t.Fatalf("store names: %v", names)
	}
	var cfgNames []string
	for _, x := range cfg.TargetList() {
		cfgNames = append(cfgNames, x.Name)
	}
	sort.Strings(cfgNames)
	if strings.Join(cfgNames, ",") != "A,C" {
		t.Fatalf("cfg targets: %v", cfgNames)
	}
}

func TestTargetListIsACopy(t *testing.T) {
	cfg := defaultConfig()
	cfg.SetTargets([]TargetCfg{{Name: "A"}})
	l := cfg.TargetList()
	l[0].Name = "changed"
	if cfg.TargetList()[0].Name != "A" {
		t.Fatal("TargetList exposed the shared slice")
	}
}

func TestValidateTargets(t *testing.T) {
	cases := []struct {
		name string
		ts   []TargetCfg
		ok   bool
	}{
		{"defaults to icmp", []TargetCfg{{Name: "a", Host: "1.1.1.1"}}, true},
		{"tcp needs port", []TargetCfg{{Name: "a", Type: "tcp", Host: "h"}}, false},
		{"unknown type", []TargetCfg{{Name: "a", Type: "udp", Host: "h"}}, false},
		{"bad pace", []TargetCfg{{Name: "a", Host: "h", Pace: "turbo"}}, false},
		{"no name", []TargetCfg{{Host: "h"}}, false},
		// both sanitize to the same directory: must be rejected, not merged on disk
		{"dir collision", []TargetCfg{{Name: "a b", Host: "h"}, {Name: "a_b", Host: "h"}}, false},
	}
	for _, c := range cases {
		cfg := &Config{Targets: c.ts}
		if err := validateTargets(cfg); (err == nil) != c.ok {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
	cfg := &Config{Targets: []TargetCfg{{Name: "x/../y", Host: "h"}}}
	validateTargets(cfg)
	if strings.ContainsAny(cfg.Targets[0].dir, "/\\") {
		t.Fatalf("dir not sanitized: %q", cfg.Targets[0].dir)
	}
}

func TestLoadTargetListsReportsBadLine(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ping.list"), []byte("# c\n1.1.1.1 one\n\n2.2.2.2 two pace=fast\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "tcp.list"), []byte("10.0.0.1:443 gw\nbad-line-no-port\n"), 0o644)
	_, err := loadTargetLists(dir)
	if err == nil || !strings.Contains(err.Error(), "tcp.list line 2") {
		t.Fatalf("want error naming tcp.list line 2, got %v", err)
	}
	os.WriteFile(filepath.Join(dir, "tcp.list"), []byte("10.0.0.1:443 gw\n"), 0o644)
	ts, err := loadTargetLists(dir)
	if err != nil || len(ts) != 3 {
		t.Fatalf("want 3 targets, got %d (%v)", len(ts), err)
	}
}

func TestParseAuthArgs(t *testing.T) {
	m, err := parseAuthArgs([]string{"user=a,b", "passwd=x,y"})
	if err != nil || m["a"] != "x" || m["b"] != "y" {
		t.Fatalf("pairs: %v %v", m, err)
	}
	if m, err := parseAuthArgs(nil); err != nil || m != nil {
		t.Fatalf("no args means no auth: %v %v", m, err)
	}
	for _, bad := range [][]string{
		{"user=a,b", "passwd=x"},
		{"user=a,", "passwd=x,y"},
		{"token=abc"},
		{"stray"},
	} {
		if _, err := parseAuthArgs(bad); err == nil {
			t.Errorf("%v: want error", bad)
		}
	}
}

func TestPortOf(t *testing.T) {
	for in, want := range map[string]string{"0.0.0.0:8517": ":8517", "127.0.0.1:9000": ":9000", "bogus": ":8517"} {
		if got := portOf(in); got != want {
			t.Errorf("portOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckBurst(t *testing.T) {
	s, _ := newTestStore(t, "A")
	d := NewDetector(s)
	now := time.Now().Unix()

	if b, _ := d.CheckBurst("A", Round{S: 20, R: 19}); b {
		t.Fatal("one lost packet is noise")
	}
	// cold start (<30 rounds of history): absolute 25% floor
	if b, _ := d.CheckBurst("A", Round{S: 20, R: 16}); b {
		t.Fatal("20% loss flagged during cold start")
	}
	if b, _ := d.CheckBurst("A", Round{S: 20, R: 15}); !b {
		t.Fatal("25% loss not flagged during cold start")
	}

	// clean baseline (MAD = 0): 10% fallback
	for i := 60; i > 0; i-- {
		s.rings["A"] = ringAppend(s.rings["A"], Round{T: now - int64(i*60), S: 20, R: 20})
	}
	if b, _ := d.CheckBurst("A", Round{S: 20, R: 18}); !b {
		t.Fatal("10% loss on a clean link not flagged")
	}

	// noisy baseline (0..2 lost per round): z-score decides
	s.rings["A"] = nil
	for i := 60; i > 0; i-- {
		s.rings["A"] = ringAppend(s.rings["A"], Round{T: now - int64(i*60), S: 20, R: 20 - i%3})
	}
	if b, _ := d.CheckBurst("A", Round{S: 20, R: 17}); b {
		t.Fatal("3 lost is within a 0..2 baseline's noise")
	}
	b, z := d.CheckBurst("A", Round{S: 20, R: 10})
	if !b || z < burstZ {
		t.Fatalf("10 lost vs 0..2 baseline: burst=%v z=%v", b, z)
	}
}

func TestListenAddr(t *testing.T) {
	cases := []struct {
		listen string
		local  bool
		want   string
	}{
		{"0.0.0.0:8517", false, "0.0.0.0:8517"},
		{"0.0.0.0:9000", false, "0.0.0.0:9000"},
		{":8517", false, ":8517"}, // all interfaces, IPv4 and IPv6
		{"10.1.2.3:8517", false, "10.1.2.3:8517"},
		{"0.0.0.0:9000", true, "127.0.0.1:9000"}, // --localhost keeps the port
		{"[::]:8517", false, "[::]:8517"},
	}
	for _, c := range cases {
		got, err := listenAddr(c.listen, c.local)
		if err != nil || got != c.want {
			t.Errorf("listenAddr(%q, %v) = %q, %v; want %q", c.listen, c.local, got, err, c.want)
		}
	}
	for _, bad := range []string{"0.0.0.0", "8517", "0.0.0.0:0", "0.0.0.0:70000", "0.0.0.0:http"} {
		if _, err := listenAddr(bad, false); err == nil {
			t.Errorf("listenAddr(%q): want error", bad)
		}
	}
}
