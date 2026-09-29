package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webAssets embed.FS

func (s *Server) registerWebUI() {
	sub, err := fs.Sub(webAssets, "web")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		body, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "web ui unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", fileServer))
}
