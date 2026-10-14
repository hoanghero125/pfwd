// Package web holds pfwd's web UI, built into web/dist and embedded in the
// binary for the daemon to serve. The source is in web/src; `make web`
// rebuilds dist, which is committed so Go builds need no Node.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built app, with index.html at its root.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a valid path, so Sub can't fail
	}
	return sub
}
