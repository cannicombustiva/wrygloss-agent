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

// failure is a non-200 outcome: Status 0 means a network error.
type failure struct {
	Status     int
	Code       string
	RetryAfter time.Duration
	Err        error
}

func (f *failure) Error() string {
	if f.Status == 0 {
		return fmt.Sprintf("network: %v", f.Err)
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
	deadline time.Time // no more retries past this instant
	wire     int       // bytes sent (compressed)
}

// post sends a request, retrying with growing waits on network errors, 5xx and 429 (spec 6.3).
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
		logf("retrying in %dms (%v)", wait.Milliseconds(), f)
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
	s.wire += n // only if the server answered: an attempt with a refused connection sends nothing
	rb, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, &failure{Err: err}
	}
	if res.StatusCode == http.StatusOK {
		var out contract.Response // unknown fields ignored: the agent is tolerant (spec 1.4)
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

// makeBatches splits the entries into batches within the budget. The last batch carries the manifest: if it does not
// fit, an empty final batch is added. There is always at least one batch, even without entries, to send the manifest.
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
	Missing  int // records still missing after the last round
	Resp     *contract.Response
}

// syncSnapshot sends the records that changed since the acked state, then the manifest; if the server reports
// missing records it resends them, for at most 2 extra rounds (spec 5.1). It updates st.Acked after every 200.
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
				// A single shrink: batches at a quarter, restarting from this batch.
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
