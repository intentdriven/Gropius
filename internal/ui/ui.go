// Package ui embeds the web control panel.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var assets embed.FS

// Handler serves the control panel.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		// The assets are compiled in; a failure here is a build error, not a
		// runtime condition.
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
