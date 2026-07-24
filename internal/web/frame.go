package web

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
)

// FramePath/FrameCType mirror pwnagotchi/ui/web/__init__.py's
// frame_path/frame_ctype module globals (PNG, saved by view.Update on
// every real render so the web UI's "/ui" route always has the latest
// rendered frame to serve).
var (
	FramePath  = "/var/tmp/pwnagotchi/pwnagotchi.png"
	FrameCType = "image/png"
)

var frameMu sync.Mutex

// UpdateFrame ports web.update_frame(img): saves the real rendered canvas
// to disk as a real PNG, under a real lock — meant to be registered via
// internal/ui/view.View.OnRender so every real display frame update also
// updates the web UI's served image, exactly mirroring view.py's own
// `web.update_frame(self._canvas)` call at the end of View.update().
func UpdateFrame(img image.Image) error {
	frameMu.Lock()
	defer frameMu.Unlock()

	dir := filepath.Dir(FramePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(FramePath, buf.Bytes(), 0o644)
}

// readFrame serves the "/ui" route's real, current frame — read under the
// same lock UpdateFrame writes under, so a concurrent render can never
// produce a torn read.
func readFrame() ([]byte, error) {
	frameMu.Lock()
	defer frameMu.Unlock()
	return os.ReadFile(FramePath)
}
