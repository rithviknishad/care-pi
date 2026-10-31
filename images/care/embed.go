// Package care embeds the Containerfile of the solution image. CI builds it (solution-image.yml),
// and the agent renders it for a local build (docs/solutions.md, section 13.1).
package care

import _ "embed"

// Containerfile is images/care/Containerfile.
//
//go:embed Containerfile
var Containerfile []byte
