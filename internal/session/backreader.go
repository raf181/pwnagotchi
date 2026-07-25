package session

import (
	"os"
	"strings"
)

// readLinesBackwards ports the line-filtering loop in LastSession.parse
// that iterates pwnagotchi's log file from the end (via Python's
// FileReadBackwards) until it finds a "connecting to http" (START_TOKEN)
// line or runs out of lines, keeping only lines that are empty or start
// with '[' (i.e., real formatted log lines, filtering out any stray
// non-bracketed continuation/traceback lines), then returns them in
// chronological (oldest-first) order — exactly matching Python's
// `lines.reverse()` at the end.
//
// This reads the whole file into memory rather than streaming backwards in
// chunks the way Python's file_read_backwards does; functionally identical
// output, but not as memory-efficient on very large log files — see
// docs/known-differences.md.
func readLinesBackwards(path string, onProgress func(linesSoFar int)) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	all := strings.Split(string(data), "\n")
	// A trailing newline produces one final empty element Python's line
	// iterator never yields as a line; drop it to match.
	if len(all) > 0 && all[len(all)-1] == "" {
		all = all[:len(all)-1]
	}

	var kept []string
	for i := len(all) - 1; i >= 0; i-- {
		line := strings.TrimSpace(all[i])
		if line != "" && line[0] != '[' {
			continue
		}
		kept = append(kept, line)
		if strings.Contains(line, startToken) {
			break
		}
		if onProgress != nil && len(kept)%100 == 0 {
			onProgress(len(kept))
		}
	}

	// reverse kept in place (currently newest-first, want oldest-first)
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept, nil
}
