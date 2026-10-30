// Package console embeds the build output of the console SPA (dist/).
//
// "make console-build" runs "npm ci && npm run build" in this directory. Without a build,
// dist/ holds only .gitkeep, and the agent serves a page that tells how to build the console.
package console

import "embed"

// Dist holds dist/: index.html and assets/*.
//
//go:embed all:dist
var Dist embed.FS
