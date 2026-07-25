package logging

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

var maxSizeRe = regexp.MustCompile(`^(\d+)([bBkKmMgG]?)`)

// ParseMaxSize mirrors log.parse_max_size. Note: like Python's
// `re.findall`, this does NOT require the whole string to match — trailing
// garbage after a valid "<digits><optional unit letter>" prefix is silently
// ignored (e.g. "10x" parses as 10 bytes, not an error), verified against
// the real interpreter.
func ParseMaxSize(s string) (int64, error) {
	m := maxSizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("can't parse %s as a max size", s)
	}
	num, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, err
	}
	switch strings.ToLower(m[2]) {
	case "k":
		return num * 1024, nil
	case "m":
		return num * 1024 * 1024, nil
	case "g":
		return num * 1024 * 1024 * 1024, nil
	default:
		return num, nil
	}
}

// LogRotation mirrors log.log_rotation(filename, cfg).
func LogRotation(filename string, cfg config.Map) error {
	if filename == "" {
		return nil
	}
	rotation, _ := cfg["rotation"].(config.Map)
	if rotation == nil {
		return nil
	}
	enabled, _ := rotation["enabled"].(bool)
	if !enabled {
		return nil
	}
	info, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	sizeStr, _ := rotation["size"].(string)
	if sizeStr == "" {
		return fmt.Errorf("log rotation is enabled but log.rotation.size was not specified")
	}
	maxSize, err := ParseMaxSize(sizeStr)
	if err != nil {
		return err
	}
	if info.Size() >= maxSize {
		return DoRotate(filename, info.Size())
	}
	return nil
}

// DoRotate mirrors log.do_rotate(filename, stats, cfg): finds a free
// "<name>[-N].gz" archive slot, moves the log there (as ".log", via the
// SAME literal-substring "gz"->"log" replace Python uses — not an
// extension-aware rename, see docs/known-differences.md for the
// same-path-move edge case this produces on the FIRST rotation), gzips it,
// and removes the intermediate ".log" file.
func DoRotate(filename string, size int64) error {
	basePath := filepath.Dir(filename)
	name := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))

	archiveFilename := filepath.Join(basePath, name+".gz")
	counter := 2
	for {
		if _, err := os.Stat(archiveFilename); os.IsNotExist(err) {
			break
		}
		archiveFilename = filepath.Join(basePath, fmt.Sprintf("%s-%d.gz", name, counter))
		counter++
	}

	logFilename := strings.ReplaceAll(archiveFilename, "gz", "log")

	fmt.Printf("%s is %d bytes big, rotating to %s ...\n", filename, size, logFilename)

	// os.Rename onto the SAME path (which happens on the very first
	// rotation, since archiveFilename="<name>.gz" replaced to
	// "<name>.log" reconstructs the original filename exactly) is a
	// successful no-op on Linux, matching Python's shutil.move behavior
	// there (verified: shutil.move(x, x) does not raise).
	if filename != logFilename {
		if err := os.Rename(filename, logFilename); err != nil {
			return err
		}
	}

	fmt.Printf("compressing to %s ...\n", archiveFilename)

	if err := gzipFile(logFilename, archiveFilename); err != nil {
		return err
	}
	return os.Remove(logFilename)
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}
