package ingestref

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// CleanWord applies section 9 of the spec: NFC, removal of punctuation, symbols, spaces and control or format
// characters at the edges only, lowercase key without a language. An empty clean means the word is "unusable".
// It is the reference implementation of the vectors in spec/ingest/v1/examples/word-cleaning.json; the app
// reimplements it in TypeScript against the same vectors.
func CleanWord(raw string) (clean, key string) {
	clean = strings.TrimFunc(norm.NFC.String(raw), isEdge)
	return clean, cases.Lower(language.Und).String(clean)
}

func isEdge(r rune) bool {
	return unicode.In(r, unicode.P, unicode.S, unicode.Z, unicode.Cc, unicode.Cf)
}
