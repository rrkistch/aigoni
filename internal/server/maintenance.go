package server

import (
	_ "embed"
	"html"
	"net/http"
	"strings"
)

//go:embed maintenance.html
var maintenanceHTML string

func (s *Server) maintenancePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_, _ = w.Write([]byte(strings.ReplaceAll(maintenanceHTML, "@@ADMIN@@", html.EscapeString(s.adminPath()))))
}
