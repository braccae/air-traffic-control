package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPAHandler serves the embedded frontend.
//
// A request whose path matches a real file in the embedded build is served
// directly. Every other path falls back to index.html so client-side routes
// survive a hard refresh. Requests under /api/ and /ws never reach this handler
// because the server mux routes those to their own, more specific handlers.
func SPAHandler() http.Handler {
	return SPAHandlerFS(FS())
}

// SPAHandlerFS is SPAHandler over an explicit filesystem, which keeps the
// fallback and traversal rules testable without a real frontend build.
func SPAHandlerFS(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			serveIndex(w, assets)
			return
		}
		// fs.ValidPath also rejects any residual traversal segments, so a
		// request can never address a file outside the embedded build.
		if !fs.ValidPath(name) {
			serveIndex(w, assets)
			return
		}
		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			serveIndex(w, assets)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, assets fs.FS) {
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		// Reached only when the binary was built without running the frontend
		// build first. Say so plainly instead of returning a blank 500.
		http.Error(w, "frontend build not found in binary; run `just build-all`", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
