package pluginrpc

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFetchIndexParsesRealHTTPResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"index_version":1,"plugins":[{"name":"foo","version":"1.0.0","author":"a","description":"d","manifest_url":"https://example.invalid/foo.toml"}]}`))
	}))
	defer srv.Close()

	idx, err := FetchIndex(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(idx.Plugins) != 1 || idx.Plugins[0].Name != "foo" {
		t.Fatalf("unexpected index: %+v", idx)
	}
}

func TestFetchIndexPropagatesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := FetchIndex(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}

func TestFetchIndexRejectsInvalidSchema(t *testing.T) {
	cases := map[string]string{
		"missing version": `{"plugins":[]}`,
		"unknown field":   `{"index_version":1,"plugins":[],"extra":true}`,
		"unsafe name":     `{"index_version":1,"plugins":[{"name":"../bad","version":"1","manifest_url":"https://example.invalid/bad"}]}`,
		"duplicate name":  `{"index_version":1,"plugins":[{"name":"same","version":"1","manifest_url":"https://example.invalid/one"},{"name":"same","version":"2","manifest_url":"https://example.invalid/two"}]}`,
		"invalid URL":     `{"index_version":1,"plugins":[{"name":"bad","version":"1","manifest_url":"file:///tmp/bad"}]}`,
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			if _, err := FetchIndex(context.Background(), srv.Client(), srv.URL); err == nil {
				t.Fatal("expected invalid repository index to be rejected")
			}
		})
	}
}

func TestFetchIndexRejectsOversizedBody(t *testing.T) {
	body := bytes.Repeat([]byte("x"), MaxIndexBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	if _, err := FetchIndex(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected oversized repository index to be rejected")
	}
}

func TestFetchManifestParsesRealHTTPResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(validManifestTOML))
	}))
	defer srv.Close()

	m, err := FetchManifest(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if m.Name != "example-plugin" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
}

func TestFetchManifestRejectsInvalidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a manifest"))
	}))
	defer srv.Close()

	if _, err := FetchManifest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for an invalid manifest body")
	}
}

func TestFetchManifestRejectsOversizedBody(t *testing.T) {
	body := bytes.Repeat([]byte("x"), MaxManifestBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	if _, err := FetchManifest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected oversized manifest to be rejected")
	}
}

func TestDownloadExecutableWritesRealBytesExecutably(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-binary-content"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "plugin-bin")
	if err := DownloadExecutable(context.Background(), srv.Client(), srv.URL, dest); err != nil {
		t.Fatalf("DownloadExecutable: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-binary-content" {
		t.Fatalf("unexpected content: %s", data)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatal("expected the downloaded file to be executable")
	}
}

func TestDownloadExecutablePropagatesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "plugin-bin")
	if err := DownloadExecutable(context.Background(), srv.Client(), srv.URL, dest); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestDownloadExecutableRejectsOversizedContentWithoutReplacingDestination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(MaxExecutableBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "plugin-bin")
	if err := os.WriteFile(dest, []byte("existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := DownloadExecutable(context.Background(), srv.Client(), srv.URL, dest); err == nil {
		t.Fatal("expected an oversized executable to be rejected")
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatalf("existing destination was changed: %q", data)
	}
}
