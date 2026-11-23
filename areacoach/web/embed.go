// Package web holds the user interface files.
package web

import "embed"

// FS contains the page, its style and its script.
//
//go:embed index.html app.css app.js
var FS embed.FS
