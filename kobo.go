package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
)

// columns is the fixed list of tables and columns that are read (spec, section 4). Nothing else is ever queried:
// in particular not the user table, not Bookmark.UserID and not content.___UserID.
var columns = map[string][]string{
	"Bookmark": {"BookmarkID", "VolumeID", "ContentID", "Type", "Text", "Annotation", "StartContainerPath",
		"EndContainerPath", "ChapterProgress", "Color", "Hidden", "DateCreated", "DateModified"},
	"WordList": {"Text", "VolumeId", "DictSuffix", "DateCreated"},
	"content":  {"ContentID", "ContentType", "Title", "Attribution", "Language", "ChapterIDBookmarked"},
}

// essential: without these columns the record kind is neither read nor put in the manifest (spec 4.4).
var essential = map[string][]string{
	"Bookmark": {"BookmarkID", "VolumeID"},
	"WordList": {"Text"},
	"content":  {"ContentID", "ContentType"},
}

const maxRecordBytes = 256 << 10

type item struct {
	ID  string
	H   string
	Raw json.RawMessage
}

type snapshot struct {
	Items    map[contract.Kind][]item
	Present  map[contract.Kind]bool // kinds that were read: only these go in the manifest
	Warnings []string
}

func (s *snapshot) warn(w string) {
	for _, x := range s.Warnings {
		if x == w {
			return
		}
	}
	s.Warnings = append(s.Warnings, w)
}

// add serializes the record once with an empty h for the fingerprint and once with h for sending.
func (s *snapshot) add(k contract.Kind, id string, rec any, setH func(string)) {
	b, err := json.Marshal(rec)
	if err != nil {
		s.warn("encode_error:" + string(k))
		return
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:8])
	setH(h)
	if b, err = json.Marshal(rec); err != nil {
		s.warn("encode_error:" + string(k))
		return
	}
	if len(b) > maxRecordBytes {
		s.warn("oversize:" + string(k))
		return
	}
	s.Items[k] = append(s.Items[k], item{ID: id, H: h, Raw: b})
}

type dbSchema struct {
	have map[string]map[string]bool
	snap *snapshot
}

func readSchema(db *sql.DB, snap *snapshot) (*dbSchema, error) {
	s := &dbSchema{have: map[string]map[string]bool{}, snap: snap}
	for table, cols := range columns {
		rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			return nil, err
		}
		have := map[string]bool{}
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				have[name] = true
			}
		}
		rows.Close()
		s.have[table] = have
		if len(have) == 0 {
			snap.warn("missing_table:" + table)
			continue
		}
		for _, c := range cols {
			if !have[c] {
				snap.warn("missing_column:" + table + "." + c)
			}
		}
	}
	return s, nil
}

func (s *dbSchema) usable(table string) bool {
	for _, c := range essential[table] {
		if !s.have[table][c] {
			return false
		}
	}
	return true
}

// col returns the SQL expression for a column of the fixed list, or NULL if it is missing.
func (s *dbSchema) col(table, alias, name string) string {
	if s.have[table][name] {
		return alias + `."` + name + `"`
	}
	return "NULL"
}

// readSnapshot reads the database copy: three queries, one per kind, plus the chapter title per annotation.
func readSnapshot(path string) (*snapshot, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=rw&_query_only=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var qc string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&qc); err != nil {
		return nil, fmt.Errorf("quick_check: %w", err)
	}
	if qc != "ok" {
		return nil, fmt.Errorf("quick_check: %s", qc)
	}

	snap := &snapshot{Items: map[contract.Kind][]item{}, Present: map[contract.Kind]bool{}}
	sch, err := readSchema(db, snap)
	if err != nil {
		return nil, err
	}
	referenced := map[string]bool{}

	if sch.usable("Bookmark") {
		chapter := "NULL"
		if sch.usable("content") && sch.have["content"]["Title"] && sch.have["Bookmark"]["ContentID"] {
			chapter = `(SELECT ch."Title" FROM content ch WHERE ch."ContentID" = b."ContentID" LIMIT 1)`
		}
		q := "SELECT " + strings.Join([]string{
			sch.col("Bookmark", "b", "BookmarkID"), sch.col("Bookmark", "b", "VolumeID"), sch.col("Bookmark", "b", "ContentID"),
			chapter, sch.col("Bookmark", "b", "Type"), sch.col("Bookmark", "b", "Text"), sch.col("Bookmark", "b", "Annotation"),
			sch.col("Bookmark", "b", "StartContainerPath"), sch.col("Bookmark", "b", "EndContainerPath"),
			sch.col("Bookmark", "b", "ChapterProgress"), sch.col("Bookmark", "b", "Color"), sch.col("Bookmark", "b", "Hidden"),
			sch.col("Bookmark", "b", "DateCreated"), sch.col("Bookmark", "b", "DateModified"),
		}, ", ") + " FROM Bookmark b"
		err := scanRows(db, q, nil, 14, func(v []any) {
			id := asID(v[0])
			if id == "" {
				return
			}
			a := &contract.Annotation{
				ID: id, VolumeID: asStr(v[1]), ContentID: asStr(v[2]), ChapterTitle: asStr(v[3]), Type: asStr(v[4]),
				Text: asStr(v[5]), Annotation: asStr(v[6]), StartPath: asStr(v[7]), EndPath: asStr(v[8]),
				ChapterProgress: asFloat(v[9]), Color: asInt(v[10]), Hidden: asBool(v[11]),
				Created: asStr(v[12]), Modified: asStr(v[13]),
			}
			if a.VolumeID != nil {
				referenced[*a.VolumeID] = true
			}
			snap.add(contract.Annotations, id, a, func(h string) { a.H = h })
		})
		if err != nil {
			return nil, fmt.Errorf("Bookmark: %w", err)
		}
		snap.Present[contract.Annotations] = true
	}

	if sch.usable("WordList") {
		q := "SELECT " + strings.Join([]string{sch.col("WordList", "w", "Text"), sch.col("WordList", "w", "VolumeId"),
			sch.col("WordList", "w", "DictSuffix"), sch.col("WordList", "w", "DateCreated")}, ", ") + " FROM WordList w"
		err := scanRows(db, q, nil, 4, func(v []any) {
			id := asID(v[0]) // original string, punctuation included: cleaning is the server's job
			if id == "" {
				return
			}
			w := &contract.Word{ID: id, VolumeID: asStr(v[1]), DictSuffix: asStr(v[2]), Created: asStr(v[3])}
			if w.VolumeID != nil {
				referenced[*w.VolumeID] = true
			}
			snap.add(contract.Words, id, w, func(h string) { w.H = h })
		})
		if err != nil {
			return nil, fmt.Errorf("WordList: %w", err)
		}
		snap.Present[contract.Words] = true
	}

	if sch.usable("content") {
		// Only cited books: the query filters by id, so the rest of the library is never even read.
		ids := make([]any, 0, len(referenced))
		for id := range referenced {
			ids = append(ids, id)
		}
		sel := "SELECT " + strings.Join([]string{sch.col("content", "c", "ContentID"), sch.col("content", "c", "Title"),
			sch.col("content", "c", "Attribution"), sch.col("content", "c", "Language"),
			sch.col("content", "c", "ChapterIDBookmarked")}, ", ") + ` FROM content c WHERE c."ContentType" = 6 AND c."ContentID" IN (`
		for start := 0; start < len(ids); start += 500 {
			chunk := ids[start:min(start+500, len(ids))]
			q := sel + strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + ")"
			err := scanRows(db, q, chunk, 5, func(v []any) {
				id := asID(v[0])
				if id == "" {
					return
				}
				b := &contract.Book{ID: id, Title: asStr(v[1]), Author: asStr(v[2]), Language: asStr(v[3]), Position: asStr(v[4])}
				snap.add(contract.Books, id, b, func(h string) { b.H = h })
			})
			if err != nil {
				return nil, fmt.Errorf("content: %w", err)
			}
		}
		snap.Present[contract.Books] = true
	}
	return snap, nil
}

func scanRows(db *sql.DB, q string, args []any, n int, fn func([]any)) error {
	rows, err := db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	vals := make([]any, n)
	ptrs := make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		fn(vals)
	}
	return rows.Err()
}

func asID(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

func asStr(v any) *string {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case []byte:
		s = string(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	case float64:
		s = strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time: // the driver converts columns declared DATE/DATETIME/TIMESTAMP
		s = x.Format("2006-01-02T15:04:05.000")
	}
	if s == "" {
		return nil
	}
	return &s
}

func asFloat(v any) *float64 {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case int64:
		f = float64(x)
	case string:
		p, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return nil
		}
		f = p
	default:
		return nil
	}
	return &f
}

func asInt(v any) *int64 {
	var i int64
	switch x := v.(type) {
	case int64:
		i = x
	case float64:
		i = int64(x)
	case string:
		p, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return nil
		}
		i = p
	default:
		return nil
	}
	return &i
}

// asBool normalizes Hidden, which the device may store as 0/1 or as the text "true"/"false".
func asBool(v any) *bool {
	var b bool
	switch x := v.(type) {
	case bool:
		b = x
	case int64:
		b = x != 0
	case string:
		switch strings.ToLower(x) {
		case "true", "1":
			b = true
		case "false", "0":
		default:
			return nil
		}
	default:
		return nil
	}
	return &b
}

// readDevice reads only firmware and model from .kobo/version. The first field (serial number) is discarded.
func readDevice(path, arch string) contract.Device {
	d := contract.Device{Arch: arch}
	b, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	f := strings.Split(strings.TrimSpace(string(b)), ",")
	if len(f) >= 3 && f[2] != "" && len(f[2]) <= 32 {
		d.Firmware = &f[2]
	}
	if len(f) >= 6 && f[5] != "" && len(f[5]) <= 64 {
		d.ModelID = &f[5]
	}
	return d
}

type fileStat struct {
	exists bool
	size   int64
	mtime  int64
}

var sidecarSuffixes = []string{"-wal", "-journal"}

func statDB(src string) [3]fileStat {
	var out [3]fileStat
	for i, p := range []string{src, src + sidecarSuffixes[0], src + sidecarSuffixes[1]} {
		if st, err := os.Stat(p); err == nil {
			out[i] = fileStat{true, st.Size(), st.ModTime().UnixNano()}
		}
	}
	return out
}

// copyStable copies the database and its sidecar files without ever opening the original. If the size or mtime of a
// file changes during the copy (Nickel wrote), it retries: an inconsistent copy can produce a stale manifest and
// therefore wrong deletions. The -shm is not copied: SQLite rebuilds it from the WAL on the copy.
func copyStable(src, dst string, sleep func(time.Duration)) (attempts int, err error) {
	for attempts = 1; attempts <= 3; attempts++ {
		removeCopy(dst)
		before := statDB(src)
		if err = copyFile(src, dst); err != nil {
			return attempts, err
		}
		for i, suf := range sidecarSuffixes {
			if before[i+1].exists {
				if err = copyFile(src+suf, dst+suf); err != nil {
					return attempts, err
				}
			}
		}
		if statDB(src) == before {
			return attempts, nil
		}
		sleep(500 * time.Millisecond)
	}
	removeCopy(dst)
	return attempts - 1, fmt.Errorf("database modified during the copy")
}

func removeCopy(dst string) {
	for _, p := range []string{dst, dst + "-wal", dst + "-shm", dst + "-journal"} {
		os.Remove(p)
	}
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
