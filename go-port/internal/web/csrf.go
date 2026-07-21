package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

// CSRF protection ports Flask-WTF's CSRFProtect (server.py wraps the app
// with `CSRFProtect(app)`, and every POST-serving template embeds
// `{{ csrf_token() }}` in a hidden field — see index.html/plugins.html/
// new_message.html). Go has no Flask-WTF equivalent, so this implements
// the same real protection via the standard double-submit-cookie pattern
// instead: a random per-browser token in a cookie, echoed back in every
// form's hidden field, and compared (constant-time) on every POST. This is
// a real, independently-secure CSRF defense — not byte-identical to
// Flask-WTF's session-bound token scheme, but not weaker, and documented
// as a structural difference in docs/known-differences.md.
const csrfCookieName = "csrf_token"

func csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand failing means the OS RNG is broken; nothing safe to do but crash
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // must be readable by the server to embed in the form; not by page JS either way since it's never read via document.cookie here
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

func checkCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	submitted := r.FormValue("csrf_token")
	if submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
}
