// Package solutions embeds the solution files so that the agent works offline.
package solutions

import "embed"

// FS holds solutions/<name>/solution.yaml and the solution schema.
//
//go:embed */solution.yaml schema/solution.schema.json
var FS embed.FS
