package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(t *testing.T, users map[string]string) (*httptest.Server, *Config, *Store) {
	t.Helper()
	cfg := defaultConfig()
	cfg.Targets = []TargetCfg{{Name: "A", Type: "icmp", Host: "127.0.0.1", dir: "A"}}
	s, _ := newTestStore(t, "A")
	s.Append("A", Round{T: time.Now().Unix() - 60, S: 20, R: 20, MS: samples(20, 5)})
	srv := httptest.NewServer(newMux(cfg, s, users))
	t.Cleanup(srv.Close)
	return srv, cfg, s
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func login(t *testing.T, srv *httptest.Server, user, pass string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"User": user, "Pass": pass})
	res, err := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func get(t *testing.T, srv *httptest.Server, path string, c *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	res, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestOpenUIWithoutUsers(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	res := get(t, srv, "/api/targets", nil)
	if res.StatusCode != 200 {
		t.Fatalf("open UI: /api/targets = %d", res.StatusCode)
	}
	var items []map[string]any
	json.NewDecoder(res.Body).Decode(&items)
	if len(items) != 1 || items[0]["name"] != "A" {
		t.Fatalf("targets payload: %v", items)
	}
}

func TestAuthFlow(t *testing.T) {
	srv, _, _ := newTestServer(t, map[string]string{"admin": "s3cret"})

	if res := get(t, srv, "/api/targets", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no cookie: want 401, got %d", res.StatusCode)
	}
	if res := get(t, srv, "/", nil); res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/login" {
		t.Fatalf("page without cookie should redirect to /login, got %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res := login(t, srv, "admin", "wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password: want 401, got %d", res.StatusCode)
	}

	res := login(t, srv, "admin", "s3cret")
	if res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	var c *http.Cookie
	for _, x := range res.Cookies() {
		if x.Name == "pingping_session" {
			c = x
		}
	}
	if c == nil || c.MaxAge != 7200 || !c.HttpOnly {
		t.Fatalf("session cookie: %+v", c)
	}
	if res := get(t, srv, "/api/targets", c); res.StatusCode != 200 {
		t.Fatalf("with cookie: %d", res.StatusCode)
	}
	if res := get(t, srv, "/api/series?target=A&minutes=60", c); res.StatusCode != 200 {
		t.Fatalf("series with cookie: %d", res.StatusCode)
	}
}

func TestLogoutRevokesToken(t *testing.T) {
	srv, _, _ := newTestServer(t, map[string]string{"admin": "s3cret"})
	res := login(t, srv, "admin", "s3cret")
	c := res.Cookies()[0]
	req, _ := http.NewRequest("POST", srv.URL+"/api/logout", nil)
	req.AddCookie(c)
	out, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if res := get(t, srv, "/api/targets", c); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token still valid after logout: %d", res.StatusCode)
	}
}

func TestSessionExpiresAfter7200s(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newSessions()
	s.now = func() time.Time { return now }
	tok := s.issue()

	now = now.Add(7199 * time.Second)
	if !s.valid(tok) {
		t.Fatal("session expired early")
	}
	now = now.Add(2 * time.Second)
	if s.valid(tok) {
		t.Fatal("session still valid after 7200s")
	}
	if _, ok := s.m[tok]; ok {
		t.Fatal("expired token not deleted")
	}
}

func TestIssueSweepsExpiredTokens(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newSessions()
	s.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		s.issue()
	}
	now = now.Add(sessionTTL + time.Second)
	s.issue()
	if len(s.m) != 1 {
		t.Fatalf("expired tokens kept: %d in map", len(s.m))
	}
}

func TestLimiterCapsConcurrency(t *testing.T) {
	var cur, peak int32
	release := make(chan struct{})
	h := limiter(maxInflight, 5*time.Second)(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&cur, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&cur, -1)
	})
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest("GET", "/", nil))
			codes <- rec.Code
		}()
	}
	time.Sleep(100 * time.Millisecond) // let all 8 arrive; 3 must be queued
	if got := atomic.LoadInt32(&cur); got != maxInflight {
		t.Fatalf("in flight: want %d, got %d", maxInflight, got)
	}
	close(release)
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Fatalf("queued request failed: %d", c)
		}
	}
	if peak != maxInflight {
		t.Fatalf("peak concurrency %d, want %d", peak, maxInflight)
	}
}

func TestLimiterBusyAfterWait(t *testing.T) {
	release := make(chan struct{})
	h := limiter(1, 50*time.Millisecond)(func(w http.ResponseWriter, r *http.Request) { <-release })
	go h(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	time.Sleep(20 * time.Millisecond)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/", nil))
	close(release)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("want 503 with Retry-After, got %d %v", rec.Code, rec.Header())
	}
}

func TestLimiterDropsAbandonedWaiters(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var ran int32
	h := limiter(1, 5*time.Second)(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ran, 1)
		<-release
	})
	go h(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter still queued")
	}
	if atomic.LoadInt32(&ran) != 1 {
		t.Fatal("cancelled request ran its handler")
	}
}

// Unauthenticated requests are rejected before the limiter and never hold a slot.
func TestUnauthenticatedRequestsSkipLimiter(t *testing.T) {
	srv, _, _ := newTestServer(t, map[string]string{"admin": "s3cret"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := http.Get(srv.URL + "/api/series?target=A")
			if err == nil {
				res.Body.Close()
				if res.StatusCode != http.StatusUnauthorized {
					t.Errorf("want 401, got %d", res.StatusCode)
				}
			}
		}()
	}
	wg.Wait()
}

func TestSeriesUnknownTargetIsEmptyArray(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	res := get(t, srv, "/api/series?target=nope&minutes=60", nil)
	var raw json.RawMessage
	json.NewDecoder(res.Body).Decode(&raw)
	if string(raw) != "[]" {
		t.Fatalf("want [], got %s", raw)
	}
}

// Regression for the data race: the reload loop rewrote cfg.Targets while
// /api/targets read it. Meaningful under -race.
func TestTargetsReadDuringReload(t *testing.T) {
	stubProbes(t)
	srv, cfg, s := newTestServer(t, nil)
	det := NewDetector(s)
	mgr := map[string]chan struct{}{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 30; i++ {
			applyTargets(cfg, []TargetCfg{
				{Name: "A", Type: "icmp", Host: "127.0.0.1", dir: "A"},
				{Name: "T", Type: "tcp", Host: "127.0.0.1", Port: 1 + i%2, dir: "T"},
			}, s, det, mgr)
		}
	}()
	for i := 0; i < 30; i++ {
		get(t, srv, "/api/targets", nil)
	}
	<-done
}
