package web

import (
	"crypto/subtle"
	"net/http"
)

// withAuth ports handler.py's Handler.with_auth: HTTP Basic auth, gated by
// config['ui']['web']['auth'], comparing credentials with a
// constant-time compare exactly like Python's
// `secrets.compare_digest(u, username) and secrets.compare_digest(p, password)`.
func (s *Server) withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled {
			h(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || !constEq(u, s.username) || !constEq(p, s.password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Unauthorized"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func constEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
