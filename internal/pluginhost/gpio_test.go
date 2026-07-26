//go:build linux

package pluginhost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeGPIORoot(t *testing.T, pin int, value string) string {
	t.Helper()
	root := t.TempDir()
	lineDir := filepath.Join(root, "gpio"+strconv.Itoa(pin))
	if err := os.Mkdir(lineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"direction": "in",
		"edge":      "none",
		"value":     value,
	} {
		if err := os.WriteFile(filepath.Join(lineDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGPIOReadAndWrite(t *testing.T) {
	root := fakeGPIORoot(t, 4, "1\n")
	line, err := (GPIO{Root: root}).Line(4)
	if err != nil {
		t.Fatal(err)
	}
	defer line.Close()
	high, err := line.Read()
	if err != nil || !high {
		t.Fatalf("Read = %v, %v; want high", high, err)
	}
	if err := line.Write(false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "gpio4", "value"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "0") {
		t.Fatalf("value = %q, want low", data)
	}
}

func TestGPIORejectsInvalidPin(t *testing.T) {
	if _, err := (GPIO{}).Line(-1); err == nil {
		t.Fatal("expected invalid pin to be rejected")
	}
}
