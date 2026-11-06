// Package cache downloads datasets into a local cache directory and verifies
// their integrity.
package cache

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrOffline is returned when a download is required in offline mode.
var ErrOffline = errors.New("offline mode: required file is not cached")

// ErrChecksum is returned when an md5 checksum does not match.
var ErrChecksum = errors.New("checksum mismatch")

// ErrNoChecksum is returned when a checksum file exists but contains no hash.
var ErrNoChecksum = errors.New("no md5 checksum found")

// Client manages a cache directory.
type Client struct {
	Dir       string
	UserAgent string
	HTTP      *http.Client
	Retries   int
	Offline   bool
	Verbose   bool
	Quiet     bool
	Stderr    io.Writer
}

// New returns a cache client. The cache directory is created on first write.
func New(dir, userAgent string) *Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   4,
	}
	return &Client{
		Dir:       dir,
		UserAgent: userAgent,
		HTTP:      &http.Client{Transport: transport},
		Retries:   3,
		Stderr:    os.Stderr,
	}
}

// EnsureDir creates the cache directory.
func (c *Client) EnsureDir() error {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return fmt.Errorf("create cache directory %s: %w", c.Dir, err)
	}
	return nil
}

// Path joins a filename onto the cache directory.
func (c *Client) Path(name string) string {
	return filepath.Join(c.Dir, name)
}

// Age returns how old a cache file is.
func (c *Client) Age(name string) (time.Duration, bool) {
	fi, err := os.Stat(c.Path(name))
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// Download fetches rawURL into the cache using the URL's base name.
func (c *Client) Download(ctx context.Context, rawURL string) (string, error) {
	return c.DownloadTo(ctx, rawURL, "")
}

// DownloadTo fetches rawURL into the cache. If filename is empty, the URL's
// base name is used.
func (c *Client) DownloadTo(ctx context.Context, rawURL, filename string) (string, error) {
	if filename == "" {
		filename = urlBase(rawURL)
	}
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." || filename == "/" || filename == string(filepath.Separator) {
		return "", fmt.Errorf("cannot determine filename for %s", rawURL)
	}
	dst := c.Path(filename)
	if c.Offline {
		if _, err := os.Stat(dst); err != nil {
			return "", fmt.Errorf("%w: %s", ErrOffline, dst)
		}
		return dst, nil
	}
	if err := c.EnsureDir(); err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 1; attempt <= c.maxRetries(); attempt++ {
		if attempt > 1 {
			delay := time.Duration(1<<uint(attempt-2)) * time.Second
			if c.Verbose {
				c.logf("retrying %s in %s (attempt %d/%d): %v", rawURL, delay, attempt, c.maxRetries(), lastErr)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}
		err := c.downloadOnce(ctx, rawURL, dst)
		if err == nil {
			return dst, nil
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return "", lastErr
}

func (c *Client) downloadOnce(ctx context.Context, rawURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if fi, err := os.Stat(dst); err == nil {
		req.Header.Set("If-Modified-Since", fi.ModTime().UTC().Format(http.TimeFormat))
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return downloadError{err: err, retry: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		now := time.Now()
		_ = os.Chtimes(dst, now, now)
		if c.Verbose {
			c.logf("%s is unchanged", rawURL)
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		re := downloadError{err: fmt.Errorf("GET %s: %s", rawURL, resp.Status), retry: resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout}
		return re
	}

	tmp, err := os.CreateTemp(c.Dir, "."+filepath.Base(dst)+".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if c.Verbose && !c.Quiet {
		c.logf("downloading %s", rawURL)
	}
	body := io.Reader(resp.Body)
	if c.Verbose && !c.Quiet {
		body = &progressReader{r: resp.Body, total: resp.ContentLength, name: filepath.Base(dst), out: c.Stderr}
	}
	if _, err := io.Copy(tmp, body); err != nil {
		return downloadError{err: err, retry: true}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		// Windows does not replace an existing destination atomically. Fall
		// back to remove-then-rename rather than failing an update.
		if removeErr := os.Remove(dst); removeErr != nil {
			return err
		}
		if err := os.Rename(tmpName, dst); err != nil {
			return err
		}
	}
	ok = true
	if c.Verbose && !c.Quiet {
		c.logf("saved %s (%s)", dst, humanBytes(resp.ContentLength))
	}
	return nil
}

// DownloadAndVerify fetches rawURL and its .md5 checksum, then verifies the
// payload. The checksum file is optional: if it cannot be parsed, the download
// is accepted with a warning.
func (c *Client) DownloadAndVerify(ctx context.Context, rawURL string) (string, error) {
	dst, err := c.Download(ctx, rawURL)
	if err != nil {
		return "", err
	}
	md5URL := rawURL + ".md5"
	md5Path, err := c.Download(ctx, md5URL)
	if err != nil {
		c.warnf("checksum unavailable for %s: %v", filepath.Base(dst), err)
		return dst, nil
	}
	if err := VerifyMD5(dst, md5Path); err != nil {
		switch {
		case errors.Is(err, ErrChecksum):
			_ = os.Remove(dst)
			return dst, err
		case errors.Is(err, ErrNoChecksum):
			c.warnf("%v", err)
			return dst, nil
		default:
			return dst, err
		}
	}
	if c.Verbose {
		c.logf("verified md5 for %s", filepath.Base(dst))
	}
	return dst, nil
}

// VerifyMD5 checks dst against an already-downloaded checksum file.
func VerifyMD5(dst, md5Path string) error {
	expected, err := readMD5(md5Path)
	if err != nil {
		return err
	}
	actual, err := fileMD5(dst)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("%w: %s expected %s, got %s", ErrChecksum, filepath.Base(dst), expected, actual)
	}
	return nil
}

var md5RE = regexp.MustCompile(`[A-Fa-f0-9]{32}`)

func readMD5(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := md5RE.FindString(string(b))
	if hash == "" {
		return "", fmt.Errorf("%w in %s", ErrNoChecksum, path)
	}
	return strings.ToLower(hash), nil
}

func fileMD5(path string) (string, error) {
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

func (c *Client) maxRetries() int {
	if c.Retries > 0 {
		return c.Retries
	}
	return 1
}

func (c *Client) logf(format string, args ...any) {
	if c.Stderr != nil {
		fmt.Fprintf(c.Stderr, format+"\n", args...)
	}
}

func (c *Client) warnf(format string, args ...any) {
	if !c.Quiet && c.Stderr != nil {
		fmt.Fprintf(c.Stderr, "warning: "+format+"\n", args...)
	}
}

type downloadError struct {
	err   error
	retry bool
}

func (e downloadError) Error() string { return e.err.Error() }
func (e downloadError) Unwrap() error { return e.err }

func retryable(err error) bool {
	var de downloadError
	if errors.As(err, &de) {
		return de.retry
	}
	return false
}

func urlBase(rawURL string) string {
	rawURL = strings.SplitN(rawURL, "?", 2)[0]
	rawURL = strings.TrimRight(rawURL, "/")
	if i := strings.LastIndex(rawURL, "/"); i >= 0 {
		return rawURL[i+1:]
	}
	return rawURL
}

func humanBytes(n int64) string {
	if n < 0 {
		return "unknown size"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	start   time.Time
	last    time.Time
	name    string
	out     io.Writer
	enabled bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	if p.start.IsZero() {
		p.start = time.Now()
		p.last = p.start
		p.enabled = isTerminal(p.out)
	}
	n, err := p.r.Read(b)
	p.read += int64(n)
	now := time.Now()
	if now.Sub(p.last) >= 500*time.Millisecond && p.enabled {
		p.last = now
		elapsed := now.Sub(p.start).Seconds()
		rate := float64(p.read) / 1024 / elapsed
		if p.total > 0 {
			pct := float64(p.read) / float64(p.total) * 100
			fmt.Fprintf(p.out, "\r  %s: %s / %s (%.0f%%) %.0f KiB/s", p.name, humanBytes(p.read), humanBytes(p.total), pct, rate)
		} else {
			fmt.Fprintf(p.out, "\r  %s: %s %.0f KiB/s", p.name, humanBytes(p.read), rate)
		}
	}
	if err == io.EOF {
		if p.enabled {
			fmt.Fprintf(p.out, "\r  %s: %s done%s\n", p.name, humanBytes(p.read), strings.Repeat(" ", 24))
		}
	}
	return n, err
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
