// Package contract holds the types of the wrygloss.ingest v1 contract (see spec/ingest/v1/README.md).
// Records travel as json.RawMessage: the agent serializes them once (for the fingerprint and for sending), the
// server decodes them record by record so it can reject a single record rather than the whole request.
package contract

import "encoding/json"

const (
	Schema   = "1.0"
	SyncPath = "/api/ingest/v1/sync"
)

type Kind string

const (
	Annotations Kind = "annotations"
	Words       Kind = "words"
	Books       Kind = "books"
)

var Kinds = []Kind{Annotations, Words, Books}

// Limits from section 7.
const (
	MaxWireBytes       = 1 << 20
	MaxBodyBytes       = 8 << 20
	MaxUpserts         = 1000
	MaxManifestPerKind = 50000
	MaxIDChars         = 512
	MaxFieldChars      = 32768
)

type Request struct {
	Schema   string    `json:"schema"`
	Run      Run       `json:"run"`
	Device   Device    `json:"device"`
	Upserts  Upserts   `json:"upserts"`
	Manifest *Manifest `json:"manifest,omitempty"`
}

type Run struct {
	ID           string   `json:"id"`
	AgentVersion string   `json:"agent_version"`
	StartedAt    string   `json:"started_at"`
	RouteAtEvent bool     `json:"route_at_event"`
	RouteReadyMS *int64   `json:"route_ready_ms"`
	Attempt      int      `json:"attempt"`
	Batch        int      `json:"batch"`
	Final        bool     `json:"final"`
	Warnings     []string `json:"warnings"`
}

type Device struct {
	Firmware *string `json:"firmware"`
	ModelID  *string `json:"model_id"`
	Arch     string  `json:"arch"`
}

type Upserts struct {
	Annotations []json.RawMessage `json:"annotations"`
	Words       []json.RawMessage `json:"words"`
	Books       []json.RawMessage `json:"books"`
}

// Of returns the array for the given kind.
func (u *Upserts) Of(k Kind) *[]json.RawMessage {
	switch k {
	case Annotations:
		return &u.Annotations
	case Words:
		return &u.Words
	default:
		return &u.Books
	}
}

// Manifest: for each kind present, id -> h of every record on the device. An absent kind is left untouched.
type Manifest map[Kind]map[string]string

type Annotation struct {
	ID              string   `json:"id"`
	H               string   `json:"h"`
	VolumeID        *string  `json:"volume_id"`
	ContentID       *string  `json:"content_id"`
	ChapterTitle    *string  `json:"chapter_title"`
	Type            *string  `json:"type"`
	Text            *string  `json:"text"`
	Annotation      *string  `json:"annotation"`
	StartPath       *string  `json:"start_path"`
	EndPath         *string  `json:"end_path"`
	ChapterProgress *float64 `json:"chapter_progress"`
	Color           *int64   `json:"color"`
	Hidden          *bool    `json:"hidden"`
	Created         *string  `json:"created"`
	Modified        *string  `json:"modified"`
}

type Word struct {
	ID         string  `json:"id"`
	H          string  `json:"h"`
	VolumeID   *string `json:"volume_id"`
	DictSuffix *string `json:"dict_suffix"`
	Created    *string `json:"created"`
}

type Book struct {
	ID       string  `json:"id"`
	H        string  `json:"h"`
	Title    *string `json:"title"`
	Author   *string `json:"author"`
	Language *string `json:"language"`
	Position *string `json:"position"`
}

type Response struct {
	Schema       string            `json:"schema"`
	ServerTime   string            `json:"server_time"`
	Missing      map[Kind][]string `json:"missing"`
	Rejected     []Rejection       `json:"rejected"`
	Deleted      map[Kind]int      `json:"deleted"`
	MinIntervalS int               `json:"min_interval_s"`
	LatestAgent  *string           `json:"latest_agent"`
}

type Rejection struct {
	Kind    Kind   `json:"kind"`
	ID      string `json:"id"`
	Code    string `json:"code"`
	Pointer string `json:"pointer,omitempty"`
}

type Problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Code   string       `json:"code"`
	Detail string       `json:"detail,omitempty"`
	Errors []FieldError `json:"errors,omitempty"`
}

type FieldError struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

// Stable error codes (section 6.3).
const (
	CodeMalformedJSON     = "malformed_json"
	CodeBadEncoding       = "bad_encoding"
	CodeInvalidToken      = "invalid_token"
	CodeTokenRevoked      = "token_revoked"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeSchemaUnsupported = "schema_unsupported"
	CodePayloadTooLarge   = "payload_too_large"
	CodeUnsupportedMedia  = "unsupported_media_type"
	CodeSchemaViolation   = "schema_violation"
	CodeRateLimited       = "rate_limited"
	CodeInternal          = "internal"
)
