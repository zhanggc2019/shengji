package webui

import (
	"embed"
	"io/fs"
)

//go:embed static
var assets embed.FS

// Files gives both commands the same embedded browser assets.
func Files() fs.FS {
	files, _ := fs.Sub(assets, "static")
	return files
}
