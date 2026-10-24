package ingestref

import (
	"encoding/json"
	"io/fs"
	"path"
	"testing"

	"github.com/cannicombustiva/wrygloss-agent/spec"
)

func TestExamplesAgainstSchema(t *testing.T) {
	v, err := NewValidator("sync-request.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"valid", "invalid"} {
		files, _ := fs.Glob(spec.FS, "ingest/v1/examples/"+dir+"/*.json")
		if len(files) == 0 {
			t.Fatalf("nessun esempio in %s", dir)
		}
		for _, f := range files {
			b, _ := spec.FS.ReadFile(f)
			errs := v.Validate(b)
			if dir == "valid" && errs != nil {
				t.Errorf("%s dovrebbe essere valido: %v", path.Base(f), errs)
			}
			if dir == "invalid" && errs == nil {
				t.Errorf("%s dovrebbe essere non valido", path.Base(f))
			}
		}
	}
}

func TestWordCleaningVectors(t *testing.T) {
	b, err := spec.FS.ReadFile("ingest/v1/examples/word-cleaning.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct{ Raw, Clean, Key string }
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Cases {
		clean, key := CleanWord(c.Raw)
		if clean != c.Clean || key != c.Key {
			t.Errorf("CleanWord(%q) = %q, %q; attesi %q, %q", c.Raw, clean, key, c.Clean, c.Key)
		}
	}
}
