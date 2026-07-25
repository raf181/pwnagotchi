package pluginrpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// buildFixture compiles the given Go source as a standalone executable
// into t.TempDir(), returning its path. Real subprocess coverage (crash
// detection, hang detection, real capability round-trips) needs a real
// separate process — not a mock — so every test in this file spawns an
// actual compiled binary rather than faking Host's subprocess.
func buildFixture(t *testing.T, name, source string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building fixture %s: %v\n%s", name, err, out)
	}
	return binPath
}

func manifestFor(t *testing.T, path, name string) *Manifest {
	t.Helper()
	sum, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	return &Manifest{
		ManifestVersion: CurrentManifestVersion,
		Name:            name,
		Version:         "1.0.0",
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		SHA256:          sum,
		ExecutableURL:   "https://example.invalid/" + name,
	}
}

// goodFixtureSource is a minimal, dependency-free (stdlib only —
// building it as a standalone file with "go build" outside this
// module's own directory tree can't resolve this module's own import
// path) implementation of the wire protocol: replies "pong" to "ping",
// and on receiving specific named events, issues its own "call" back to
// the host, exercising the same message shapes internal/pluginrpc's
// Host/CapabilityDispatcher speak, without depending on package
// pluginrpc's Client type.
const goodFixtureSource = `
package main

import (
	"bufio"
	"encoding/json"
	"os"
)

type message struct {
	ID     int64           ` + "`json:\"id,omitempty\"`" + `
	Type   string          ` + "`json:\"type\"`" + `
	Name   string          ` + "`json:\"name,omitempty\"`" + `
	Args   json.RawMessage ` + "`json:\"args,omitempty\"`" + `
	Result json.RawMessage ` + "`json:\"result,omitempty\"`" + `
	Error  string          ` + "`json:\"error,omitempty\"`" + `
}

func write(w *bufio.Writer, m message) {
	data, _ := json.Marshal(m)
	w.Write(data)
	w.Write([]byte("\n"))
	w.Flush()
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4<<20)
	out := bufio.NewWriter(os.Stdout)
	nextID := int64(1000)

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		switch m.Type {
		case "ping":
			write(out, message{ID: m.ID, Type: "pong"})
		case "event":
			if m.Name == "loaded" {
				nextID++
				write(out, message{ID: nextID, Type: "call", Name: "Log.Printf", Args: json.RawMessage(` + "`{\"message\":\"fixture loaded ok\"}`" + `)})
			}
			if m.Name == "ping-me-back" {
				nextID++
				write(out, message{ID: nextID, Type: "call", Name: "Agent.Run", Args: json.RawMessage(` + "`{\"cmd\":\"echo hi\",\"verbose\":false}`" + `)})
			}
		}
	}
}
`

const crashFixtureSource = `
package main

import "os"

func main() {
	os.Exit(7)
}
`

// hangFixtureSource stays genuinely alive (never exits) but never reads
// or replies to anything on stdin — a real "the process is running fine,
// it's just stuck" condition, distinct from a crash. A bare "select{}"
// would trip Go's own runtime deadlock detector (fatal error: all
// goroutines are asleep) and exit almost immediately, which would
// exercise the crash-detection path instead of the heartbeat/hang path
// this fixture is meant to test — sleeping in a loop keeps the runtime
// convinced something is legitimately still pending.
const hangFixtureSource = `
package main

import "time"

func main() {
	for {
		time.Sleep(time.Hour)
	}
}
`

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []string
}

func (d *fakeDispatcher) Dispatch(method string, args json.RawMessage) (interface{}, error) {
	d.mu.Lock()
	d.calls = append(d.calls, method)
	d.mu.Unlock()
	switch method {
	case "Log.Printf":
		return nil, nil
	case "Agent.Run":
		return map[string]interface{}{"output": "hi\n"}, nil
	}
	return nil, nil
}

func TestSpawnRefusesChecksumMismatch(t *testing.T) {
	path := buildFixture(t, "good", goodFixtureSource)
	m := manifestFor(t, path, "good")
	m.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"[:64] // definitely wrong

	_, err := Spawn("good", path, m, Options{})
	if err == nil {
		t.Fatal("expected Spawn to refuse a checksum mismatch")
	}
	if _, ok := err.(*ErrChecksumMismatch); !ok {
		t.Fatalf("expected *ErrChecksumMismatch, got %T: %v", err, err)
	}
}

func TestSpawnAndEventCallRoundTrip(t *testing.T) {
	path := buildFixture(t, "good", goodFixtureSource)
	m := manifestFor(t, path, "good")
	disp := &fakeDispatcher{}

	h, err := Spawn("good", path, m, Options{Dispatcher: disp, PingInterval: -1})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer h.Close()

	if err := h.SendEvent("loaded", nil); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	waitFor(t, func() bool {
		disp.mu.Lock()
		defer disp.mu.Unlock()
		for _, c := range disp.calls {
			if c == "Log.Printf" {
				return true
			}
		}
		return false
	})
}

func TestSpawnCapabilityCallRoundTrip(t *testing.T) {
	path := buildFixture(t, "good", goodFixtureSource)
	m := manifestFor(t, path, "good")
	disp := &fakeDispatcher{}

	h, err := Spawn("good", path, m, Options{Dispatcher: disp, PingInterval: -1})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer h.Close()

	if err := h.SendEvent("ping-me-back", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		disp.mu.Lock()
		defer disp.mu.Unlock()
		for _, c := range disp.calls {
			if c == "Agent.Run" {
				return true
			}
		}
		return false
	})
}

// TestCrashIsDetectedAndIsolated proves a real process crash is
// observed via monitorExit and reported through OnFailure — the "hard
// crash" half of process-boundary isolation.
func TestCrashIsDetectedAndIsolated(t *testing.T) {
	path := buildFixture(t, "crash", crashFixtureSource)
	m := manifestFor(t, path, "crash")

	var mu sync.Mutex
	var failErr error
	h, err := Spawn("crash", path, m, Options{
		PingInterval: -1,
		OnFailure: func(err error) {
			mu.Lock()
			failErr = err
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer h.Close()

	waitFor(t, func() bool { return !h.Alive() })
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failErr != nil
	})

	// Sending an event to a dead plugin must fail cleanly, never panic.
	if err := h.SendEvent("epoch", nil); err == nil {
		t.Fatal("expected SendEvent to a crashed plugin to return an error")
	}
}

// TestHangIsDetectedViaHeartbeat proves a process that's alive but
// unresponsive (never answers pings) is detected and killed — the "soft
// hang" half of process-boundary isolation, which a bare exit-code check
// alone can never catch.
func TestHangIsDetectedViaHeartbeat(t *testing.T) {
	path := buildFixture(t, "hang", hangFixtureSource)
	m := manifestFor(t, path, "hang")

	var mu sync.Mutex
	var failErr error
	h, err := Spawn("hang", path, m, Options{
		PingInterval:   30 * time.Millisecond,
		CallTimeout:    30 * time.Millisecond,
		MaxMissedPings: 2,
		OnFailure: func(err error) {
			mu.Lock()
			failErr = err
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer h.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := failErr
		mu.Unlock()
		if got != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if failErr == nil {
		t.Fatal("expected the hung plugin to be detected and reported as failed")
	}
	if h.Alive() {
		t.Fatal("expected the hung plugin's process to have been killed")
	}
}

func TestOneCallTimesOutWithoutBlockingForever(t *testing.T) {
	path := buildFixture(t, "hang", hangFixtureSource)
	m := manifestFor(t, path, "hang")
	h, err := Spawn("hang", path, m, Options{PingInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = h.doCall(ctx, TypePing, "", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > time.Second {
		t.Fatalf("call took %v, expected it to respect the ~100ms context deadline", elapsed)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}
