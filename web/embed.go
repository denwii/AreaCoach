// Package web holds the user interface files.
package web

import "embed"

// FS contains index.html, styles, scripts and fonts.
//
//go:embed index.html app.css app.js charts.js fonts
var FS embed.FS
