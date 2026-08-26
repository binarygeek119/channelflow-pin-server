package webui

import "embed"

// FS is the tester UI and ChannelFlow brand assets.
//
//go:embed index.html app.js style.css site.webmanifest favicon.ico *.png
var FS embed.FS
