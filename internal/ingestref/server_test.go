package ingestref

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
	"github.com/cannicombustiva/wrygloss-agent/spec"
)

const tok = "wgd_server_test"

func post(t *testing.T, s *Server, body []byte, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, contract.SyncPath, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func example(t *testing.T, name string) []byte {
	b, err := spec.FS.ReadFile("ingest/v1/examples/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestServer(t *testing.T) (*Server, *Device) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return s, s.AddDevice(tok)
}

func TestErrorCodes(t *testing.T) {
	valid := example(t, "valid/full-sync.json")
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	zw.Write([]byte(`{"pad":"` + strings.Repeat("a", contract.MaxBodyBytes) + `"}`)) // decompresso oltre 8 MiB
	zw.Close()

	cases := []struct {
		name   string
		body   []byte
		hdr    map[string]string
		status int
		code   string
	}{
		{"token sconosciuto", valid, map[string]string{"Authorization": "Bearer wgd_nope"}, 401, contract.CodeInvalidToken},
		{"content-type", valid, map[string]string{"Content-Type": "text/plain"}, 415, contract.CodeUnsupportedMedia},
		{"encoding", valid, map[string]string{"Content-Encoding": "br"}, 415, contract.CodeUnsupportedMedia},
		{"gzip non valido", valid, map[string]string{"Content-Encoding": "gzip"}, 400, contract.CodeBadEncoding},
		{"gzip bomb", zipped.Bytes(), map[string]string{"Content-Encoding": "gzip"}, 413, contract.CodePayloadTooLarge},
		{"oltre 1 MiB sul filo", bytes.Repeat([]byte(" "), contract.MaxWireBytes+1), nil, 413, contract.CodePayloadTooLarge},
		{"non JSON", []byte("{"), nil, 400, contract.CodeMalformedJSON},
		{"minore sconosciuta", example(t, "invalid/unknown-minor-version.json"), nil, 410, contract.CodeSchemaUnsupported},
		{"campo sconosciuto", example(t, "invalid/unknown-record-field.json"), nil, 422, contract.CodeSchemaViolation},
		{"dati user", example(t, "invalid/user-data-top-level.json"), nil, 422, contract.CodeSchemaViolation},
	}
	for _, c := range cases {
		s, _ := newTestServer(t)
		status, out := post(t, s, c.body, c.hdr)
		if status != c.status || out["code"] != c.code {
			t.Errorf("%s: status=%d code=%v, attesi %d %s", c.name, status, out["code"], c.status, c.code)
		}
	}

	s, d := newTestServer(t)
	d.Revoked = true
	if status, out := post(t, s, valid, nil); status != 401 || out["code"] != contract.CodeTokenRevoked {
		t.Errorf("revocato: %d %v", status, out["code"])
	}
}

func TestSchemaViolationHasPointer(t *testing.T) {
	s, _ := newTestServer(t)
	_, out := post(t, s, example(t, "invalid/unknown-record-field.json"), nil)
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 || !strings.HasPrefix(errs[0].(map[string]any)["pointer"].(string), "/upserts/words/0") {
		t.Fatalf("errors=%v", out["errors"])
	}
}

func TestRateLimit(t *testing.T) {
	s, _ := newTestServer(t)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	s.AddDevice(tok) // ricrea il secchio con l'orologio finto
	body := example(t, "valid/no-changes.json")
	for i := 0; i < 10; i++ {
		if status, _ := post(t, s, body, nil); status != 200 {
			t.Fatalf("richiesta %d: %d", i, status)
		}
	}
	if status, out := post(t, s, body, nil); status != 429 || out["code"] != contract.CodeRateLimited {
		t.Fatalf("11a richiesta: %d %v", status, out["code"])
	}
	now = now.Add(time.Minute) // 60 all'ora: un gettone al minuto
	if status, _ := post(t, s, body, nil); status != 200 {
		t.Fatalf("dopo un minuto: %d", status)
	}
}

func TestRecordLevelRejection(t *testing.T) {
	s, d := newTestServer(t)
	var req map[string]any
	json.Unmarshal(example(t, "valid/full-sync.json"), &req)
	ann := req["upserts"].(map[string]any)["annotations"].([]any)[0].(map[string]any)
	ann["text"] = strings.Repeat("x", contract.MaxFieldChars+1)
	body, _ := json.Marshal(req)
	status, out := post(t, s, body, nil)
	if status != 200 {
		t.Fatalf("status=%d %v", status, out)
	}
	rej := out["rejected"].([]any)
	if len(rej) != 1 || rej[0].(map[string]any)["code"] != "field_too_long" {
		t.Fatalf("rejected=%v", rej)
	}
	// Il record rifiutato e' noto con il suo h: non torna in missing.
	missing := out["missing"].(map[string]any)["annotations"].([]any)
	for _, m := range missing {
		if m == ann["id"] {
			t.Fatal("record rifiutato richiesto di nuovo in missing")
		}
	}
	if d.Records[contract.Annotations][ann["id"].(string)].Raw != nil {
		t.Fatal("contenuto del record rifiutato salvato")
	}
}

func TestResponsesMatchSchema(t *testing.T) {
	v, err := NewValidator("sync-response.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	pv, err := NewValidator("problem.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServer(t)
	for _, name := range []string{"valid/full-sync.json", "valid/intermediate-batch.json", "valid/no-changes.json"} {
		r := httptest.NewRequest(http.MethodPost, contract.SyncPath, bytes.NewReader(example(t, name)))
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if errs := v.Validate(w.Body.Bytes()); w.Code != 200 || errs != nil {
			t.Errorf("%s: %d %v", name, w.Code, errs)
		}
	}
	r := httptest.NewRequest(http.MethodPost, contract.SyncPath, strings.NewReader("{"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if errs := pv.Validate(w.Body.Bytes()); errs != nil {
		t.Errorf("problem: %v", errs)
	}
}
