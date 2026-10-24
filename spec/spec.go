// Package spec incorpora gli schemi e gli esempi del contratto, per i test e per il server di riferimento.
// L'agente non lo importa: gli schemi non finiscono nel binario per il dispositivo.
package spec

import "embed"

//go:embed ingest/v1/*.json ingest/v1/examples/*.json ingest/v1/examples/valid/*.json ingest/v1/examples/invalid/*.json
var FS embed.FS
