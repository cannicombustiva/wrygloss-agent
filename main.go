// Wrygloss agent v0.2: on every wifi activation it reads a fixed list of columns (highlights, notes, dogears, saved
// words, cited books) from a copy of KoboReader.sqlite and syncs them with the Wrygloss server according to the
// contract in spec/ingest/v1. It never reads the user table or the account ids.
package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed cacert.pem
var caPEM []byte

const version = "0.2.0"

// recentSkip: a second wifi event within this time of a successful sync does not redo the work (double events
// 11-16 s apart, measured on the Clara BW). The server can raise it with min_interval_s.
const recentSkip = 30 * time.Second

type Config struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
}

// env gathers everything that depends on the device, so run() can be tested on any computer.
type env struct {
	dir         string // config, state, log
	dbPath      string
	versionPath string
	routePath   string
	tmpDir      string
	lockPath    string
	routeWait   time.Duration
	retryBudget time.Duration
	now         func() time.Time
	sleep       func(time.Duration)
	transport   http.RoundTripper // nil: transport with embedded roots
}

var (
	logPath string
	prefix  string
)

func logf(format string, a ...any) {
	if logPath == "" {
		return
	}
	line := time.Now().Format("2006-01-02 15:04:05") + " " + prefix + " " + fmt.Sprintf(format, a...) + "\n"
	if st, err := os.Stat(logPath); err == nil && st.Size() > 200000 {
		_ = os.Rename(logPath, logPath+".old")
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func main() {
	e := env{
		routePath:   "/proc/net/route",
		tmpDir:      "/tmp",
		routeWait:   45 * time.Second,
		retryBudget: 60 * time.Second,
		now:         time.Now,
		sleep:       time.Sleep,
	}
	flag.StringVar(&e.dir, "dir", "/mnt/onboard/.adds/wrygloss", "working directory (config, state, log)")
	flag.StringVar(&e.dbPath, "db", "/mnt/onboard/.kobo/KoboReader.sqlite", "device database")
	flag.StringVar(&e.routePath, "route-file", e.routePath, "routing table (for tests)")
	flag.StringVar(&e.tmpDir, "tmp", e.tmpDir, "directory for the database copy and the lock")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	e.versionPath = filepath.Join(filepath.Dir(e.dbPath), "version")
	e.lockPath = filepath.Join(e.tmpDir, "wrygloss.lock")
	logPath = filepath.Join(e.dir, "agent.log")
	prefix = fmt.Sprintf("[%d]", os.Getpid())
	if !run(e) {
		os.Exit(1)
	}
}

func loadConfig(dir string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return cfg, err
	}
	var keys map[string]any
	if err := json.Unmarshal(b, &keys); err != nil {
		return cfg, fmt.Errorf("invalid JSON: %w", err)
	}
	for k := range keys {
		if k != "server_url" && k != "token" {
			logf("config: unknown key %q ignored", k)
		}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid JSON: %w", err)
	}
	u, err := url.Parse(cfg.ServerURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return cfg, fmt.Errorf("invalid server_url")
	}
	if u.Scheme == "http" {
		logf("config: server_url without HTTPS (local network tests only)")
	}
	if cfg.Token == "" {
		return cfg, fmt.Errorf("missing token")
	}
	return cfg, nil
}

func hasDefaultRoute(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for i, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if i > 0 && len(f) > 2 && f[1] == "00000000" {
			return true
		}
	}
	return false
}

func newRunID(t time.Time) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "r-" + t.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}

// run performs a full sync and returns true if it succeeded or was skipped for a normal reason.
func run(e env) bool {
	start := e.now()
	routeAtEvent := hasDefaultRoute(e.routePath) // route state at the instant of the event
	logf("START version=%s route_at_event=%v", version, routeAtEvent)

	cfg, err := loadConfig(e.dir)
	if err != nil {
		logf("FAIL config: %v", err)
		return false
	}

	// Lock against overlapping runs: a second event while the first one is working does nothing.
	lock, err := os.OpenFile(e.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		logf("FAIL lock: %v", err)
		return false
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		logf("SKIP another sync is running")
		return true
	}

	statePath := filepath.Join(e.dir, "state.json")
	fp := tokenFP(cfg.Token)
	st, reset := loadState(statePath, fp)
	if reset {
		logf("state reset (token changed or state unreadable): full resend")
	}
	if b := st.Blocked; b != nil {
		if (b.Code == contract.CodeInvalidToken || b.Code == contract.CodeTokenRevoked) && b.TokenFP == fp ||
			b.Code == contract.CodeSchemaUnsupported && b.AgentVersion == version {
			logf("SKIP blocked by %s: needs a new config or a new agent version", b.Code)
			return true
		}
		st.Blocked = nil
	}
	if st.LastOK > 0 {
		gap := max(recentSkip, time.Duration(st.MinIntervalS)*time.Second)
		if age := start.Sub(time.Unix(st.LastOK, 0)); age >= 0 && age < gap {
			logf("SKIP last successful sync %ds ago (minimum %ds)", int(age.Seconds()), int(gap.Seconds()))
			return true
		}
	}

	// Wait for the route: replaces the fixed 20 s debounce of v0.1.
	var routeReady *int64
	for {
		if hasDefaultRoute(e.routePath) {
			ms := e.now().Sub(start).Milliseconds()
			routeReady = &ms
			break
		}
		if e.now().Sub(start) >= e.routeWait {
			break
		}
		e.sleep(250 * time.Millisecond)
	}
	if routeReady == nil {
		logf("FAIL no default route after %ds", int(e.routeWait.Seconds()))
		return false
	}

	t0 := e.now()
	tmp := filepath.Join(e.tmpDir, "wrygloss-copy.sqlite")
	attempts, err := copyStable(e.dbPath, tmp, e.sleep)
	defer removeCopy(tmp)
	if err != nil {
		logf("FAIL database copy (attempts=%d): %v", attempts, err)
		return false
	}
	copyMS := e.now().Sub(t0).Milliseconds()
	t0 = e.now()
	snap, err := readSnapshot(tmp)
	removeCopy(tmp)
	if err != nil {
		logf("FAIL database read: %v", err)
		return false
	}
	queryMS := e.now().Sub(t0).Milliseconds()
	logf("read annotations=%d words=%d books=%d copy_ms=%d copy_attempts=%d query_ms=%d warnings=%v",
		len(snap.Items[contract.Annotations]), len(snap.Items[contract.Words]), len(snap.Items[contract.Books]),
		copyMS, attempts, queryMS, snap.Warnings)

	transport := e.transport
	if transport == nil {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caPEM)
		transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	}
	s := &sender{
		client:   &http.Client{Timeout: 20 * time.Second, Transport: transport},
		url:      strings.TrimSuffix(cfg.ServerURL, "/") + contract.SyncPath,
		token:    cfg.Token,
		now:      e.now,
		sleep:    e.sleep,
		jitter:   func(d time.Duration) time.Duration { return time.Duration(float64(d) * (0.8 + 0.4*mrand.Float64())) },
		deadline: e.now().Add(e.retryBudget),
	}
	base := contract.Request{
		Schema: contract.Schema,
		Run: contract.Run{
			ID: newRunID(start), AgentVersion: version, StartedAt: start.UTC().Format("2006-01-02T15:04:05.000Z"),
			RouteAtEvent: routeAtEvent, RouteReadyMS: routeReady,
		},
		Device: readDevice(e.versionPath, runtime.GOARCH),
	}
	t0 = e.now()
	res, err := syncSnapshot(s, base, snap, st)
	syncMS := e.now().Sub(t0).Milliseconds()
	if err != nil {
		if f, ok := err.(*failure); ok && (f.Code == contract.CodeInvalidToken || f.Code == contract.CodeTokenRevoked ||
			f.Code == contract.CodeSchemaUnsupported) {
			st.Blocked = &blocked{Code: f.Code, TokenFP: fp, AgentVersion: version, At: e.now().Unix()}
		}
		saveState(st, statePath) // keeps the acks of batches already accepted
		logf("FAIL sync after %d batches: %v sync_ms=%d wire_bytes=%d", res.Batches, err, syncMS, s.wire)
		return false
	}
	st.LastOK = e.now().Unix()
	st.MinIntervalS = res.Resp.MinIntervalS
	saveState(st, statePath)
	if res.Missing > 0 {
		logf("warning: %d records still missing on the server after %d rounds", res.Missing, res.Rounds)
	}
	if la := res.Resp.LatestAgent; la != nil && *la != version {
		logf("new agent version available: %s", *la)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	logf("END ok sent=%d/%d/%d deleted=%d/%d/%d rejected=%d batches=%d rounds=%d sync_ms=%d wire_bytes=%d mem_sys_mb=%d total_ms=%d",
		res.Sent[contract.Annotations], res.Sent[contract.Words], res.Sent[contract.Books],
		res.Deleted[contract.Annotations], res.Deleted[contract.Words], res.Deleted[contract.Books],
		res.Rejected, res.Batches, res.Rounds, syncMS, s.wire, ms.Sys>>20, e.now().Sub(start).Milliseconds())
	return true
}

func saveState(st *state, path string) {
	if err := st.save(path); err != nil {
		logf("state not saved: %v", err)
	}
}
