// Package webui embeds the built React frontend so the Go daemon can serve the
// whole application without a Node process in the runtime path.
//
// The real assets are produced by `npm run build` at the repository root and
// copied into dist/ by the `build-all` justfile recipe. A placeholder
// index.html is committed so `go build` and `go test ./...` always succeed on a
// fresh clone that has never run a frontend build.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the embedded frontend rooted at the dist directory, so paths
// inside it are addressed as "index.html", "assets/...", and so on.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Unreachable while dist/ exists in the source tree, which the
		// committed placeholder guarantees.
		panic("webui: embedded dist directory is missing: " + err.Error())
	}
	return sub
}
