package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
	"github.com/cannicombustiva/wrygloss-agent/internal/ingestref"
)

const testToken = "wgd_test_token_0123456789abcdefghijklmnopqrstu"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fixtureDB crea il database sintetico (testdata/kobo.sql) e il file version accanto.
func fixtureDB(t *testing.T, dir string) string {
	t.Helper()
	sqlText, err := os.ReadFile("testdata/kobo.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "KoboReader.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(sqlText)); err != nil {
		t.Fatal(err)
	}
	v, _ := os.ReadFile("testdata/version")
	if err := os.WriteFile(filepath.Join(dir, "version"), v, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func execDB(t *testing.T, path, q string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}

// harness: server di riferimento con cattura dei corpi decompressi e un ambiente agente in cartelle temporanee.
type harness struct {
	t      *testing.T
	srv    *ingestref.Server
	dev    *ingestref.Device
	http   *httptest.Server
	clk    *clock
	e      env
	mu     sync.Mutex
	bodies [][]byte
	// intercept, se impostato, puo' rispondere al posto del server (status != 0).
	intercept func(n int, body []byte) int
	calls     int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, clk: &clock{t: time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)}}
	h.newServer()
	h.http = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.http.Close)

	root := t.TempDir()
	dir := filepath.Join(root, "adds")
	tmp := filepath.Join(root, "tmp")
	dbDir := filepath.Join(root, "kobo")
	for _, d := range []string{dir, tmp, dbDir} {
		os.MkdirAll(d, 0755)
	}
	route := filepath.Join(root, "route")
	os.WriteFile(route, []byte("Iface\tDestination\tGateway\nwlan0\t00000000\t0101A8C0\n"), 0644)
	h.e = env{
		dir: dir, dbPath: fixtureDB(t, dbDir), versionPath: filepath.Join(dbDir, "version"), routePath: route,
		tmpDir: tmp, lockPath: filepath.Join(tmp, "wrygloss.lock"), routeWait: 45 * time.Second,
		retryBudget: 60 * time.Second, now: h.clk.now, sleep: h.clk.sleep,
	}
	h.writeConfig(testToken)
	logPath = filepath.Join(dir, "agent.log")
	prefix = "[test]"
	return h
}

// newServer sostituisce il server con uno vuoto (simula la perdita dei dati lato server).
func (h *harness) newServer() {
	srv, err := ingestref.New()
	if err != nil {
		h.t.Fatal(err)
	}
	srv.Now = h.clk.now
	srv.RatePerHour, srv.Burst = 1e9, 1e9
	srv.LatestAgent = version
	h.srv = srv
	h.dev = srv.AddDevice(testToken)
}

func (h *harness) writeConfig(token string) {
	cfg := fmt.Sprintf(`{"server_url":%q,"token":%q}`, h.http.URL, token)
	os.WriteFile(filepath.Join(h.e.dir, "config.json"), []byte(cfg), 0644)
}

func (h *harness) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := raw
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err == nil {
			body, _ = io.ReadAll(zr)
		}
	}
	h.mu.Lock()
	h.calls++
	n := h.calls
	h.bodies = append(h.bodies, body)
	icpt := h.intercept
	h.mu.Unlock()
	if icpt != nil {
		if status := icpt(n, body); status != 0 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"type":"urn:wrygloss:problem:x","title":"x","status":%d,"code":"x"}`, status)
			return
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	h.srv.ServeHTTP(w, r)
}

// runAgent esegue l'agente dopo aver fatto avanzare l'orologio oltre recentSkip.
func (h *harness) runAgent() bool {
	h.clk.sleep(recentSkip + time.Second)
	return run(h.e)
}

func (h *harness) lastRequest() contract.Request {
	return h.dev.Requests[len(h.dev.Requests)-1]
}

func (h *harness) readState() *state {
	st, _ := loadState(filepath.Join(h.e.dir, "state.json"), tokenFP(testToken))
	return st
}

func TestFixtureSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := fixtureDB(t, dir)
	snap, err := readSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snap.Items[contract.Annotations]); n != 32 {
		t.Errorf("annotations=%d, attese 32 (22 evidenziazioni, 2 note, 8 dogear)", n)
	}
	if n := len(snap.Items[contract.Words]); n != 62 {
		t.Errorf("words=%d, attese 62", n)
	}
	if n := len(snap.Items[contract.Books]); n != 2 {
		t.Errorf("books=%d, attesi 2 (solo i citati)", n)
	}
	if len(snap.Warnings) != 0 {
		t.Errorf("avvisi inattesi: %v", snap.Warnings)
	}
	byID := map[string]contract.Annotation{}
	for _, it := range snap.Items[contract.Annotations] {
		var a contract.Annotation
		json.Unmarshal(it.Raw, &a)
		byID[a.ID] = a
	}
	if a := byID["h0"]; a.ChapterTitle == nil || *a.ChapterTitle != "At the market" || *a.Hidden {
		t.Errorf("h0: chapter_title=%v hidden=%v", a.ChapterTitle, a.Hidden)
	}
	if a := byID["h21"]; a.Hidden == nil || !*a.Hidden {
		t.Errorf("h21: Hidden 'true' non normalizzato: %v", a.Hidden)
	}
	if a := byID["d0"]; a.Text != nil || *a.Type != "dogear" {
		t.Errorf("d0: %+v", a)
	}
	words := map[string]bool{}
	for _, it := range snap.Items[contract.Words] {
		words[it.ID] = true
	}
	for _, w := range []string{"snazzy,", "Snazzy", "word0,", "cuesta"} {
		if !words[w] {
			t.Errorf("parola grezza %q mancante: l'agente non deve pulire", w)
		}
	}
	snap2, _ := readSnapshot(path)
	for i, it := range snap.Items[contract.Words] {
		if snap2.Items[contract.Words][i].H != it.H {
			t.Fatalf("h non deterministico per %q", it.ID)
		}
	}
}

func TestReadDeviceSkipsSerial(t *testing.T) {
	d := readDevice("testdata/version", "arm")
	if d.Firmware == nil || *d.Firmware != "4.46.23836" || d.ModelID == nil || !strings.HasSuffix(*d.ModelID, "391") {
		t.Fatalf("device=%+v", d)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "CANARY") {
		t.Fatalf("numero di serie nel device: %s", b)
	}
}

func TestSyncEndToEndWithoutCanaries(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if got := h.dev.Live(contract.Annotations); got != 32 {
		t.Errorf("server annotations=%d", got)
	}
	if got := h.dev.Live(contract.Words); got != 62 {
		t.Errorf("server words=%d", got)
	}
	if got := h.dev.Live(contract.Books); got != 2 {
		t.Errorf("server books=%d", got)
	}
	req := h.lastRequest()
	if !req.Run.Final || req.Manifest == nil || req.Run.RouteReadyMS == nil || !req.Run.RouteAtEvent {
		t.Errorf("run=%+v", req.Run)
	}
	// Nessun valore canarino (tabella user, UserID, ___UserID, numero di serie, libri non citati, analytics)
	// in alcun corpo inviato, nel log o nello stato.
	all := bytes.Join(h.bodies, nil)
	all = append(all, readLog(t, h)...)
	st, _ := os.ReadFile(filepath.Join(h.e.dir, "state.json"))
	all = append(all, st...)
	if i := bytes.Index(all, []byte("CANARY")); i >= 0 {
		t.Fatalf("valore canarino trapelato: %q", all[max(0, i-80):min(len(all), i+40)])
	}
	if strings.Contains(readLog(t, h), "snazzy") {
		t.Fatal("il log contiene parole dell'utente")
	}
}

func TestIncrementalSyncAndDeletes(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	// Nessun cambiamento: solo manifest.
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	req := h.lastRequest()
	if n := len(req.Upserts.Annotations) + len(req.Upserts.Words) + len(req.Upserts.Books); n != 0 {
		t.Errorf("secondo sync senza cambiamenti ha inviato %d record", n)
	}
	if len((*req.Manifest)[contract.Words]) != 62 {
		t.Errorf("manifest words=%d", len((*req.Manifest)[contract.Words]))
	}

	execDB(t, h.e.dbPath, `DELETE FROM Bookmark WHERE BookmarkID = 'h3'; DELETE FROM WordList WHERE Text = 'Snazzy';
		UPDATE Bookmark SET Annotation = 'nuova nota' WHERE BookmarkID = 'h4';`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	req = h.lastRequest()
	if len(req.Upserts.Annotations) != 1 || len(req.Upserts.Words) != 0 {
		t.Errorf("upserts dopo una modifica: %d annotations, %d words", len(req.Upserts.Annotations), len(req.Upserts.Words))
	}
	if h.dev.Live(contract.Annotations) != 31 || h.dev.Live(contract.Words) != 61 {
		t.Errorf("dopo le cancellazioni: annotations=%d words=%d", h.dev.Live(contract.Annotations), h.dev.Live(contract.Words))
	}
	if st := h.readState(); len(st.Acked[contract.Annotations]) != 31 || st.Acked[contract.Words]["Snazzy"] != "" {
		t.Errorf("stato confermato non ripulito: %d annotations", len(st.Acked[contract.Annotations]))
	}

	// Il record ricompare (es. copia incoerente corretta al sync successivo): ripristino, non duplicato.
	execDB(t, h.e.dbPath, `INSERT INTO WordList VALUES ('Snazzy', 'file:///mnt/onboard/Books/Novel.epub', '-en', '2026-10-01T09:00:00Z')`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if h.dev.Live(contract.Words) != 62 {
		t.Errorf("parola non ripristinata: %d", h.dev.Live(contract.Words))
	}
}

func TestServerDataLossSelfHeals(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	h.newServer() // il server ha perso tutto, l'agente crede di essere aggiornato
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if h.dev.Live(contract.Annotations) != 32 || h.dev.Live(contract.Words) != 62 || h.dev.Live(contract.Books) != 2 {
		t.Fatalf("non riparato: %d/%d/%d", h.dev.Live(contract.Annotations), h.dev.Live(contract.Words), h.dev.Live(contract.Books))
	}
	if n := len(h.dev.Requests); n != 2 {
		t.Errorf("attese 2 richieste (manifest, poi i mancanti), fatte %d", n)
	}
}

func TestRevokedTokenBlocksUntilConfigChanges(t *testing.T) {
	h := newHarness(t)
	h.dev.Revoked = true
	if h.runAgent() {
		t.Fatal("sync riuscito con token revocato")
	}
	calls := h.calls
	if !h.runAgent() { // SKIP per blocco: esito normale
		t.Fatal(readLog(t, h))
	}
	if h.calls != calls {
		t.Fatal("l'agente bloccato ha contattato il server")
	}
	newToken := "wgd_new_token_0123456789abcdefghijklmnopqrstuv"
	dev := h.srv.AddDevice(newToken)
	h.writeConfig(newToken)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if dev.Live(contract.Words) != 62 {
		t.Fatalf("nuovo token: words=%d", dev.Live(contract.Words))
	}
}

func TestRetriesOn503(t *testing.T) {
	h := newHarness(t)
	h.intercept = func(n int, _ []byte) int {
		if n <= 2 {
			return http.StatusServiceUnavailable
		}
		return 0
	}
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if req := h.lastRequest(); req.Run.Attempt != 3 {
		t.Errorf("attempt=%d, atteso 3", req.Run.Attempt)
	}
}

func TestGivesUpAfterRetryBudget(t *testing.T) {
	h := newHarness(t)
	h.intercept = func(int, []byte) int { return http.StatusBadGateway }
	if h.runAgent() {
		t.Fatal("riuscito con il server sempre in errore")
	}
	if h.calls > len(backoff)+1 {
		t.Errorf("troppi tentativi: %d", h.calls)
	}
}

func TestRecentSuccessSkipsDoubleEvent(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	calls := h.calls
	h.clk.sleep(12 * time.Second) // secondo evento a 12 s, come misurato sul Clara BW
	if !run(h.e) {
		t.Fatal(readLog(t, h))
	}
	if h.calls != calls {
		t.Fatal("evento doppio non saltato")
	}
}

func TestNoRouteFails(t *testing.T) {
	h := newHarness(t)
	os.WriteFile(h.e.routePath, []byte("Iface\tDestination\tGateway\n"), 0644)
	if h.runAgent() {
		t.Fatal("riuscito senza rotta")
	}
	if h.calls != 0 {
		t.Fatal("contattato il server senza rotta")
	}
	if !strings.Contains(readLog(t, h), "nessuna rotta") {
		t.Fatal(readLog(t, h))
	}
}

func TestBatchingLargeDatabase(t *testing.T) {
	h := newHarness(t)
	execDB(t, h.e.dbPath, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 2499)
		INSERT INTO WordList SELECT 'bulk' || i, 'file:///mnt/onboard/Books/Novel.epub', '-en', '2026-10-05T00:00:00Z' FROM n`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if got := h.dev.Live(contract.Words); got != 2562 {
		t.Fatalf("words=%d", got)
	}
	if n := len(h.dev.Requests); n < 3 {
		t.Errorf("attesi almeno 3 lotti, fatti %d", n)
	}
	for i, r := range h.dev.Requests {
		if (r.Manifest != nil) != r.Run.Final || r.Run.Batch != i+1 {
			t.Errorf("lotto %d: final=%v manifest=%v batch=%d", i, r.Run.Final, r.Manifest != nil, r.Run.Batch)
		}
	}
}

func TestShrinksBatchesOn413(t *testing.T) {
	h := newHarness(t)
	execDB(t, h.e.dbPath, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 1499)
		INSERT INTO WordList SELECT 'bulk' || i, NULL, '-en', NULL FROM n`)
	h.intercept = func(_ int, body []byte) int {
		var req contract.Request
		json.Unmarshal(body, &req)
		if len(req.Upserts.Words)+len(req.Upserts.Annotations) > 300 { // il server accetta lotti piu' piccoli
			return http.StatusRequestEntityTooLarge
		}
		return 0
	}
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if h.dev.Live(contract.Words) != 1562 || h.dev.Live(contract.Annotations) != 32 {
		t.Fatalf("dopo 413: %d/%d", h.dev.Live(contract.Annotations), h.dev.Live(contract.Words))
	}
}

func TestMissingTableIsNotADeletion(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	// Firmware futuro: WordList sparita, Bookmark senza Color.
	execDB(t, h.e.dbPath, `DROP TABLE WordList; ALTER TABLE Bookmark DROP COLUMN Color;`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	req := h.lastRequest()
	if _, ok := (*req.Manifest)[contract.Words]; ok {
		t.Error("words nel manifest senza tabella WordList")
	}
	if h.dev.Live(contract.Words) != 62 {
		t.Errorf("parole cancellate per una tabella mancante: %d", h.dev.Live(contract.Words))
	}
	w := strings.Join(req.Run.Warnings, " ")
	if !strings.Contains(w, "missing_table:WordList") || !strings.Contains(w, "missing_column:Bookmark.Color") {
		t.Errorf("avvisi: %v", req.Run.Warnings)
	}
}

// Il database del Kobo e' in WAL: le righe non ancora riportate nel file principale devono essere lette.
func TestCopyIncludesUncheckpointedWAL(t *testing.T) {
	dir := t.TempDir()
	path := fixtureDB(t, dir)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`,
		`INSERT INTO WordList VALUES ('only-in-wal', NULL, '-en', NULL)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("WAL assente: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "copy.sqlite")
	if _, err := copyStable(path, dst, func(time.Duration) {}); err != nil {
		t.Fatal(err)
	}
	snap, err := readSnapshot(dst)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range snap.Items[contract.Words] {
		found = found || it.ID == "only-in-wal"
	}
	if !found {
		t.Fatal("riga presente solo nel WAL non letta")
	}
}

func TestMakeBatches(t *testing.T) {
	mk := func(n, size int) []entry {
		out := make([]entry, n)
		for i := range out {
			out[i] = entry{contract.Words, item{ID: fmt.Sprint(i), Raw: make([]byte, size)}}
		}
		return out
	}
	if b := makeBatches(nil, defaultBudget, 10); len(b) != 1 || len(b[0]) != 0 {
		t.Errorf("senza voci: %v", b)
	}
	if b := makeBatches(mk(2500, 10), defaultBudget, 10); len(b) != 3 || len(b[2]) != 500 {
		t.Errorf("per numero: %d lotti", len(b))
	}
	b := makeBatches(mk(3, 900<<10), defaultBudget, 10)
	if len(b) != 2 || len(b[0]) != 2 || len(b[1]) != 1 {
		t.Errorf("per dimensione: %d lotti", len(b))
	}
	b = makeBatches(mk(2, 10), defaultBudget, 3<<20)
	if len(b) != 2 || len(b[1]) != 0 {
		t.Errorf("manifest grande: atteso un lotto finale vuoto, %d lotti", len(b))
	}
}

func readLog(t *testing.T, h *harness) string {
	b, _ := os.ReadFile(filepath.Join(h.e.dir, "agent.log"))
	return string(b)
}
