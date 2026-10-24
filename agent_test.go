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

// fixtureDB creates the synthetic database (testdata/kobo.sql) and the version file next to it.
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

// harness: reference server capturing decompressed bodies, plus an agent environment in temporary directories.
type harness struct {
	t      *testing.T
	srv    *ingestref.Server
	dev    *ingestref.Device
	http   *httptest.Server
	clk    *clock
	e      env
	mu     sync.Mutex
	bodies [][]byte
	// intercept, if set, can answer instead of the server (status != 0).
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

// newServer replaces the server with an empty one (simulates server-side data loss).
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

// runAgent runs the agent after moving the clock past recentSkip.
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
		t.Errorf("annotations=%d, want 32 (22 highlights, 2 notes, 8 dogears)", n)
	}
	if n := len(snap.Items[contract.Words]); n != 62 {
		t.Errorf("words=%d, want 62", n)
	}
	if n := len(snap.Items[contract.Books]); n != 2 {
		t.Errorf("books=%d, want 2 (cited only)", n)
	}
	if len(snap.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", snap.Warnings)
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
		t.Errorf("h21: Hidden 'true' not normalized: %v", a.Hidden)
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
			t.Errorf("raw word %q missing: the agent must not clean", w)
		}
	}
	snap2, _ := readSnapshot(path)
	for i, it := range snap.Items[contract.Words] {
		if snap2.Items[contract.Words][i].H != it.H {
			t.Fatalf("non-deterministic h for %q", it.ID)
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
		t.Fatalf("serial number in device: %s", b)
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
	// No canary value (user table, UserID, ___UserID, serial number, uncited books, analytics) in any body sent,
	// in the log or in the state.
	all := bytes.Join(h.bodies, nil)
	all = append(all, readLog(t, h)...)
	st, _ := os.ReadFile(filepath.Join(h.e.dir, "state.json"))
	all = append(all, st...)
	if i := bytes.Index(all, []byte("CANARY")); i >= 0 {
		t.Fatalf("canary value leaked: %q", all[max(0, i-80):min(len(all), i+40)])
	}
	if strings.Contains(readLog(t, h), "snazzy") {
		t.Fatal("the log contains user words")
	}
}

func TestIncrementalSyncAndDeletes(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	// No changes: manifest only.
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	req := h.lastRequest()
	if n := len(req.Upserts.Annotations) + len(req.Upserts.Words) + len(req.Upserts.Books); n != 0 {
		t.Errorf("second sync without changes sent %d records", n)
	}
	if len((*req.Manifest)[contract.Words]) != 62 {
		t.Errorf("manifest words=%d", len((*req.Manifest)[contract.Words]))
	}

	execDB(t, h.e.dbPath, `DELETE FROM Bookmark WHERE BookmarkID = 'h3'; DELETE FROM WordList WHERE Text = 'Snazzy';
		UPDATE Bookmark SET Annotation = 'new note' WHERE BookmarkID = 'h4';`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	req = h.lastRequest()
	if len(req.Upserts.Annotations) != 1 || len(req.Upserts.Words) != 0 {
		t.Errorf("upserts after an edit: %d annotations, %d words", len(req.Upserts.Annotations), len(req.Upserts.Words))
	}
	if h.dev.Live(contract.Annotations) != 31 || h.dev.Live(contract.Words) != 61 {
		t.Errorf("after the deletions: annotations=%d words=%d", h.dev.Live(contract.Annotations), h.dev.Live(contract.Words))
	}
	if st := h.readState(); len(st.Acked[contract.Annotations]) != 31 || st.Acked[contract.Words]["Snazzy"] != "" {
		t.Errorf("acked state not cleaned up: %d annotations", len(st.Acked[contract.Annotations]))
	}

	// The record reappears (e.g. an inconsistent copy fixed at the next sync): restore, not duplicate.
	execDB(t, h.e.dbPath, `INSERT INTO WordList VALUES ('Snazzy', 'file:///mnt/onboard/Books/Novel.epub', '-en', '2026-10-01T09:00:00Z')`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if h.dev.Live(contract.Words) != 62 {
		t.Errorf("word not restored: %d", h.dev.Live(contract.Words))
	}
}

func TestServerDataLossSelfHeals(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	h.newServer() // the server lost everything, the agent believes it is up to date
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if h.dev.Live(contract.Annotations) != 32 || h.dev.Live(contract.Words) != 62 || h.dev.Live(contract.Books) != 2 {
		t.Fatalf("not repaired: %d/%d/%d", h.dev.Live(contract.Annotations), h.dev.Live(contract.Words), h.dev.Live(contract.Books))
	}
	if n := len(h.dev.Requests); n != 2 {
		t.Errorf("want 2 requests (manifest, then the missing ones), got %d", n)
	}
}

func TestRevokedTokenBlocksUntilConfigChanges(t *testing.T) {
	h := newHarness(t)
	h.dev.Revoked = true
	if h.runAgent() {
		t.Fatal("sync succeeded with a revoked token")
	}
	calls := h.calls
	if !h.runAgent() { // SKIP because blocked: normal outcome
		t.Fatal(readLog(t, h))
	}
	if h.calls != calls {
		t.Fatal("the blocked agent contacted the server")
	}
	newToken := "wgd_new_token_0123456789abcdefghijklmnopqrstuv"
	dev := h.srv.AddDevice(newToken)
	h.writeConfig(newToken)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if dev.Live(contract.Words) != 62 {
		t.Fatalf("new token: words=%d", dev.Live(contract.Words))
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
		t.Errorf("attempt=%d, want 3", req.Run.Attempt)
	}
}

func TestGivesUpAfterRetryBudget(t *testing.T) {
	h := newHarness(t)
	h.intercept = func(int, []byte) int { return http.StatusBadGateway }
	if h.runAgent() {
		t.Fatal("succeeded with the server always failing")
	}
	if h.calls > len(backoff)+1 {
		t.Errorf("too many attempts: %d", h.calls)
	}
}

func TestRecentSuccessSkipsDoubleEvent(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	calls := h.calls
	h.clk.sleep(12 * time.Second) // second event at 12 s, as measured on the Clara BW
	if !run(h.e) {
		t.Fatal(readLog(t, h))
	}
	if h.calls != calls {
		t.Fatal("double event not skipped")
	}
}

func TestNoRouteFails(t *testing.T) {
	h := newHarness(t)
	os.WriteFile(h.e.routePath, []byte("Iface\tDestination\tGateway\n"), 0644)
	if h.runAgent() {
		t.Fatal("succeeded without a route")
	}
	if h.calls != 0 {
		t.Fatal("contacted the server without a route")
	}
	if !strings.Contains(readLog(t, h), "no default route") {
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
		t.Errorf("want at least 3 batches, got %d", n)
	}
	for i, r := range h.dev.Requests {
		if (r.Manifest != nil) != r.Run.Final || r.Run.Batch != i+1 {
			t.Errorf("batch %d: final=%v manifest=%v batch=%d", i, r.Run.Final, r.Manifest != nil, r.Run.Batch)
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
		if len(req.Upserts.Words)+len(req.Upserts.Annotations) > 300 { // the server accepts smaller batches
			return http.StatusRequestEntityTooLarge
		}
		return 0
	}
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	if h.dev.Live(contract.Words) != 1562 || h.dev.Live(contract.Annotations) != 32 {
		t.Fatalf("after 413: %d/%d", h.dev.Live(contract.Annotations), h.dev.Live(contract.Words))
	}
}

func TestMissingTableIsNotADeletion(t *testing.T) {
	h := newHarness(t)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	// Future firmware: WordList gone, Bookmark without Color.
	execDB(t, h.e.dbPath, `DROP TABLE WordList; ALTER TABLE Bookmark DROP COLUMN Color;`)
	if !h.runAgent() {
		t.Fatal(readLog(t, h))
	}
	req := h.lastRequest()
	if _, ok := (*req.Manifest)[contract.Words]; ok {
		t.Error("words in the manifest without a WordList table")
	}
	if h.dev.Live(contract.Words) != 62 {
		t.Errorf("words deleted because of a missing table: %d", h.dev.Live(contract.Words))
	}
	w := strings.Join(req.Run.Warnings, " ")
	if !strings.Contains(w, "missing_table:WordList") || !strings.Contains(w, "missing_column:Bookmark.Color") {
		t.Errorf("warnings: %v", req.Run.Warnings)
	}
}

// The Kobo database uses WAL: rows not yet checkpointed into the main file must be read.
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
		t.Fatalf("WAL missing: %v", err)
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
		t.Fatal("row present only in the WAL not read")
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
		t.Errorf("no entries: %v", b)
	}
	if b := makeBatches(mk(2500, 10), defaultBudget, 10); len(b) != 3 || len(b[2]) != 500 {
		t.Errorf("by count: %d batches", len(b))
	}
	b := makeBatches(mk(3, 900<<10), defaultBudget, 10)
	if len(b) != 2 || len(b[0]) != 2 || len(b[1]) != 1 {
		t.Errorf("by size: %d batches", len(b))
	}
	b = makeBatches(mk(2, 10), defaultBudget, 3<<20)
	if len(b) != 2 || len(b[1]) != 0 {
		t.Errorf("large manifest: want an empty final batch, %d batches", len(b))
	}
}

func readLog(t *testing.T, h *harness) string {
	b, _ := os.ReadFile(filepath.Join(h.e.dir, "agent.log"))
	return string(b)
}
