// Wrygloss test agent: legge SOLO i conteggi di Bookmark e WordList da una copia di
// KoboReader.sqlite, verifica rete e TLS, e (se configurato) invia i conteggi a un endpoint di prova.
// Non legge testi, titoli o la tabella "user".
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed cacert.pem
var caPEM []byte

const version = "0.1.0-test"

const (
	lastEventFile = "/tmp/wrygloss-last-event"
	lockFile      = "/tmp/wrygloss.lock"
)

type Config struct {
	Endpoint           string `json:"endpoint"`
	Token              string `json:"token"`
	DBPath             string `json:"db_path"`
	QuietSeconds       int    `json:"quiet_seconds"`
	MinIntervalSeconds int    `json:"min_interval_seconds"`
	RouteWaitSeconds   int    `json:"route_wait_seconds"`
	TLSTestURL         string `json:"tls_test_url"`
}

var (
	logPath string
	prefix  string
)

func logf(format string, a ...any) {
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

func loadConfig(dir string) Config {
	cfg := Config{
		DBPath:           "/mnt/onboard/.kobo/KoboReader.sqlite",
		QuietSeconds:     20,
		RouteWaitSeconds: 60,
		TLSTestURL:       "https://example.com",
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		logf("config: %v (uso i valori predefiniti)", err)
		return cfg
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		logf("config: JSON non valido: %v (uso i valori predefiniti)", err)
	}
	return cfg
}

func hasDefaultRoute() bool {
	b, err := os.ReadFile("/proc/net/route")
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

func waitRoute(maxSec int) bool {
	deadline := time.Now().Add(time.Duration(maxSec) * time.Second)
	for time.Now().Before(deadline) {
		if hasDefaultRoute() {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return hasDefaultRoute()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyDB copia il database e gli eventuali file accessori. Il database originale non viene mai aperto.
func copyDB(src, dst string) (sidecars []string, err error) {
	if err = copyFile(src, dst); err != nil {
		return nil, err
	}
	for _, suf := range []string{"-wal", "-shm", "-journal"} {
		if _, e := os.Stat(src + suf); e == nil {
			if e := copyFile(src+suf, dst+suf); e == nil {
				sidecars = append(sidecars, suf)
			}
		}
	}
	return sidecars, nil
}

var required = map[string][]string{
	"Bookmark": {"BookmarkID", "VolumeID", "ContentID", "StartContainerPath", "Text", "Annotation", "DateCreated", "Type", "Hidden"},
	"WordList": {"Text", "VolumeId", "DictSuffix", "DateCreated"},
}

// readCounts interroga SOLO le tabelle Bookmark e WordList e restituisce numeri, mai contenuti.
func readCounts(path string) (map[string]any, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_query_only=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	out := map[string]any{}

	byType := map[string]int{}
	rows, err := db.Query(`SELECT COALESCE(Type,''), COUNT(*) FROM Bookmark GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("Bookmark: %w", err)
	}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err == nil {
			byType[t] = n
		}
	}
	rows.Close()
	out["bookmark_by_type"] = byType

	var words int
	if err := db.QueryRow(`SELECT COUNT(*) FROM WordList`).Scan(&words); err != nil {
		return nil, fmt.Errorf("WordList: %w", err)
	}
	out["wordlist_rows"] = words

	missing := []string{}
	for table, cols := range required {
		have := map[string]bool{}
		r, err := db.Query(`SELECT name FROM pragma_table_info('` + table + `')`)
		if err != nil {
			return nil, err
		}
		for r.Next() {
			var name string
			if r.Scan(&name) == nil {
				have[name] = true
			}
		}
		r.Close()
		for _, c := range cols {
			if !have[c] {
				missing = append(missing, table+"."+c)
			}
		}
	}
	out["missing_columns"] = missing

	var qc string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&qc); err == nil {
		out["quick_check"] = qc
	}
	return out, nil
}

func tlsCheck(url string, roots *x509.CertPool) string {
	t0 := time.Now()
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}},
	}
	resp, err := client.Get(url)
	ms := time.Since(t0).Milliseconds()
	if err != nil {
		msg := err.Error()
		if len(msg) > 120 {
			msg = msg[:120]
		}
		return fmt.Sprintf("error=%q ms=%d", msg, ms)
	}
	resp.Body.Close()
	return fmt.Sprintf("status=%d ms=%d", resp.StatusCode, ms)
}

func main() {
	dir := flag.String("dir", "/mnt/onboard/.adds/wrygloss-test", "cartella di lavoro (config, log, stato)")
	flag.Parse()
	logPath = filepath.Join(*dir, "agent.log")
	prefix = fmt.Sprintf("[%d]", os.Getpid())
	start := time.Now()
	cfg := loadConfig(*dir)
	logf("START version=%s arch=%s", version, runtime.GOARCH)

	// 1) Debounce: vince l'ultimo evento di una raffica.
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = os.WriteFile(lastEventFile, []byte(stamp), 0644)
	time.Sleep(time.Duration(cfg.QuietSeconds) * time.Second)
	if b, _ := os.ReadFile(lastEventFile); strings.TrimSpace(string(b)) != stamp {
		logf("SKIP superato da un evento piu' recente")
		return
	}

	// 2) Blocco contro esecuzioni sovrapposte.
	lock, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			logf("SKIP un altro sync e' in corso")
			return
		}
		defer lock.Close()
	}

	// 3) Intervallo minimo tra sync riusciti.
	statePath := filepath.Join(*dir, "state.json")
	if cfg.MinIntervalSeconds > 0 {
		var st struct {
			LastOK int64 `json:"last_ok"`
		}
		if b, err := os.ReadFile(statePath); err == nil && json.Unmarshal(b, &st) == nil {
			if age := time.Now().Unix() - st.LastOK; age < int64(cfg.MinIntervalSeconds) {
				logf("SKIP ultimo sync riuscito %ds fa (minimo %ds)", age, cfg.MinIntervalSeconds)
				return
			}
		}
	}

	// 4) Attesa della rotta predefinita.
	t0 := time.Now()
	if !waitRoute(cfg.RouteWaitSeconds) {
		logf("FAIL nessuna rotta predefinita dopo %ds", cfg.RouteWaitSeconds)
		return
	}
	logf("route ok dopo %dms", time.Since(t0).Milliseconds())

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	diag := map[string]any{"embedded_roots": bytes.Count(caPEM, []byte("BEGIN CERTIFICATE"))}

	// 5) Rete: DNS e TLS (con certificati incorporati e con quelli di sistema, che sul Kobo mancano).
	host := strings.TrimPrefix(strings.TrimPrefix(cfg.TLSTestURL, "https://"), "http://")
	t0 = time.Now()
	_, dnsErr := net.LookupHost(strings.Split(host, "/")[0])
	diag["dns_ms"] = time.Since(t0).Milliseconds()
	diag["dns_ok"] = dnsErr == nil
	diag["tls_embedded"] = tlsCheck(cfg.TLSTestURL, pool)
	diag["tls_system"] = tlsCheck(cfg.TLSTestURL, nil)
	logf("dns_ok=%v dns_ms=%v tls_embedded=%v tls_system=%v", diag["dns_ok"], diag["dns_ms"], diag["tls_embedded"], diag["tls_system"])

	// 6) Copia del database e lettura dei soli conteggi.
	tmp := "/tmp/wrygloss-copy.sqlite"
	t0 = time.Now()
	sidecars, err := copyDB(cfg.DBPath, tmp)
	diag["copy_ms"] = time.Since(t0).Milliseconds()
	diag["sidecars"] = sidecars
	if err != nil {
		logf("FAIL copia database: %v", err)
		return
	}
	defer func() {
		os.Remove(tmp)
		for _, s := range []string{"-wal", "-shm", "-journal"} {
			os.Remove(tmp + s)
		}
	}()
	t0 = time.Now()
	counts, err := readCounts(tmp)
	diag["query_ms"] = time.Since(t0).Milliseconds()
	if err != nil {
		logf("FAIL lettura database: %v", err)
		return
	}
	logf("counts=%v copy_ms=%v query_ms=%v sidecars=%v", counts, diag["copy_ms"], diag["query_ms"], sidecars)

	// 7) Invio all'endpoint di prova, se configurato.
	postOK := true
	if cfg.Endpoint != "" {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		diag["mem_sys_mb"] = ms.Sys / (1 << 20)
		payload := map[string]any{
			"agent_version": version,
			"sent_at":       time.Now().UTC().Format(time.RFC3339),
			"arch":          runtime.GOARCH,
			"counts":        counts,
			"diagnostics":   diag,
		}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", cfg.Endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
		}
		client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
		t0 = time.Now()
		resp, err := client.Do(req)
		if err != nil {
			postOK = false
			logf("POST errore: %v", err)
		} else {
			resp.Body.Close()
			postOK = resp.StatusCode/100 == 2
			logf("POST status=%d bytes=%d ms=%d", resp.StatusCode, len(body), time.Since(t0).Milliseconds())
		}
	} else {
		logf("nessun endpoint configurato: invio saltato")
	}

	if postOK {
		b, _ := json.Marshal(map[string]int64{"last_ok": time.Now().Unix()})
		_ = os.WriteFile(statePath, b, 0644)
	}
	logf("END ok=%v totale_ms=%d", postOK, time.Since(start).Milliseconds())
}
