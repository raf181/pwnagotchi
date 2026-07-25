package pluginhost

import (
	"context"
	"strings"
	"testing"
)

func TestExecRunsRealArgvCommand(t *testing.T) {
	var r Exec
	out, err := r.Run(context.Background(), "echo", "hello", "world")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello world" {
		t.Fatalf("output = %q, want %q", got, "hello world")
	}
}

func TestExecPropagatesRealError(t *testing.T) {
	var r Exec
	if _, err := r.Run(context.Background(), "false"); err == nil {
		t.Fatal("expected an error from a command that exits non-zero")
	}
}
