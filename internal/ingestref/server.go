// Package ingestref e' un'implementazione di riferimento, in memoria, del server di ingest v1.
// Serve ai test dell'agente e a cmd/devserver per le prove in rete locale. Non e' il server di produzione:
// non pulisce le parole e non conosce account. Valida le richieste contro il JSON Schema della specifica.
package ingestref

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
)

type Record struct {
	H        string
	Raw      json.RawMessage // nil se rifiutato
	Deleted  bool
	Rejected string
}

type Device struct {
	Revoked  bool
	Records  map[contract.Kind]map[string]*Record
	Requests []contract.Request // richieste accettate (200), per i test

	tokens     float64
	lastRefill time.Time
}

// Live conta i record non cancellati di un tipo.
func (d *Device) Live(k contract.Kind) int {
	n := 0
	for _, r := range d.Records[k] {
		if !r.Deleted {
			n++
		}
	}
	return n
}

type Server struct {
	Now          func() time.Time
	LatestAgent  string
	MinIntervalS int
	RatePerHour  float64
	Burst        float64
	// Log, se impostato, riceve una riga per richiesta.
	Log func(format string, a ...any)

	mu        sync.Mutex
	devices   map[string]*Device // per SHA-256 del token
	validator *Validator
}

func New() (*Server, error) {
	v, err := NewValidator("sync-request.schema.json")
	if err != nil {
		return nil, err
	}
	return &Server{
		Now:         time.Now,
		RatePerHour: 60,
		Burst:       10,
		devices:     map[string]*Device{},
		validator:   v,
	}, nil
}

func tokenHash(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// AddDevice registra un dispositivo con il suo token (il server conserva solo l'impronta).
func (s *Server) AddDevice(token string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := &Device{Records: map[contract.Kind]map[string]*Record{}}
	for _, k := range contract.Kinds {
		d.Records[k] = map[string]*Record{}
	}
	d.tokens = s.Burst
	d.lastRefill = s.Now()
	s.devices[tokenHash(token)] = d
	return d
}

var minorRe = regexp.MustCompile(`^1\.[0-9]+$`)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock() // serializza le richieste (sezione 5.3); in memoria basta un solo lock
	defer s.mu.Unlock()
	status, body := s.handle(r)
	w.Header().Set("Content-Type", "application/json")
	if p, ok := body.(*contract.Problem); ok {
		w.Header().Set("Content-Type", "application/problem+json")
		if p.Code == contract.CodeRateLimited {
			w.Header().Set("Retry-After", "60")
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func problem(status int, code, detail string, errs ...contract.FieldError) (int, any) {
	return status, &contract.Problem{
		Type: "urn:wrygloss:problem:" + code, Title: strings.ReplaceAll(code, "_", " "),
		Status: status, Code: code, Detail: detail, Errors: errs,
	}
}

func (s *Server) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log(format, a...)
	}
}

func (s *Server) handle(r *http.Request) (int, any) {
	if r.Method != http.MethodPost {
		return problem(http.StatusMethodNotAllowed, contract.CodeMethodNotAllowed, "")
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	d := s.devices[tokenHash(tok)]
	if !ok || d == nil {
		return problem(http.StatusUnauthorized, contract.CodeInvalidToken, "")
	}
	if d.Revoked {
		return problem(http.StatusUnauthorized, contract.CodeTokenRevoked, "")
	}
	if !s.allow(d) {
		return problem(http.StatusTooManyRequests, contract.CodeRateLimited, "")
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		return problem(http.StatusUnsupportedMediaType, contract.CodeUnsupportedMedia, "Content-Type")
	}

	body, status, code := readBody(r)
	if code != "" {
		return problem(status, code, "")
	}

	var head struct {
		Schema any `json:"schema"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return problem(http.StatusBadRequest, contract.CodeMalformedJSON, err.Error())
	}
	if v, ok := head.Schema.(string); ok && minorRe.MatchString(v) && v != contract.Schema {
		return problem(http.StatusGone, contract.CodeSchemaUnsupported, "accepted: "+contract.Schema)
	}
	if errs := s.validator.Validate(body); errs != nil {
		return problem(http.StatusUnprocessableEntity, contract.CodeSchemaViolation, "", errs...)
	}
	var req contract.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return problem(http.StatusBadRequest, contract.CodeMalformedJSON, err.Error())
	}
	if n := len(req.Upserts.Annotations) + len(req.Upserts.Words) + len(req.Upserts.Books); n > contract.MaxUpserts {
		return problem(http.StatusUnprocessableEntity, contract.CodeSchemaViolation, "",
			contract.FieldError{Pointer: "/upserts", Message: fmt.Sprintf("%d record, massimo %d", n, contract.MaxUpserts)})
	}

	resp := s.apply(d, &req)
	d.Requests = append(d.Requests, req)
	s.logf("run=%s batch=%d attempt=%d final=%v upserts=%d/%d/%d missing=%d/%d/%d deleted=%d/%d/%d rejected=%d route_at_event=%v route_ready_ms=%v warnings=%v",
		req.Run.ID, req.Run.Batch, req.Run.Attempt, req.Run.Final,
		len(req.Upserts.Annotations), len(req.Upserts.Words), len(req.Upserts.Books),
		len(resp.Missing[contract.Annotations]), len(resp.Missing[contract.Words]), len(resp.Missing[contract.Books]),
		resp.Deleted[contract.Annotations], resp.Deleted[contract.Words], resp.Deleted[contract.Books],
		len(resp.Rejected), req.Run.RouteAtEvent, ptrStr(req.Run.RouteReadyMS), req.Run.Warnings)
	return http.StatusOK, resp
}

func ptrStr(p *int64) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprint(*p)
}

// readBody applica i limiti sul filo e decompressi (sezione 7).
func readBody(r *http.Request) ([]byte, int, string) {
	wire := io.LimitReader(r.Body, contract.MaxWireBytes+1)
	var src io.Reader = wire
	counted := &countingReader{r: wire}
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "":
		src = counted
	case "gzip":
		zr, err := gzip.NewReader(counted)
		if err != nil {
			if counted.n > contract.MaxWireBytes {
				return nil, http.StatusRequestEntityTooLarge, contract.CodePayloadTooLarge
			}
			return nil, http.StatusBadRequest, contract.CodeBadEncoding
		}
		src = zr
	default:
		return nil, http.StatusUnsupportedMediaType, contract.CodeUnsupportedMedia
	}
	b, err := io.ReadAll(io.LimitReader(src, contract.MaxBodyBytes+1))
	if counted.n > contract.MaxWireBytes || len(b) > contract.MaxBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, contract.CodePayloadTooLarge
	}
	if err != nil {
		return nil, http.StatusBadRequest, contract.CodeBadEncoding
	}
	return b, 0, ""
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (s *Server) allow(d *Device) bool {
	now := s.Now()
	d.tokens = math.Min(s.Burst, d.tokens+now.Sub(d.lastRefill).Hours()*s.RatePerHour)
	d.lastRefill = now
	if d.tokens < 1 {
		return false
	}
	d.tokens--
	return true
}

// apply applica upserts e, nel lotto finale, il manifest (sezioni 5.2 e 6.2).
func (s *Server) apply(d *Device, req *contract.Request) *contract.Response {
	resp := &contract.Response{
		Schema:       contract.Schema,
		ServerTime:   s.Now().UTC().Format(time.RFC3339),
		Missing:      map[contract.Kind][]string{},
		Rejected:     []contract.Rejection{},
		Deleted:      map[contract.Kind]int{},
		MinIntervalS: s.MinIntervalS,
	}
	if s.LatestAgent != "" {
		resp.LatestAgent = &s.LatestAgent
	}
	for _, k := range contract.Kinds {
		resp.Missing[k] = []string{}
		resp.Deleted[k] = 0
		for _, raw := range *req.Upserts.Of(k) {
			id, h, code, ptr := checkRecord(raw)
			rec := &Record{H: h, Raw: raw}
			if code != "" {
				rec = &Record{H: h, Rejected: code}
				resp.Rejected = append(resp.Rejected, contract.Rejection{Kind: k, ID: id, Code: code, Pointer: ptr})
			}
			d.Records[k][id] = rec
		}
	}
	if req.Manifest == nil {
		return resp
	}
	for k, man := range *req.Manifest {
		recs := d.Records[k]
		for id, r := range recs {
			if _, ok := man[id]; !ok && !r.Deleted {
				r.Deleted = true
				resp.Deleted[k]++
			}
		}
		for id, h := range man {
			r := recs[id]
			switch {
			case r == nil || r.H != h:
				resp.Missing[k] = append(resp.Missing[k], id)
			case r.Deleted:
				r.Deleted = false
			}
		}
		sort.Strings(resp.Missing[k])
	}
	return resp
}

// checkRecord estrae id e h e controlla i limiti di valore. La struttura e' gia' stata validata dallo schema.
func checkRecord(raw json.RawMessage) (id, h, code, pointer string) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "", "invalid_value", ""
	}
	id, _ = m["id"].(string)
	h, _ = m["h"].(string)
	if utf8.RuneCountInString(id) > contract.MaxIDChars {
		return id, h, "id_too_long", "/id"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := m[k].(string); ok && k != "id" && utf8.RuneCountInString(v) > contract.MaxFieldChars {
			return id, h, "field_too_long", "/" + k
		}
	}
	return id, h, "", ""
}
