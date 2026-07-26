package config

import (
	"archive/zip"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var downloadHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// MD5 mirrors utils.md5: the hex-encoded MD5 digest of a file's contents.
func MD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadFile mirrors utils.download_file: GET url, stream the body to
// destination. Python raises on a non-2xx status (resp.raise_for_status());
// replicated as a returned error.
func DownloadFile(url, destination string) error {
	resp, err := downloadHTTPClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download file: unexpected status %s", resp.Status)
	}
	f, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// Unzip mirrors utils.unzip(file, destination, strip_dirs=0): extracts a
// zip archive, optionally stripping the first stripDirs leading path
// components from every entry (matching Python's
// `info.filename.split('/', maxsplit=strip_dirs)[strip_dirs]`, including
// its behavior of silently DROPPING any entry whose stripped name becomes
// empty — e.g. a bare top-level directory entry itself).
func Unzip(file, destination string, stripDirs int) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	r, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		name := f.Name
		if stripDirs > 0 {
			parts := strings.SplitN(name, "/", stripDirs+1)
			if len(parts) <= stripDirs {
				continue
			}
			name = parts[stripDirs]
			if name == "" {
				continue
			}
		}
		target, err := archiveTarget(destination, name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func archiveTarget(destination, name string) (string, error) {
	name = filepath.FromSlash(name)
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("unzip: archive entry %q escapes destination", name)
	}
	target := filepath.Join(destination, filepath.Clean(name))
	rel, err := filepath.Rel(destination, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unzip: archive entry %q escapes destination", name)
	}
	return target, nil
}

func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}
