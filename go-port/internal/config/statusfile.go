package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	gofs "github.com/jayofelony/pwnagotchi/go-port/internal/fs"
)

// StatusFile ports utils.StatusFile: a small persisted status marker, either
// a raw text blob (data_format="raw", the default) or a JSON document
// (data_format="json").
type StatusFile struct {
	path    string
	format  string
	updated time.Time
	hasData bool
	Data    interface{}
}

// NewStatusFile mirrors StatusFile.__init__: if path exists, its mtime seeds
// `updated` and its contents are loaded eagerly (parsed as JSON when
// format=="json").
func NewStatusFile(path, format string) (*StatusFile, error) {
	if format == "" {
		format = "raw"
	}
	s := &StatusFile{path: path, format: format}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.updated = info.ModTime()

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if format == "json" {
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		s.Data = v
	} else {
		s.Data = string(raw)
	}
	s.hasData = true
	return s, nil
}

// DataFieldOr mirrors StatusFile.data_field_or(name, default). For
// data_format=="json" (the only format any bundled plugin actually uses with
// this method), Data is expected to be a map and this looks up name in it.
// For the raw format Python does `name in self.data` (a SUBSTRING test,
// since self.data is a plain string there) and then `self.data[name]`, which
// raises TypeError in real Python because string indices must be integers —
// replicated here as an error rather than silently returning something
// plausible-looking.
func (s *StatusFile) DataFieldOr(name string, def interface{}) (interface{}, error) {
	if !s.hasData || s.Data == nil {
		return def, nil
	}
	switch d := s.Data.(type) {
	case map[string]interface{}:
		if v, ok := d[name]; ok {
			return v, nil
		}
		return def, nil
	case string:
		if strings.Contains(d, name) {
			return nil, fmt.Errorf("config: StatusFile.DataFieldOr(%q): string indices must be integers (raw-format data), matching Python TypeError", name)
		}
		return def, nil
	default:
		return def, nil
	}
}

// NewerThenMinutes mirrors StatusFile.newer_then_minutes.
func (s *StatusFile) NewerThenMinutes(minutes float64) bool {
	if s.updated.IsZero() {
		return false
	}
	return time.Since(s.updated).Minutes() < minutes
}

// NewerThenHours mirrors StatusFile.newer_then_hours.
func (s *StatusFile) NewerThenHours(hours float64) bool {
	if s.updated.IsZero() {
		return false
	}
	return time.Since(s.updated).Hours() < hours
}

// NewerThenDays mirrors StatusFile.newer_then_days: Python's
// (datetime.now() - self._updated).days is whole-day floor division of the
// elapsed duration, not a rounded day count.
func (s *StatusFile) NewerThenDays(days int) bool {
	if s.updated.IsZero() {
		return false
	}
	elapsedDays := int(time.Since(s.updated).Hours() / 24)
	return elapsedDays < days
}

// Update mirrors StatusFile.update(data): atomically (re)writes the file via
// the same ensure_write pattern as Python, and updates in-memory state.
//   - data == nil: writes str(datetime.now()) (Python's default str(datetime) format).
//   - format == "json": json-encodes data (compact, matching json.dump defaults).
//   - otherwise: writes data as-is; the caller must pass a string, matching
//     Python's fp.write(data) (which itself would raise TypeError on non-str).
func (s *StatusFile) Update(data interface{}) error {
	s.updated = time.Now()
	s.Data = data
	s.hasData = data != nil

	return gofs.EnsureWrite(s.path, func(f *os.File) error {
		if data == nil {
			_, err := f.WriteString(pyDateTimeStr(s.updated))
			return err
		}
		if s.format == "json" {
			// json.Marshal (not Encoder.Encode, which appends a trailing
			// newline Python's json.dump never writes). Key ORDER for
			// map[string]interface{} values is alphabetical in Go vs
			// insertion-order in Python — an unavoidable, documented
			// difference (see docs/known-differences.md) since Go maps
			// don't retain insertion order once decoded.
			b, err := json.Marshal(data)
			if err != nil {
				return err
			}
			_, err = f.Write(b)
			return err
		}
		str, ok := data.(string)
		if !ok {
			return fmt.Errorf("config: StatusFile.Update: non-string data with format %q, matching Python's fp.write(data) TypeError", s.format)
		}
		_, err := f.WriteString(str)
		return err
	})
}

// pyDateTimeStr mirrors str(datetime.now()): "YYYY-MM-DD HH:MM:SS[.ffffff]",
// with the microsecond component present only when nonzero.
func pyDateTimeStr(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02 15:04:05")
	}
	return t.Format("2006-01-02 15:04:05.000000")
}
