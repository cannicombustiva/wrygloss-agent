package ingestref

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// CleanWord applica la sezione 9 della specifica: NFC, rimozione di punteggiatura, simboli, spazi e caratteri di
// controllo o formato solo ai bordi, chiave in minuscolo senza lingua. clean vuoto significa parola "unusable".
// E' l'implementazione di riferimento dei vettori in spec/ingest/v1/examples/word-cleaning.json; l'app la
// reimplementa in TypeScript contro gli stessi vettori.
func CleanWord(raw string) (clean, key string) {
	clean = strings.TrimFunc(norm.NFC.String(raw), isEdge)
	return clean, cases.Lower(language.Und).String(clean)
}

func isEdge(r rune) bool {
	return unicode.In(r, unicode.P, unicode.S, unicode.Z, unicode.Cc, unicode.Cf)
}
