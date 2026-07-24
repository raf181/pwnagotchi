package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

// registerRoutes ports Handler.__init__'s add_url_rule calls 1:1.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.Handle("/css/", http.StripPrefix("/css", http.FileServer(subFS("static/css"))))
	mux.Handle("/js/", http.StripPrefix("/js", http.FileServer(subFS("static/js"))))
	mux.Handle("/fonts/", http.StripPrefix("/fonts", http.FileServer(subFS("static/fonts"))))
	mux.Handle("/images/", http.StripPrefix("/images", http.FileServer(subFS("static/images"))))
	mux.Handle("/svg/", http.StripPrefix("/svg", http.FileServer(subFS("static/svg"))))

	mux.HandleFunc("/css/theme.css", s.dynamicTheme)

	mux.HandleFunc("/", s.withAuth(s.index))
	mux.HandleFunc("/ui", s.withAuth(s.ui))

	mux.HandleFunc("/shutdown", s.withAuth(s.requirePOST(s.shutdown)))
	mux.HandleFunc("/reboot", s.withAuth(s.requirePOST(s.reboot)))
	mux.HandleFunc("/restart", s.withAuth(s.requirePOST(s.restart)))

	mux.HandleFunc("/inbox", s.withAuth(s.inbox))
	mux.HandleFunc("/inbox/profile", s.withAuth(s.inboxProfile))
	mux.HandleFunc("/inbox/peers", s.withAuth(s.inboxPeers))
	mux.HandleFunc("/inbox/new", s.withAuth(s.newMessage))
	mux.HandleFunc("/inbox/send", s.withAuth(s.requirePOST(s.sendMessage)))
	mux.HandleFunc("/inbox/", s.withAuth(s.inboxSubpath)) // /inbox/<id> and /inbox/<id>/<mark>

	mux.HandleFunc("/plugins", s.withAuth(s.pluginsIndex))
	mux.HandleFunc("/plugins/", s.withAuth(s.pluginsSubpath))
}

func subFS(dir string) http.FileSystem {
	sub, err := fs.Sub(staticFS, dir)
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

func (s *Server) requirePOST(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func (s *Server) mode() string {
	if s.agent == nil {
		return "auto"
	}
	return s.agent.Mode()
}

func (s *Server) fingerprint() string {
	if s.agent == nil {
		return ""
	}
	return s.agent.GetFingerprint()
}

func (s *Server) otherMode() string {
	if s.mode() == "manual" {
		return "AUTO"
	}
	return "MANU"
}

// index ports Handler.index.
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "index", map[string]interface{}{
		"title":       s.name,
		"other_mode":  s.otherMode(),
		"fingerprint": s.fingerprint(),
		"active_page": "home",
	})
}

// ui ports Handler.ui: serves the real, current device-display PNG frame.
func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	data, err := readFrame()
	if err != nil {
		http.Error(w, "no frame yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", FrameCType)
	w.Write(data)
}

// dynamicTheme ports Handler.dynamic_theme.
func (s *Server) dynamicTheme(w http.ResponseWriter, r *http.Request) {
	css := fmt.Sprintf(":root {\n  --accent: rgb(%d, %d, %d);\n  --accent-r: %d;\n  --accent-g: %d;\n  --accent-b: %d;\n}",
		s.accentR, s.accentG, s.accentB, s.accentR, s.accentG, s.accentB)
	w.Header().Set("Content-Type", "text/css")
	w.Write([]byte(css))
}

func (s *Server) statusPage(w http.ResponseWriter, goBackAfter int, message string) {
	body, err := renderStatus(map[string]interface{}{
		"title":         s.name,
		"go_back_after": goBackAfter,
		"message":       message,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body)
}

// shutdown ports Handler.shutdown: real system shutdown, run in the
// background exactly like Python's `threading.Thread(target=pwnagotchi.shutdown,
// daemon=True).start()` inside its `finally:` block (the response is
// served BEFORE the action completes, matching Python precisely).
func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
		return
	}
	s.statusPage(w, 60, "Shutting down ...")
	go func() {
		if err := s.actions.Shutdown(); err != nil {
			// Real Python's threading.Thread target has nowhere to report
			// an error either (its own os.system calls don't raise); a
			// clear log line is strictly more visibility than Python gives.
			fmt.Println("web: shutdown:", err)
		}
	}()
}

// reboot ports Handler.reboot.
func (s *Server) reboot(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
		return
	}
	s.statusPage(w, 60, "Rebooting ...")
	go func() {
		if err := s.actions.Reboot(""); err != nil {
			fmt.Println("web: reboot:", err)
		}
	}()
}

// restart ports Handler.restart.
func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
		return
	}
	mode := r.FormValue("mode")
	if mode != "AUTO" && mode != "MANU" {
		mode = "MANU"
	}
	s.statusPage(w, 30, fmt.Sprintf("Restarting in %s mode ...", mode))
	go func() {
		if err := s.actions.Restart(mode); err != nil {
			fmt.Println("web: restart:", err)
		}
	}()
}

// render wraps renderTemplate, injecting the csrf_token every page
// template may reference (Jinja's `csrf_token()` global).
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	data["csrf_token"] = csrfToken(w, r)
	body, err := renderTemplate(name, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body)
}

// inbox ports Handler.inbox.
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	page := 1
	if v := r.URL.Query().Get("p"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			page = n
		}
	}

	pages, records := 1, 0
	var messages []map[string]interface{}
	var errMsg string

	if s.grid == nil || !s.grid.IsConnected() {
		errMsg = "not connected"
	} else if result, err := s.grid.Inbox(page, true); err != nil {
		errMsg = err.Error()
	} else if m, ok := result.(map[string]interface{}); ok {
		pages = intFromJSON(m["pages"], 1)
		records = intFromJSON(m["records"], 0)
		if raw, ok := m["messages"].([]interface{}); ok {
			for _, item := range raw {
				if mm, ok := item.(map[string]interface{}); ok {
					messages = append(messages, mm)
				}
			}
		}
	}

	s.render(w, r, "inbox", map[string]interface{}{
		"name":           s.name,
		"page":           page,
		"error":          errMsg,
		"inbox_pages":    pages,
		"inbox_records":  records,
		"inbox_messages": messages,
		"active_page":    "inbox",
	})
}

// inboxProfile ports Handler.inbox_profile.
func (s *Server) inboxProfile(w http.ResponseWriter, r *http.Request) {
	var data interface{}
	var errMsg string
	if s.grid == nil {
		errMsg = "grid client unavailable"
	} else if result, err := s.grid.GetAdvertisementData(); err != nil {
		errMsg = err.Error()
	} else {
		data = result
	}
	s.render(w, r, "profile", map[string]interface{}{
		"name":        s.name,
		"fingerprint": s.fingerprint(),
		"data":        jsonIndent(data),
		"error":       errMsg,
		"active_page": "profile",
	})
}

// inboxPeers ports Handler.inbox_peers.
func (s *Server) inboxPeers(w http.ResponseWriter, r *http.Request) {
	var rows []map[string]interface{}
	var errMsg string
	if s.grid == nil {
		errMsg = "grid client unavailable"
	} else if result, err := s.grid.Memory(); err != nil {
		errMsg = err.Error()
	} else {
		rows = normalizePeers(result)
	}
	s.render(w, r, "peers", map[string]interface{}{
		"name":        s.name,
		"peers":       rows,
		"error":       errMsg,
		"active_page": "peers",
	})
}

// normalizePeers accepts either a JSON array of peer records or a JSON
// object keyed by peer id (real pwngrid /mesh/memory response shape isn't
// pinned down by an existing test — see docs/known-differences.md), and
// flattens each peer's nested "advertisement" fields for template use.
func normalizePeers(result interface{}) []map[string]interface{} {
	var raw []interface{}
	switch v := result.(type) {
	case []interface{}:
		raw = v
	case map[string]interface{}:
		for _, val := range v {
			raw = append(raw, val)
		}
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		row := map[string]interface{}{
			"fingerprint": m["fingerprint"],
			"encounters":  m["encounters"],
		}
		if adv, ok := m["advertisement"].(map[string]interface{}); ok {
			row["advertisement_face"] = adv["face"]
			row["advertisement_name"] = adv["name"]
			row["advertisement_pwnd_tot"] = adv["pwnd_tot"]
		}
		out = append(out, row)
	}
	return out
}

// inboxSubpath ports /inbox/<id>, /inbox/<id>/<mark>.
func (s *Server) inboxSubpath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/inbox/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 {
		s.markMessage(w, r, id, parts[1])
		return
	}
	s.showMessage(w, r, id)
}

// showMessage ports Handler.show_message.
func (s *Server) showMessage(w http.ResponseWriter, r *http.Request, id string) {
	var errMsg string
	data := map[string]interface{}{}
	if s.grid == nil || !s.grid.IsConnected() {
		errMsg = "not connected"
	} else {
		idNum, _ := strconv.Atoi(id)
		result, err := s.grid.InboxMessage(idNum)
		if err != nil {
			errMsg = err.Error()
		} else if m, ok := result.(map[string]interface{}); ok {
			data = m
			if b64, ok := m["data"].(string); ok && b64 != "" {
				if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
					data["data"] = string(decoded)
				}
			}
		}
	}
	s.render(w, r, "message", map[string]interface{}{
		"name":                s.name,
		"error":               errMsg,
		"message_id":          id,
		"message_sender":      data["sender"],
		"message_sender_name": data["sender_name"],
		"message_created_at":  data["created_at"],
		"message_seen_at":     data["seen_at"],
		"message_data":        data["data"],
		"active_page":         "",
	})
}

// markMessage ports Handler.mark_message.
func (s *Server) markMessage(w http.ResponseWriter, r *http.Request, id, mark string) {
	if s.grid == nil || !s.grid.IsConnected() {
		w.WriteHeader(http.StatusOK)
		return
	}
	idNum, _ := strconv.Atoi(id)
	s.grid.MarkMessage(idNum, mark)
	http.Redirect(w, r, "/inbox", http.StatusFound)
}

// newMessage ports Handler.new_message.
func (s *Server) newMessage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "new_message", map[string]interface{}{
		"to":          r.URL.Query().Get("to"),
		"active_page": "new",
	})
}

// sendMessage ports Handler.send_message.
func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, `{"error":"CSRF token missing or invalid"}`, http.StatusForbidden)
		return
	}
	to := r.FormValue("to")
	message := r.FormValue("message")
	var errMsg interface{}
	if s.grid == nil || !s.grid.IsConnected() {
		errMsg = "not connected"
	} else if _, err := s.grid.SendMessage(to, message); err != nil {
		errMsg = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	if errMsg != nil {
		fmt.Fprintf(w, `{"error":%q}`, errMsg)
	} else {
		fmt.Fprint(w, `{"error":null}`)
	}
}

func intFromJSON(v interface{}, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	}
	return def
}

func jsonIndent(v interface{}) string {
	if v == nil {
		return "{}"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
