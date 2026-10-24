// Package spec embeds the contract's schemas and examples, for the tests and the reference server.
// The agent does not import it: the schemas do not end up in the device binary.
package spec

import "embed"

//go:embed ingest/v1/*.json ingest/v1/examples/*.json ingest/v1/examples/valid/*.json ingest/v1/examples/invalid/*.json
var FS embed.FS
