package config

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMD5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := MD5(path)
	if err != nil {
		t.Fatal(err)
	}
	// md5("hello world") is a well-known value.
	want := "5eb63bbbe01eeed093cb22bb8f5acdc3"
	if got != want {
		t.Fatalf("MD5 = %s, want %s", got, want)
	}
}

func TestDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("downloaded content"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	if err := DownloadFile(srv.URL, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "downloaded content" {
		t.Fatalf("content = %q", got)
	}
}

func TestDownloadFileErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	dir := t.TempDir()
	err := DownloadFile(srv.URL, filepath.Join(dir, "out.bin"))
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}

func makeTestZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func TestUnzipStripsLeadingDirs(t *testing.T) {
	zipPath := makeTestZip(t, map[string]string{
		"repo-master/plugin.py":     "print('hi')",
		"repo-master/plugin.yml":    "enabled: true",
		"repo-master/sub/nested.py": "x = 1",
	})
	dest := filepath.Join(t.TempDir(), "out")

	if err := Unzip(zipPath, dest, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "plugin.py")); err != nil {
		t.Fatalf("expected plugin.py at stripped path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "nested.py")); err != nil {
		t.Fatalf("expected nested file to be extracted with dir stripped: %v", err)
	}
}

func TestUnzipNoStrip(t *testing.T) {
	zipPath := makeTestZip(t, map[string]string{
		"plugin.py": "print('hi')",
	})
	dest := filepath.Join(t.TempDir(), "out")

	if err := Unzip(zipPath, dest, 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "plugin.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("hi")) {
		t.Fatalf("content = %s", got)
	}
}
