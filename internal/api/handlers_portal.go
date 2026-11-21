package api

import (
	_ "embed"
	"net/http"
)

//go:embed portal.html
var portalHTML []byte

// handlePortal renders the developer portal and gateway landing page on GET /.
func (s *Server) handlePortal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(portalHTML)
}
