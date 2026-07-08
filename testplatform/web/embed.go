// Package web embeds the static single-page UI of the test platform.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js style.css
var content embed.FS

// FS returns the embedded web assets.
func FS() fs.FS {
	return content
}
