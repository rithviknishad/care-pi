// Package solutions embeds the solution files so that the agent works offline.
package solutions

import "embed"

// FS holds solutions/<name>/solution.yaml, the solution locks in solutions/<name>/locks/, and the
// schemas of the solution, the solution lock, and the index.
//
//go:embed */solution.yaml schema/*.schema.json */locks
var FS embed.FS

// Keys holds the minisign public keys that the agent trusts (keys/*.pub, Phase 7, docs/solutions.md section 18).
//
//go:embed keys
var Keys embed.FS
