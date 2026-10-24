package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
)

// failure e' un esito non 200: Status 0 indica un errore di rete.
type failure struct {
	Status     int
	Code       string
	RetryAfter time.Duration
	Err        error
}

func (f *failure) Error() string {
	if f.Status == 0 {
		return fmt.Sprintf("rete: %v", f.Err)
	}
	return fmt.Sprintf("status=%d code=%s", f.Status, f.Code)
}

func (f *failure) retryable() bool {
	return f.Status == 0 || f.Status == http.StatusTooManyRequests || f.Status >= 500
}

var backoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

type sender struct {
	client   *http.Client
	url      string
	token    string
	now      func() time.Time
	sleep    func(time.Duration)
	jitter   func(time.Duration) time.Duration
	deadline time.Time // oltre questo istante non si riprova piu'
	wire     int       // byte inviati (compressi)
}

// post invia una richiesta, riprovando con attese crescenti su errori di rete, 5xx e 429 (spec 6.3).
func (s *sender) post(req *contract.Request) (*contract.Response, error) {
	for attempt := 1; ; attempt++ {
		req.Run.Attempt = attempt
		resp, f := s.once(req)
		if f == nil {
			return resp, nil
		}
		if !f.retryable() || attempt > len(backoff) {
			return nil, f
		}
		wait := s.jitter(backoff[attempt-1])
		if f.RetryAfter > 0 {
			wait = f.RetryAfter
		}
		if s.now().Add(wait).After(s.deadline) {
			return nil, f
		}
		logf("riprovo tra %dms (%v)", wait.Milliseconds(), f)
		s.sleep(wait)
	}
}

func (s *sender) once(req *contract.Request) (*contract.Response, *failure) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, &failure{Status: -1, Code: "encode", Err: err}
	}
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	zw.Write(body)
	zw.Close()
	n := zb.Len()

	hr, err := http.NewRequest(http.MethodPost, s.url, &zb)
	if err != nil {
		return nil, &failure{Status: -1, Code: "request", Err: err}
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Content-Encoding", "gzip")
	hr.Header.Set("Authorization", "Bearer "+s.token)
	hr.Header.Set("User-Agent", "wrygloss-agent/"+version)
	res, err := s.client.Do(hr)
	if err != nil {
		return nil, &failure{Err: err}
	}
	defer res.Body.Close()
	s.wire += n // solo se il server ha risposto: un tentativo con connessione rifiutata non invia nulla
	rb, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, &failure{Err: err}
	}
	if res.StatusCode == http.StatusOK {
		var out contract.Response // campi sconosciuti ignorati: l'agente e' tollerante (spec 1.4)
		if err := json.Unmarshal(rb, &out); err != nil {
			return nil, &failure{Status: res.StatusCode, Code: "bad_response", Err: err}
		}
		return &out, nil
	}
	f := &failure{Status: res.StatusCode}
	var p contract.Problem
	if json.Unmarshal(rb, &p) == nil {
		f.Code = p.Code
	}
	if sec, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && sec > 0 {
		f.RetryAfter = time.Duration(sec) * time.Second
	}
	return nil, f
}

type entry struct {
	kind contract.Kind
	it   item
}

type budget struct{ records, bytes int }

var defaultBudget = budget{records: contract.MaxUpserts, bytes: 2 << 20}

// makeBatches divide le voci in lotti entro il budget. L'ultimo lotto porta il manifest: se non ci sta, si aggiunge
// un lotto finale vuoto. C'e' sempre almeno un lotto, anche senza voci, per inviare il manifest.
func makeBatches(entries []entry, b budget, manifestBytes int) [][]entry {
	var out [][]entry
	var cur []entry
	size := 0
	for _, e := range entries {
		if len(cur) > 0 && (len(cur) >= b.records || size+len(e.it.Raw) > b.bytes) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, e)
		size += len(e.it.Raw)
	}
	if len(cur) > 0 && size+manifestBytes > b.bytes {
		out = append(out, cur)
		cur = nil
	}
	return append(out, cur)
}

type syncResult struct {
	Sent     map[contract.Kind]int
	Deleted  map[contract.Kind]int
	Rejected int
	Batches  int
	Rounds   int
	Missing  int // record ancora mancanti dopo l'ultimo giro
	Resp     *contract.Response
}

// syncSnapshot invia i record cambiati rispetto allo stato confermato, poi il manifest; se il server segnala
// record mancanti li reinvia, al massimo per 2 giri in piu' (spec 5.1). Aggiorna st.Acked dopo ogni 200.
func syncSnapshot(s *sender, base contract.Request, snap *snapshot, st *state) (*syncResult, error) {
	res := &syncResult{Sent: map[contract.Kind]int{}, Deleted: map[contract.Kind]int{}}
	manifest := contract.Manifest{}
	index := map[contract.Kind]map[string]item{}
	manifestBytes := 0
	var pending []entry
	for _, k := range contract.Kinds {
		if !snap.Present[k] {
			continue
		}
		manifest[k] = map[string]string{}
		index[k] = map[string]item{}
		for _, it := range snap.Items[k] {
			if _, dup := manifest[k][it.ID]; dup {
				snap.warn("duplicate_id:" + string(k))
				continue
			}
			manifest[k][it.ID] = it.H
			index[k][it.ID] = it
			manifestBytes += len(it.ID) + len(it.H) + 6
			if st.Acked[k][it.ID] != it.H {
				pending = append(pending, entry{k, it})
			}
		}
	}

	b := defaultBudget
	shrunk := false
	for round := 0; round < 3; round++ {
		res.Rounds = round + 1
		batches := makeBatches(pending, b, manifestBytes)
		for i := 0; i < len(batches); i++ {
			final := i == len(batches)-1
			req := base
			req.Run.Batch = res.Batches + 1
			req.Run.Final = final
			req.Run.Warnings = append([]string{}, snap.Warnings...)
			req.Upserts = contract.Upserts{Annotations: []json.RawMessage{}, Words: []json.RawMessage{}, Books: []json.RawMessage{}}
			for _, e := range batches[i] {
				*req.Upserts.Of(e.kind) = append(*req.Upserts.Of(e.kind), e.it.Raw)
			}
			if final {
				req.Manifest = &manifest
			}
			resp, err := s.post(&req)
			if f, ok := err.(*failure); ok && f.Status == http.StatusRequestEntityTooLarge && !shrunk {
				// Una sola riduzione: lotti a un quarto, ripartendo da questo lotto.
				shrunk = true
				b = budget{records: max(1, b.records/4), bytes: max(1, b.bytes/4)}
				var rest []entry
				for _, bb := range batches[i:] {
					rest = append(rest, bb...)
				}
				batches = append(batches[:i], makeBatches(rest, b, manifestBytes)...)
				i--
				continue
			}
			if err != nil {
				return res, err
			}
			res.Batches++
			for _, e := range batches[i] {
				st.Acked[e.kind][e.it.ID] = e.it.H
				res.Sent[e.kind]++
			}
			res.Rejected += len(resp.Rejected)
			if final {
				res.Resp = resp
				for k, n := range resp.Deleted {
					res.Deleted[k] += n
				}
			}
		}
		for k, m := range manifest {
			for id := range st.Acked[k] {
				if _, ok := m[id]; !ok {
					delete(st.Acked[k], id)
				}
			}
		}
		pending = nil
		for k, ids := range res.Resp.Missing {
			for _, id := range ids {
				if it, ok := index[k][id]; ok {
					pending = append(pending, entry{k, it})
				}
			}
		}
		res.Missing = len(pending)
		if len(pending) == 0 {
			break
		}
	}
	return res, nil
}
