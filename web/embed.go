// Package web embeds the complete console. No frontend runtime, CDN or build
// tool is required on the deployed machine.
package web

import (
	"bytes"
	"embed"
	"net/http"
	"time"
)

//go:embed assets/*
var files embed.FS

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		name := ""
		contentType := ""
		switch r.URL.Path {
		case "/":
			name = "index.html"
			contentType = "text/html; charset=utf-8"
		case "/assets/app.js":
			name = "app.js"
			contentType = "text/javascript; charset=utf-8"
		case "/assets/naming.js":
			name = "naming.js"
			contentType = "text/javascript; charset=utf-8"
		case "/assets/style.css":
			name = "style.css"
			contentType = "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := files.ReadFile("assets/" + name)
		if err != nil {
			http.Error(w, "asset_unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
