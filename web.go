package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

//go:embed static
var staticFS embed.FS

// sessionTTL is an absolute lifetime: a login is good for two hours from the moment
// it was issued, whether or not the page keeps refreshing.
const sessionTTL = 7200 * time.Second

// maxInflight caps concurrent data requests (/api/targets, /api/series). A long
// series read holds the CPU and the disk; five at a time keeps a small host
// responsive no matter how many tabs or clients are open.
const (
	maxInflight = 5
	queueWait   = 30 * time.Second // then 503: a queue this long means the host is saturated
)

// sessions: in-memory, so a restart logs everyone out — acceptable and simple
// for a single-binary tool. Auth exists only when users are passed on the CLI.
type sessions struct {
	mu  sync.Mutex
	m   map[string]time.Time
	now func() time.Time // tests move the clock
}

func newSessions() *sessions { return &sessions{m: map[string]time.Time{}, now: time.Now} }

func (s *sessions) issue() string {
	b := make([]byte, 16)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	now := s.now()
	for t, exp := range s.m { // tokens nobody presents again would otherwise pile up
		if now.After(exp) {
			delete(s.m, t)
		}
	}
	s.m[tok] = now.Add(sessionTTL)
	s.mu.Unlock()
	return tok
}

func (s *sessions) revoke(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}

func (s *sessions) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[tok]
	if !ok || s.now().After(exp) {
		delete(s.m, tok)
		return false
	}
	return true
}

// limiter admits at most n handlers at once. Excess requests wait for a slot until
// the client gives up (the UI aborts superseded series queries) or `wait` passes.
func limiter(n int, wait time.Duration) func(http.HandlerFunc) http.HandlerFunc {
	slots := make(chan struct{}, n)
	return func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case slots <- struct{}{}:
			case <-r.Context().Done():
				return
			case <-t.C:
				w.Header().Set("Retry-After", "5")
				http.Error(w, `{"error":"busy"}`, http.StatusServiceUnavailable)
				return
			}
			defer func() { <-slots }()
			h(w, r)
		}
	}
}

func serveWeb(cfg *Config, store *Store, users map[string]string) error {
	return http.ListenAndServe(cfg.Listen, newMux(cfg, store, users))
}

func newMux(cfg *Config, store *Store, users map[string]string) http.Handler {
	sess := newSessions()
	limit := limiter(maxInflight, queueWait)
	mux := http.NewServeMux()

	authed := func(r *http.Request) bool {
		if len(users) == 0 {
			return true
		}
		c, err := r.Cookie("pingping_session")
		return err == nil && sess.valid(c.Value)
	}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) { // before the limiter: unauthenticated requests never hold a slot
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			limit(func(w http.ResponseWriter, r *http.Request) {
				t0 := time.Now()
				h(w, r)
				if d := time.Since(t0); d > time.Second {
					log.Printf("slow request: %s %s took %v", r.URL.Path, r.URL.RawQuery, d)
				}
			})(w, r)
		}
	}
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := staticFS.ReadFile("static/" + name)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !authed(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		page("index.html")(w, r)
	})
	mux.HandleFunc("/login", page("login.html"))
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	// login: constant-time compare, 1s delay on failure to make brute force boring
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct{ User, Pass string }
		json.NewDecoder(r.Body).Decode(&body)
		want, ok := users[body.User]
		if len(users) == 0 || !ok ||
			subtle.ConstantTimeCompare([]byte(body.Pass), []byte(want)) != 1 {
			time.Sleep(time.Second)
			log.Printf("web login failed from %s", r.RemoteAddr)
			http.Error(w, `{"error":"auth"}`, http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "pingping_session", Value: sess.issue(),
			Path: "/", HttpOnly: true, MaxAge: int(sessionTTL.Seconds()), SameSite: http.SameSiteLaxMode})
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("pingping_session"); err == nil {
			sess.revoke(c.Value) // otherwise the token stays valid until it expires
		}
		http.SetCookie(w, &http.Cookie{Name: "pingping_session", Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": version})
	})

	mux.HandleFunc("/api/targets", guard(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		type item struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Host        string `json:"host"`
			IntervalSec int    `json:"interval_sec"`
			Pace        string `json:"pace"`
			Down        bool   `json:"down"`
			Last1h      Stats  `json:"last_1h"`
			Last24h     Stats  `json:"last_24h"`
		}
		byName := map[string]TargetCfg{}
		for _, t := range cfg.TargetList() {
			byName[t.Name] = t
		}
		var out []item
		for _, name := range store.Names() {
			t := byName[name]
			iv, _ := probeParams(t, cfg.Probe)
			pace := t.Pace
			if pace == "" {
				pace = "normal"
			}
			rec := store.Recent(name, now.Add(-time.Hour).Unix())
			down := len(rec) > 0 && rec[len(rec)-1].R == 0
			out = append(out, item{
				Name: name, Type: t.Type, Host: targetAddr(t),
				IntervalSec: int(iv.Seconds()), Pace: pace, Down: down,
				Last1h:  calcStats(rec),
				Last24h: calcStats(store.Recent(name, now.Add(-24*time.Hour).Unix())),
			})
		}
		writeJSON(w, out)
	}))

	// raw rounds for smoke. Supports either minutes=N (recent window) or from/to unix
	// (arbitrary range). No thinning: every sample is returned as stored.
	mux.HandleFunc("/api/series", guard(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		name := q.Get("target")
		from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
		to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
		if from == 0 || to == 0 {
			minutes, _ := strconv.Atoi(q.Get("minutes"))
			if minutes <= 0 || minutes > 432000 { // up to 300 days
				minutes = 360
			}
			to = time.Now().Unix()
			from = to - int64(minutes)*60
		}
		writeJSON(w, store.ReadRange(r.Context(), name, from, to))
	}))

	return mux
}

func targetAddr(t TargetCfg) string {
	if t.Type == "tcp" {
		return t.Host + ":" + strconv.Itoa(t.Port)
	}
	return t.Host
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
