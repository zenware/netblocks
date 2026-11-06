package cache

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineRequiresCachedFile(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, "test")
	c.Offline = true
	if _, err := c.Download(context.Background(), "https://example.com/file"); !errors.Is(err, ErrOffline) {
		t.Fatalf("expected ErrOffline, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := c.Download(context.Background(), "https://example.com/file")
	if err != nil || path == "" {
		t.Fatalf("cached offline download = %q, %v", path, err)
	}
}

func TestDownloadAndConditionalGet(t *testing.T) {
	var gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data":
			if ims := r.Header.Get("If-Modified-Since"); ims != "" {
				gotIMS = ims
				w.WriteHeader(http.StatusNotModified)
				return
			}
			fmt.Fprint(w, "hello")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	c := New(dir, "test")
	c.Retries = 1
	path, err := c.Download(context.Background(), srv.URL+"/data")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "hello" {
		t.Fatalf("content = %q, %v", b, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), srv.URL+"/data"); err != nil {
		t.Fatal(err)
	}
	if gotIMS == "" {
		t.Fatal("second request did not send If-Modified-Since")
	}
	b, _ = os.ReadFile(path)
	if string(b) != "hello" {
		t.Fatalf("304 clobbered content: %q", b)
	}
}

func TestDownloadAndVerify(t *testing.T) {
	body := []byte("verified content")
	sum := md5.Sum(body)
	hexSum := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/payload":
			_, _ = w.Write(body)
		case "/payload.md5":
			fmt.Fprintf(w, "%s  payload\n", hexSum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	c := New(dir, "test")
	c.Retries = 1
	if _, err := c.DownloadAndVerify(context.Background(), srv.URL+"/payload"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.md5"), []byte(hexSum), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMD5(filepath.Join(dir, "bad"), filepath.Join(dir, "bad.md5")); !errors.Is(err, ErrChecksum) {
		t.Fatalf("expected ErrChecksum, got %v", err)
	}
}

func TestFilename(t *testing.T) {
	if got := urlBase("https://example.com/a/b.bz2?x=1"); got != "b.bz2" {
		t.Fatalf("urlBase = %q", got)
	}
}
