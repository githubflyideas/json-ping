package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

// demoPingList is written on first run so the very first launch shows smoke.
const demoPingList = `# one ICMP target per line; # is a comment; saved changes apply automatically
# format: host  [name]  [pace=fast|slow]  [interval=seconds]
www.google.com Demo pace=fast
`

var (
	flagListen = flag.String("listen", defaultConfig().Listen, "web UI address `host:port`; 0.0.0.0 = all interfaces")
	flagLocal  = flag.Bool("localhost", false, "bind 127.0.0.1 only (this machine only), keeping the --listen port")
	flagVer    = flag.Bool("version", false, "print version and exit")
)

// helpText is everything needed to get pingping running without the README:
// every command in it can be copied as is.
const helpText = `pingping %s — network latency oscilloscope. One binary: no Docker, no database, no config file.

USAGE
  ./pingping [flags] [user=NAME[,NAME2...] passwd=PASS[,PASS2...]]
  Flags go first; user= / passwd= come last.

QUICK START
  mkdir -p /home/pingping && cd /home/pingping
  # put the pingping binary here (downloads: https://github.com/githubflyideas/pingping/releases)
  ./pingping
  # open http://<server-ip>:8517   (all interfaces, port 8517, no login)

EXAMPLES
  ./pingping user=admin passwd=admin            # login page; a login lasts 2 hours
  ./pingping user=alice,bob passwd=pw1,pw2      # two users, paired by position
  ./pingping --listen 0.0.0.0:9000              # all interfaces, port 9000
  ./pingping --listen 10.1.2.3:8517             # one interface only
  ./pingping --localhost                        # 127.0.0.1:8517, this machine only
  ./pingping --listen 0.0.0.0:9000 user=admin passwd=admin

RUN IN BACKGROUND  (always start from /home/pingping: targets/ and data/ live there)
  cd /home/pingping
  nohup ./pingping user=admin passwd=admin > pingping.log 2>&1 &   # start, keeps running after logout
  tail -f pingping.log                                              # watch the log
  pkill -x pingping                                                 # stop

TARGETS  (edit any time; saved changes apply within 3 seconds, no restart)
  vim targets/ping.list    # ICMP: host       [name] [pace=fast|slow] [interval=SECONDS]
  vim targets/tcp.list     # TCP:  host:port  [name] [pace=fast|slow] [interval=SECONDS]

  echo "8.8.8.8 google-dns"            >> targets/ping.list
  echo "1.2.3.4 my-link pace=fast"     >> targets/ping.list
  echo "10.0.0.5:443 api-gw"           >> targets/tcp.list
  echo "10.0.0.6:3306 db interval=30"  >> targets/tcp.list

  pace   default: every 60s, 20 packets   fast: every 15s, 30 packets   slow: every 300s, 20 packets
  Lines starting with # are comments. Without a name, the host is the name.

ICMP PERMISSION  (root: nothing to do. Other users: without this, ping targets show 100%% loss)
  sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"

FILES  (relative to the directory pingping is started in)
  targets/ping.list                 created on first run with one demo target (www.google.com)
  targets/tcp.list                  create it when you need TCP targets; picked up automatically
  data/<target>/YYYY-MM-DD.jsonl    full samples 30 days, downsampled after, deleted at 300 days

FLAGS
`

// usage prints helpText followed by the flags, written with -- like the examples.
func usage(w io.Writer) {
	fmt.Fprintf(w, helpText, version)
	flag.VisitAll(func(f *flag.Flag) {
		if strings.HasPrefix(f.Name, "test.") { // go test registers its own
			return
		}
		name, u := flag.UnquoteUsage(f)
		arg := "--" + f.Name
		if name != "" {
			arg += " " + name
		}
		def := ""
		if f.DefValue != "" && f.DefValue != "false" {
			def = fmt.Sprintf(" (default %s)", f.DefValue)
		}
		fmt.Fprintf(w, "  %-22s %s%s\n", arg, u, def)
	})
	fmt.Fprintf(w, "  %-22s %s\n", "--help", "this text (also: ./pingping help)")
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "-h", "-help", "--help", "help":
			return true
		}
	}
	return false
}

func main() {
	flag.Usage = func() { usage(flag.CommandLine.Output()) }
	if wantsHelp(os.Args[1:]) { // asked for: stdout, so it pipes into less/grep
		flag.CommandLine.SetOutput(os.Stdout)
		flag.Usage()
		return
	}
	flag.Parse()
	if *flagVer {
		fmt.Println("pingping", version)
		return
	}

	// v2.1: no config file. Program parameters are constants; users edit target
	// lists and, optionally, pass web credentials on the command line:
	//   ./pingping user=u1,u2 passwd=p1,p2
	cfg := defaultConfig()
	users, err := parseAuthArgs(flag.Args())
	if err != nil {
		log.Fatalf("bad auth args: %v (see ./pingping --help)", err)
	}
	if cfg.Listen, err = listenAddr(*flagListen, *flagLocal); err != nil {
		log.Fatalf("bad --listen: %v (see ./pingping --help)", err)
	}

	// first-run bootstrap: a demo target list, nothing else
	if _, err := os.Stat(filepath.Join(cfg.TargetsDir, "ping.list")); os.IsNotExist(err) {
		os.MkdirAll(cfg.TargetsDir, 0o755)
		os.WriteFile(filepath.Join(cfg.TargetsDir, "ping.list"), []byte(demoPingList), 0o644)
		log.Printf("no targets found — generated %s/ping.list (probing www.google.com)", cfg.TargetsDir)
		log.Printf(`add a target with one line: echo "1.2.3.4 my-link" >> %s/ping.list (applies automatically)`, cfg.TargetsDir)
	}
	if err := FinishConfig(cfg); err != nil {
		log.Fatalf("startup failed: %v", err)
	}

	store, err := NewStore(cfg.DataDir, cfg.Targets)
	if err != nil {
		log.Fatalf("store init failed: %v", err)
	}
	store.Replay()
	detector := NewDetector(store)

	// one probe loop per target, individually stoppable for hot reload
	mgr := map[string]chan struct{}{}
	for _, t := range cfg.Targets {
		ch := make(chan struct{})
		mgr[t.Name] = ch
		runningSig[t.Name] = t
		go probeLoop(t, cfg.Probe, store, detector, ch)
	}
	stop := make(chan struct{})
	go reloadLoop(cfg, store, detector, mgr, stop)
	go housekeeping(cfg, store, stop)

	log.Printf("pingping %s up · %d targets · listening on %s · data in %s · %d-day retention",
		version, len(cfg.TargetList()), cfg.Listen, cfg.DataDir, cfg.RetentionDays)
	log.Printf("➜  open http://localhost%s for the smoke graph", portOf(cfg.Listen))
	if len(users) == 0 {
		log.Printf("tip: web UI is open to everyone; add a login with  ./pingping user=u1,u2 passwd=p1,p2")
	} else {
		log.Printf("web login enabled for %d user(s)", len(users))
	}

	go func() {
		if err := serveWeb(cfg, store, users); err != nil {
			log.Fatalf("web server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	close(stop)
	log.Printf("pingping shutting down")
}

// parseAuthArgs parses trailing "user=a,b passwd=x,y" arguments into a cred map.
func parseAuthArgs(args []string) (map[string]string, error) {
	var users, pws []string
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if strings.HasPrefix(a, "-") {
			return nil, fmt.Errorf("flag %q after user=/passwd=: put flags first", a)
		}
		if !ok {
			return nil, fmt.Errorf("unrecognized argument %q (expected user=... passwd=...)", a)
		}
		switch k {
		case "user", "users":
			users = strings.Split(v, ",")
		case "passwd", "password", "passwords":
			pws = strings.Split(v, ",")
		default:
			return nil, fmt.Errorf("unknown key %q (expected user=, passwd=)", k)
		}
	}
	if len(users) == 0 && len(pws) == 0 {
		return nil, nil
	}
	if len(users) != len(pws) {
		return nil, fmt.Errorf("user count (%d) != passwd count (%d)", len(users), len(pws))
	}
	m := map[string]string{}
	for i := range users {
		if users[i] == "" || pws[i] == "" {
			return nil, fmt.Errorf("empty user or passwd at position %d", i+1)
		}
		m[users[i]] = pws[i]
	}
	return m, nil
}

var runningSig = map[string]TargetCfg{}

// reloadLoop: stdlib mtime polling every 3s — save the list, the chart follows.
func reloadLoop(cfg *Config, store *Store, det *Detector, mgr map[string]chan struct{}, stop chan struct{}) {
	stamp := func() string {
		out := ""
		for _, f := range []string{"ping.list", "tcp.list"} {
			if fi, err := os.Stat(filepath.Join(cfg.TargetsDir, f)); err == nil {
				out += fmt.Sprintf("%s:%d;", f, fi.ModTime().UnixNano())
			}
		}
		return out
	}
	last := stamp()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if s := stamp(); s != last {
			last = s
			fresh, err := loadTargetLists(cfg.TargetsDir)
			if err != nil {
				log.Printf("target reload failed (keeping current set): %v", err)
				continue
			}
			tmp := &Config{Targets: fresh}
			if err := validateTargets(tmp); err != nil {
				log.Printf("target reload failed (keeping current set): %v", err)
				continue
			}
			applyTargets(cfg, tmp.Targets, store, det, mgr)
		}
	}
}

// applyTargets swaps the running target set without dropping in-memory history.
func applyTargets(cfg *Config, fresh []TargetCfg, store *Store, det *Detector, mgr map[string]chan struct{}) {
	sig := func(t TargetCfg) string {
		return fmt.Sprintf("%s|%s|%d|%s|%d", t.Type, t.Host, t.Port, t.Pace, t.IntervalSec)
	}
	want := map[string]TargetCfg{}
	for _, t := range fresh {
		want[t.Name] = t
	}
	for name, ch := range mgr {
		t, ok := want[name]
		if !ok || sig(t) != sig(runningSig[name]) {
			close(ch)
			delete(mgr, name)
			delete(runningSig, name)
			if !ok {
				store.RemoveTarget(name)
				log.Printf("[%s] target removed (data files kept)", name)
			}
		}
	}
	var all []TargetCfg
	for name, t := range want {
		all = append(all, t)
		if _, ok := mgr[name]; ok {
			continue
		}
		if err := store.EnsureTarget(t); err != nil {
			log.Printf("[%s] init failed: %v", name, err)
			continue
		}
		ch := make(chan struct{})
		mgr[name] = ch
		runningSig[name] = t
		startProbe(t, cfg.Probe, store, det, ch)
		log.Printf("[%s] target online (%s)", name, t.Host)
	}
	cfg.SetTargets(all)
}

// startProbe is swapped out by tests so reload logic can be checked without
// real probe goroutines outliving the test.
var startProbe = func(t TargetCfg, p ProbeCfg, s *Store, d *Detector, stop chan struct{}) {
	go probeLoop(t, p, s, d, stop)
}

// housekeeping: nightly retention at 00:05 — filename-dated files, plain unlink.
func housekeeping(cfg *Config, store *Store, stop chan struct{}) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	last := ""
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		now := time.Now()
		day := now.Format("2006-01-02")
		if now.Hour() == 0 && now.Minute() == 5 && last != day {
			last = day
			store.Tier(cfg.HotDays, cfg.RetentionDays)
		}
	}
}

// listenAddr validates --listen and applies --localhost, which keeps the port and
// swaps the host for 127.0.0.1. A bare ":8517" means all interfaces, as in net.Listen.
func listenAddr(listen string, localOnly bool) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("port %q must be 1-65535", port)
	}
	if localOnly {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func portOf(listen string) string {
	for i := len(listen) - 1; i >= 0; i-- {
		if listen[i] == ':' {
			return listen[i:]
		}
	}
	return ":8517"
}
