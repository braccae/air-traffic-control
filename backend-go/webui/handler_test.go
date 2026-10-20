package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testAssets mirrors the shape of a real Vite build without depending on one.
func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<!doctype html><div id=root></div>")},
		"ghostty-vt.wasm":      &fstest.MapFile{Data: []byte("\x00asm")},
		"assets/index-abc.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
		"assets/index-abc.css": &fstest.MapFile{Data: []byte("body{}")},
	}
}

func TestFS_LoadsWithoutFrontendBuild(t *testing.T) {
	// A fresh clone has no Vite output. The embedded tree must still load, or
	// every `go build` and `go test ./...` would fail before a frontend build.
	assets := FS()
	if _, err := fs.Stat(assets, "."); err != nil {
		t.Fatalf("embedded dist must be a usable filesystem: %v", err)
	}
}

func TestSPAHandler_ServesRealAsset(t *testing.T) {
	h := SPAHandlerFS(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/ghostty-vt.wasm", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a real asset, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "\x00asm" {
		t.Fatalf("expected the asset bytes to be served verbatim, got %q", got)
	}
}

func TestSPAHandler_ServesNestedAsset(t *testing.T) {
	h := SPAHandlerFS(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a nested asset, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("expected the nested asset body, got %q", rec.Body.String())
	}
}

func TestSPAHandler_FallsBackToIndexForUnknownRoute(t *testing.T) {
	h := SPAHandlerFS(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/nodes/datacenter/zfs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected html content type, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `id=root`) {
		t.Fatalf("expected index.html body, got %q", rec.Body.String())
	}
}

func TestSPAHandler_ServesIndexAtRoot(t *testing.T) {
	h := SPAHandlerFS(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 at root, got %d", rec.Code)
	}
}

func TestSPAHandler_DirectoryRequestFallsBackToIndex(t *testing.T) {
	h := SPAHandlerFS(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/assets/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id=root`) {
		t.Fatalf("directory requests must fall back to index.html, got %q", rec.Body.String())
	}
}

func TestSPAHandler_DoesNotEscapeEmbeddedRoot(t *testing.T) {
	h := SPAHandlerFS(testAssets())
	req := httptest.NewRequest(http.MethodGet, "/../../../../etc/passwd", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("path traversal must not read files outside the embedded build")
	}
}

func TestSPAHandler_MissingIndexReportsBuildProblem(t *testing.T) {
	assets := fstest.MapFS{
		"assets/index-abc.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	h := SPAHandlerFS(assets)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when index.html is absent, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "build-all") {
		t.Fatalf("error should tell the operator how to fix it, got %q", rec.Body.String())
	}
}
