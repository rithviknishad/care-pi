// Package quadlets embeds the unit templates that the agent renders.
// Template files in the templates directory use the .tmpl extension.
package quadlets

import "embed"

// FS holds the template files (*.tmpl). agent/internal/render parses them.
//
//go:embed all:templates
var FS embed.FS
