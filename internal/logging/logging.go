// Package logging ports pwnagotchi/log.py's setup_logging and log rotation
// (the LastSession parsing half of log.py lives in internal/session).
package logging

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// Level mirrors Python's logging levels (only the ones setup_logging uses).
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarning
	LevelError
	LevelCritical
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarning:
		return "WARNING"
	case LevelError:
		return "ERROR"
	case LevelCritical:
		return "CRITICAL"
	default:
		return "INFO"
	}
}

// Logger ports the handler topology setup_logging builds: a root level
// gate, plus two file handlers whose OWN levels are fixed regardless of
// the root level — INFO for the "normal" file, DEBUG for the "debug" file
// — matching Python exactly:
//   - non-debug run: root level INFO means DEBUG records never reach ANY
//     handler (filtered before dispatch), so both files show INFO+ only.
//   - debug run: root level DEBUG lets everything through; the normal file
//     still only shows INFO+ (its own handler level), while the debug file
//     shows DEBUG+.
type Logger struct {
	mu sync.Mutex

	RootLevel Level

	NormalWriter io.WriteCloser
	DebugWriter  io.WriteCloser

	// ThreadName mirrors Python's %(threadName)s. Go has no equivalent to
	// named threads; callers set this per Logger instance to mirror
	// Python's threading.Thread(name="Grid"), threading.Thread(name="File
	// Sys"), etc. Defaults to "MainThread", matching Python's main-thread
	// default.
	ThreadName string

	// Now is overridable for tests; defaults to time.Now.
	Now func() time.Time
}

const normalFileLevel = LevelInfo
const debugFileLevel = LevelDebug

func newLogger() *Logger {
	return &Logger{RootLevel: LevelInfo, ThreadName: "MainThread", Now: time.Now}
}

func (l *Logger) formatLine(level Level, format string, args ...interface{}) string {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	asctime := l.Now().Format("2006-01-02 15:04:05.000")
	// Python's asctime uses a comma before milliseconds, not a period.
	asctime = asctime[:len(asctime)-4] + "," + asctime[len(asctime)-3:]
	return fmt.Sprintf("[%s] [%s] [%s] : %s", asctime, level, l.ThreadName, msg)
}

func (l *Logger) log(level Level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.RootLevel {
		return
	}
	line := l.formatLine(level, format, args...) + "\n"
	if l.NormalWriter != nil && level >= normalFileLevel {
		l.NormalWriter.Write([]byte(line))
	}
	if l.DebugWriter != nil && level >= debugFileLevel {
		l.DebugWriter.Write([]byte(line))
	}
}

func (l *Logger) Debug(format string, args ...interface{})    { l.log(LevelDebug, format, args...) }
func (l *Logger) Info(format string, args ...interface{})     { l.log(LevelInfo, format, args...) }
func (l *Logger) Warning(format string, args ...interface{})  { l.log(LevelWarning, format, args...) }
func (l *Logger) Error(format string, args ...interface{})    { l.log(LevelError, format, args...) }
func (l *Logger) Critical(format string, args ...interface{}) { l.log(LevelCritical, format, args...) }

// Args is the subset of cli.py's argparse Namespace setup_logging reads.
type Args struct {
	Debug bool
}

// SetupLogging ports log.setup_logging(args, config): rotates existing log
// files if needed, opens (creates) both log files, and returns a configured
// Logger. The startup banner line is written by the caller via
// Logger.Info(StartupBanner) to keep this function side-effect-obvious and
// testable without asserting on log content.
func SetupLogging(args Args, cfg config.Map) (*Logger, error) {
	logCfg, _ := cfg["main"].(config.Map)["log"].(config.Map)
	filename, _ := logCfg["path"].(string)
	filenameDebug, _ := logCfg["path-debug"].(string)

	l := newLogger()
	if args.Debug {
		l.RootLevel = LevelDebug
	} else {
		l.RootLevel = LevelInfo
	}

	if filename == "" {
		return l, nil
	}

	if err := LogRotation(filename, logCfg); err != nil {
		return nil, err
	}
	if err := LogRotation(filenameDebug, logCfg); err != nil {
		return nil, err
	}

	normalFile, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	debugFile, err := os.OpenFile(filenameDebug, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		normalFile.Close()
		return nil, err
	}
	l.NormalWriter = normalFile
	l.DebugWriter = debugFile

	return l, nil
}

// StartupBanner mirrors the exact banner line setup_logging emits at the
// end, at INFO level.
const StartupBanner = "-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=- Pwnagotchi Re|Started -=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-"
