package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Operator UI static assets (vanilla HTML/JS/CSS). Canonical tree is also
// linked from repo-root ui/ → this directory.
//
//go:embed ui/*
var operatorUIFS embed.FS

func operatorUIHandler() http.Handler {
	sub, err := fs.Sub(operatorUIFS, "ui")
	if err != nil {
		// Should never happen with a successful embed.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusInternalServerError, "operator UI not embedded")
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve index for /ui and /ui/
		path := strings.TrimPrefix(r.URL.Path, "/ui")
		if path == "" || path == "/" {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = path
		fileServer.ServeHTTP(w, r2)
	})
}
