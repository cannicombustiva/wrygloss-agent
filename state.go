package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
)

// state is the agent's persistent state, in state.json next to the config. If it is missing or unreadable the agent
// starts from scratch: the server answers with missing and the first sync resends everything.
type state struct {
	Version      int                                 `json:"version"`
	TokenFP      string                              `json:"token_fp"`
	Acked        map[contract.Kind]map[string]string `json:"acked"` // id -> h acked by the server
	LastOK       int64                               `json:"last_ok"`
	MinIntervalS int                                 `json:"min_interval_s"`
	Blocked      *blocked                            `json:"blocked,omitempty"`
}

// blocked stops the agent after an error that retrying does not fix (revoked token, contract no longer accepted),
// until the thing that caused it changes: the token in the config or the agent version.
type blocked struct {
	Code         string `json:"code"`
	TokenFP      string `json:"token_fp"`
	AgentVersion string `json:"agent_version"`
	At           int64  `json:"at"`
}

func tokenFP(token string) string {
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:8])
}

func newState(fp string) *state {
	st := &state{Version: 1, TokenFP: fp, Acked: map[contract.Kind]map[string]string{}}
	for _, k := range contract.Kinds {
		st.Acked[k] = map[string]string{}
	}
	return st
}

// loadState returns the state for the current token; a different token is a new device for the server.
func loadState(path, fp string) (st *state, reset bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return newState(fp), false
	}
	st = &state{}
	if json.Unmarshal(b, st) != nil || st.Version != 1 {
		return newState(fp), true
	}
	if st.TokenFP != fp {
		return newState(fp), true
	}
	for _, k := range contract.Kinds {
		if st.Acked[k] == nil {
			if st.Acked == nil {
				st.Acked = map[contract.Kind]map[string]string{}
			}
			st.Acked[k] = map[string]string{}
		}
	}
	return st, false
}

// save writes atomically: temporary file, sync, rename.
func (st *state) save(path string) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
