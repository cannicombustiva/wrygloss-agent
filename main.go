// Wrygloss agent v0.2: a ogni attivazione del wifi legge da una copia di KoboReader.sqlite una lista fissa di
// colonne (evidenziazioni, note, dogear, parole cercate, libri citati) e le sincronizza con il server di Wrygloss
// secondo il contratto in spec/ingest/v1. Non legge mai la tabella user ne' gli id dell'account.
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

// recentSkip: un secondo evento wifi entro questo tempo da un sync riuscito non rifa' il lavoro (eventi doppi
// a 11-16 s l'uno dall'altro, misurati sul Clara BW). Il server puo' alzarlo con min_interval_s.
const recentSkip = 30 * time.Second

type Config struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
}

// env raccoglie tutto cio' che dipende dal dispositivo, per poter provare run() su un computer qualsiasi.
type env struct {
	dir         string // config, stato, log
	dbPath      string
	versionPath string
	routePath   string
	tmpDir      string
	lockPath    string
	routeWait   time.Duration
	retryBudget time.Duration
	now         func() time.Time
	sleep       func(time.Duration)
	transport   http.RoundTripper // nil: transport con radici incorporate
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
	flag.StringVar(&e.dir, "dir", "/mnt/onboard/.adds/wrygloss", "cartella di lavoro (config, stato, log)")
	flag.StringVar(&e.dbPath, "db", "/mnt/onboard/.kobo/KoboReader.sqlite", "database del dispositivo")
	flag.StringVar(&e.routePath, "route-file", e.routePath, "tabella delle rotte (per i test)")
	flag.StringVar(&e.tmpDir, "tmp", e.tmpDir, "cartella per la copia del database e il blocco")
	showVersion := flag.Bool("version", false, "stampa la versione ed esce")
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
		return cfg, fmt.Errorf("JSON non valido: %w", err)
	}
	for k := range keys {
		if k != "server_url" && k != "token" {
			logf("config: chiave sconosciuta %q ignorata", k)
		}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("JSON non valido: %w", err)
	}
	u, err := url.Parse(cfg.ServerURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return cfg, fmt.Errorf("server_url non valido")
	}
	if u.Scheme == "http" {
		logf("config: server_url senza HTTPS (solo per prove in rete locale)")
	}
	if cfg.Token == "" {
		return cfg, fmt.Errorf("token mancante")
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

// run esegue un sync completo e restituisce true se riuscito o saltato per un motivo normale.
func run(e env) bool {
	start := e.now()
	routeAtEvent := hasDefaultRoute(e.routePath) // stato della rotta all'istante dell'evento
	logf("START version=%s route_at_event=%v", version, routeAtEvent)

	cfg, err := loadConfig(e.dir)
	if err != nil {
		logf("FAIL config: %v", err)
		return false
	}

	// Blocco contro esecuzioni sovrapposte: un secondo evento mentre il primo lavora non fa nulla.
	lock, err := os.OpenFile(e.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		logf("FAIL blocco: %v", err)
		return false
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		logf("SKIP un altro sync e' in corso")
		return true
	}

	statePath := filepath.Join(e.dir, "state.json")
	fp := tokenFP(cfg.Token)
	st, reset := loadState(statePath, fp)
	if reset {
		logf("stato azzerato (token cambiato o stato illeggibile): reinvio completo")
	}
	if b := st.Blocked; b != nil {
		if (b.Code == contract.CodeInvalidToken || b.Code == contract.CodeTokenRevoked) && b.TokenFP == fp ||
			b.Code == contract.CodeSchemaUnsupported && b.AgentVersion == version {
			logf("SKIP bloccato da %s: serve un nuovo config o una nuova versione dell'agente", b.Code)
			return true
		}
		st.Blocked = nil
	}
	if st.LastOK > 0 {
		gap := max(recentSkip, time.Duration(st.MinIntervalS)*time.Second)
		if age := start.Sub(time.Unix(st.LastOK, 0)); age >= 0 && age < gap {
			logf("SKIP ultimo sync riuscito %ds fa (minimo %ds)", int(age.Seconds()), int(gap.Seconds()))
			return true
		}
	}

	// Attesa della rotta: sostituisce il debounce fisso di 20 s della v0.1.
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
		logf("FAIL nessuna rotta predefinita dopo %ds", int(e.routeWait.Seconds()))
		return false
	}

	t0 := e.now()
	tmp := filepath.Join(e.tmpDir, "wrygloss-copy.sqlite")
	attempts, err := copyStable(e.dbPath, tmp, e.sleep)
	defer removeCopy(tmp)
	if err != nil {
		logf("FAIL copia database (tentativi=%d): %v", attempts, err)
		return false
	}
	copyMS := e.now().Sub(t0).Milliseconds()
	t0 = e.now()
	snap, err := readSnapshot(tmp)
	removeCopy(tmp)
	if err != nil {
		logf("FAIL lettura database: %v", err)
		return false
	}
	queryMS := e.now().Sub(t0).Milliseconds()
	logf("letti annotations=%d words=%d books=%d copy_ms=%d copy_attempts=%d query_ms=%d warnings=%v",
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
		saveState(st, statePath) // conserva le conferme dei lotti gia' accettati
		logf("FAIL sync dopo %d lotti: %v sync_ms=%d wire_bytes=%d", res.Batches, err, syncMS, s.wire)
		return false
	}
	st.LastOK = e.now().Unix()
	st.MinIntervalS = res.Resp.MinIntervalS
	saveState(st, statePath)
	if res.Missing > 0 {
		logf("attenzione: %d record ancora mancanti per il server dopo %d giri", res.Missing, res.Rounds)
	}
	if la := res.Resp.LatestAgent; la != nil && *la != version {
		logf("disponibile una nuova versione dell'agente: %s", *la)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	logf("END ok sent=%d/%d/%d deleted=%d/%d/%d rejected=%d batches=%d rounds=%d sync_ms=%d wire_bytes=%d mem_sys_mb=%d totale_ms=%d",
		res.Sent[contract.Annotations], res.Sent[contract.Words], res.Sent[contract.Books],
		res.Deleted[contract.Annotations], res.Deleted[contract.Words], res.Deleted[contract.Books],
		res.Rejected, res.Batches, res.Rounds, syncMS, s.wire, ms.Sys>>20, e.now().Sub(start).Milliseconds())
	return true
}

func saveState(st *state, path string) {
	if err := st.save(path); err != nil {
		logf("stato non salvato: %v", err)
	}
}
