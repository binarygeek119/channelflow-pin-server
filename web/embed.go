package webui

import "embed"

// FS is the tester UI (index.html, app.js, style.css).
//
//go:embed index.html app.js style.css
var FS embed.FS
