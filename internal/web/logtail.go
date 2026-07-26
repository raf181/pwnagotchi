package web

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// Native Go replacement for plugins/default/logtail.py's web UI.
//
// Why native instead of routed through internal/pyplugin: logtail's
// on_webhook has two real problems for the Python subprocess bridge,
// neither of which is a defect in logtail.py itself:
//
//  1. Its "stream" route is a genuine infinite generator (tails the log
//     file forever, matching real `tail -f`) — bridge.py's webhook call
//     captures a plugin's ENTIRE response body via Flask's
//     resp.get_data() before returning it in one message, which can
//     never finish for a response that never ends. This is a structural
//     limit of "capture whole body, then respond" RPC, documented in
//     known-differences.md, not something fixable without redesigning
//     the bridge's webhook protocol into a real streaming one.
//  2. More generally, this bridge's single stdin/stdout channel carries
//     both fire-and-forget on_ui_update-style events AND synchronous
//     calls (list_plugins/toggle/webhook) in strict FIFO order; under
//     real load (many plugins, frequent view updates) a synchronous call
//     can queue behind a large backlog of events and appear to hang from
//     the caller's side even though the bridge process itself is not
//     deadlocked. Real, load-dependent head-of-line blocking — again not
//     a logtail.py defect.
//
// A native Go implementation sidesteps both: it reads the real log file
// directly (same path/config as the Python plugin: main.log.path,
// plugins.logtail.max-lines) and streams new lines using Go's own
// http.Flusher, which supports true incremental chunked output — no
// "capture the whole body first" limitation at all.
func (s *Server) logtailIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "logtail", map[string]interface{}{
		"active_page": "plugins",
	})
}

func (s *Server) logtailPath() string {
	if s.cfg == nil {
		return ""
	}
	mainCfg, _ := s.cfg["main"].(config.Map)
	logCfg, _ := mainCfg["log"].(config.Map)
	return stringField(logCfg, "path", "/etc/pwnagotchi/log/pwnagotchi.log")
}

func (s *Server) logtailMaxLines() int {
	if s.cfg == nil {
		return 4096
	}
	mainCfg, _ := s.cfg["main"].(config.Map)
	pluginsCfg, _ := mainCfg["plugins"].(config.Map)
	logtailCfg, _ := pluginsCfg["logtail"].(config.Map)
	return intField(logtailCfg, "max-lines", 4096)
}

// logtailStream ports the real "stream" route's behavior exactly
// (last max-lines lines, then follow forever) but as a genuine live
// HTTP stream via http.Flusher, instead of a captured-then-returned
// body — real dynamic/live interaction, not a polled static resource.
func (s *Server) logtailStream(w http.ResponseWriter, r *http.Request) {
	path := s.logtailPath()
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "log file not available: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer f.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")

	lines, err := tailLines(path, s.logtailMaxLines())
	if err != nil {
		http.Error(w, "reading log file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, line := range lines {
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return
		}
	}
	flusher.Flush()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return
	}
	reader := bufio.NewReader(f)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if _, werr := w.Write([]byte(line)); werr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			// Real EOF: no new data yet — wait briefly and retry, the
			// same polling shape Python's own `while True: yield
			// f.readline()` generator has (readline() on a still-open
			// file at EOF returns '' without blocking, so it too is
			// effectively a poll loop; Python just doesn't sleep between
			// attempts, we do, to avoid a real busy-loop here).
			select {
			case <-ctx.Done():
				return
			case <-time.After(150 * time.Millisecond):
			}
		}
	}
}

// tailLines reads the last n non-empty-file lines of path, mirroring
// Python's `f.readlines()[-max_lines:]`.
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
