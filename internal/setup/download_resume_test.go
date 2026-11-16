package setup

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var bigPayload = bytes.Repeat([]byte("0123456789abcdef"), 256<<10) // 4 MiB

func progressLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "MB") && strings.Contains(l, "/") || strings.HasPrefix(l, "  ") {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestSetupDownloadPrintsProgressAtMostOncePerSecond(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(bigPayload)))
		w.Write(bigPayload)
	}))
	defer srv.Close()
	var out bytes.Buffer
	r := &runner{out: &out, client: srv.Client()}
	// Every reading of the clock is 300 ms later than the last, so only about
	// one in four chunks may produce a line.
	var tick int64
	base := time.Unix(1000, 0)
	r.now = func() time.Time { return base.Add(time.Duration(atomic.AddInt64(&tick, 1)) * 300 * time.Millisecond) }
	dest := filepath.Join(t.TempDir(), "m.bin")
	if err := r.download(srv.URL+"/m.bin", dest, sha256Hex(bigPayload)); err != nil {
		t.Fatal(err)
	}
	lines := progressLines(out.String())
	if len(lines) == 0 {
		t.Fatalf("no progress lines in %q", out.String())
	}
	elapsed := time.Duration(atomic.LoadInt64(&tick)) * 300 * time.Millisecond
	if max := int(elapsed/time.Second) + 1; len(lines) > max {
		t.Errorf("%d progress lines in %v of clock time (max %d)", len(lines), elapsed, max)
	}
	if !strings.Contains(lines[0], "%") || !strings.Contains(lines[0], "MB") {
		t.Errorf("progress line %q has no percent and MB", lines[0])
	}
	if strings.Contains(out.String(), "\r") {
		t.Error("progress must be plain lines, not carriage-return updates")
	}
}

func TestSetupDownloadProgressWithoutSizeShowsOnlyMB(t *testing.T) {
	var out bytes.Buffer
	r := &runner{out: &out}
	base := time.Unix(0, 0)
	n := 0
	r.now = func() time.Time { n++; return base.Add(time.Duration(n) * 2 * time.Second) }
	p := &progressPrinter{r: r, last: r.clock()}
	p.update(3<<20, 0)
	if got := strings.TrimSpace(out.String()); got != "3.0 MB" {
		t.Errorf("line = %q", got)
	}
}

func TestSetupDownloadResumesAfterADroppedConnection(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(bigPayload)))
			w.Write(bigPayload[:len(bigPayload)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "m", time.Time{}, bytes.NewReader(bigPayload))
	}))
	defer srv.Close()
	var out bytes.Buffer
	r := &runner{out: &out, client: srv.Client(), retries: 3, retryBase: time.Millisecond}
	dest := filepath.Join(t.TempDir(), "m.bin")
	if err := r.download(srv.URL+"/m.bin", dest, sha256Hex(bigPayload)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); !bytes.Equal(b, bigPayload) {
		t.Fatal("file differs after the resume")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ranges) != 2 || !strings.HasPrefix(ranges[1], "bytes=") {
		t.Errorf("ranges = %q", ranges)
	}
	if !strings.Contains(out.String(), "interrupted") || !strings.Contains(out.String(), "Continuing") {
		t.Errorf("setup output does not say it resumed: %q", out.String())
	}
}

func TestSetupDownloadKeepsThePartialForTheNextRun(t *testing.T) {
	// The host is down for the whole run (404 is not retried, 503 is: use 503
	// with no retries left).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	r := &runner{out: &bytes.Buffer{}, client: srv.Client()}
	dest := filepath.Join(t.TempDir(), "m.bin")
	os.WriteFile(dest+".partial", bigPayload[:1000], 0o644)
	if err := r.download(srv.URL+"/m.bin", dest, sha256Hex(bigPayload)); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed run left a file under the final name")
	}
	if info, err := os.Stat(dest + ".partial"); err != nil || info.Size() != 1000 {
		t.Errorf("the partial file should stay for the next run: %v", err)
	}
}

func TestAcceptHeaderOnlyForGitHub(t *testing.T) {
	for url, want := range map[string]bool{
		"https://github.com/ggml-org/llama.cpp/releases/download/b1/x.zip": true,
		"https://api.github.com/repos/x/y":                                 true,
		"https://huggingface.co/pfnet/m/resolve/rev/m.gguf":                false,
		"http://127.0.0.1:1/x":                                             false,
		"https://evilgithub.com/x":                                         false,
	} {
		if got := isGitHub(url); got != want {
			t.Errorf("isGitHub(%q) = %v", url, got)
		}
	}
}
