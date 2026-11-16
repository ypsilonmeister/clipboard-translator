package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

var payload = bytes.Repeat([]byte("0123456789abcdef"), 64<<10) // 1 MiB

// server counts requests and lets each test script the answer.
type server struct {
	*httptest.Server
	mu     sync.Mutex
	ranges []string
	hits   int
}

func newServer(t *testing.T, h func(s *server, n int, w http.ResponseWriter, r *http.Request)) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits++
		n := s.hits
		s.ranges = append(s.ranges, r.Header.Get("Range"))
		s.mu.Unlock()
		h(s, n, w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) Ranges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

func opts(s *server, dir string) Options {
	return Options{
		Client: s.Client(), URL: s.URL + "/f", Dest: filepath.Join(dir, "f.bin"), SHA256: sum(payload),
		StallTimeout: 5 * time.Second, Retries: 3, RetryBase: time.Millisecond,
	}
}

func assertInstalled(t *testing.T, o Options) {
	t.Helper()
	got, err := os.ReadFile(o.Dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("final file: %v", err)
	}
	if _, err := os.Stat(o.Dest + ".partial"); !os.IsNotExist(err) {
		t.Error("partial file left behind")
	}
}

func assertNoFinal(t *testing.T, o Options) {
	t.Helper()
	if _, err := os.Stat(o.Dest); !os.IsNotExist(err) {
		t.Error("a failed download left a file under the final name")
	}
}

// cutHalf serves half of the body and drops the connection.
func cutHalf(w http.ResponseWriter, r *http.Request, from int) {
	w.Header().Set("Content-Length", fmt.Sprint(len(payload)-from))
	if from > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
	}
	w.Write(payload[from : from+(len(payload)-from)/2])
	w.(http.Flusher).Flush()
	panic(http.ErrAbortHandler)
}

func TestPlainDownload(t *testing.T) {
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	o := opts(s, t.TempDir())
	var last, total int64
	o.Progress = func(d, tt int64) { last, total = d, tt }
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	if last != int64(len(payload)) || total != 0 { // chunked: no declared size
		t.Errorf("progress %d/%d", last, total)
	}
}

func TestConnectionDroppedHalfwayResumesWithRange(t *testing.T) {
	s := newServer(t, func(_ *server, n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			cutHalf(w, r, 0)
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(payload))
	})
	o := opts(s, t.TempDir())
	var retries int
	o.OnRetry = func(int, error, time.Duration) { retries++ }
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	rs := s.Ranges()
	if len(rs) != 2 || rs[0] != "" || !strings.HasPrefix(rs[1], "bytes=") || rs[1] == "bytes=0-" {
		t.Errorf("second attempt must continue with a Range: %q", rs)
	}
	if retries != 1 {
		t.Errorf("retries = %d", retries)
	}
}

func TestStalledServerFailsWithinTheTimeoutAndRetries(t *testing.T) {
	release := make(chan struct{})
	s := newServer(t, func(_ *server, n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload[:1000])
			w.(http.Flusher).Flush()
			select { // then send nothing
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(payload))
	})
	defer close(release)
	o := opts(s, t.TempDir())
	o.StallTimeout = 300 * time.Millisecond
	start := time.Now()
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the stall was not detected in time")
	}
	assertInstalled(t, o)
	if rs := s.Ranges(); len(rs) != 2 || rs[1] != "bytes=1000-" {
		t.Errorf("ranges %q", rs)
	}
}

func TestStallWithoutRetriesFailsInsteadOfHanging(t *testing.T) {
	release := make(chan struct{})
	s := newServer(t, func(_ *server, n int, w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	o := opts(s, t.TempDir())
	o.StallTimeout = 300 * time.Millisecond
	o.Retries = 0
	err := Fetch(context.Background(), o)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("err = %v", err)
	}
	assertNoFinal(t, o)
}

func TestServerIgnoringRangeStartsOver(t *testing.T) {
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	o := opts(s, t.TempDir())
	os.WriteFile(o.Dest+".partial", payload[:300<<10], 0o644)
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	if rs := s.Ranges(); len(rs) != 1 || rs[0] == "" {
		t.Errorf("ranges %q", rs)
	}
}

func TestRangeNotSatisfiable(t *testing.T) {
	serve := func(_ *server, _ int, w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(payload))
	}
	t.Run("complete partial is accepted", func(t *testing.T) {
		s := newServer(t, serve)
		o := opts(s, t.TempDir())
		os.WriteFile(o.Dest+".partial", payload, 0o644)
		if err := Fetch(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		assertInstalled(t, o)
	})
	t.Run("a bad partial of full length restarts from zero", func(t *testing.T) {
		s := newServer(t, serve)
		o := opts(s, t.TempDir())
		bad := append([]byte(nil), payload...)
		bad[7] ^= 1
		os.WriteFile(o.Dest+".partial", bad, 0o644)
		if err := Fetch(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		assertInstalled(t, o)
		if rs := s.Ranges(); len(rs) != 2 || rs[1] != "" {
			t.Errorf("ranges %q", rs)
		}
	})
	t.Run("a partial longer than the expected size is dropped", func(t *testing.T) {
		s := newServer(t, serve)
		o := opts(s, t.TempDir())
		o.ExpectedSize = int64(len(payload))
		os.WriteFile(o.Dest+".partial", append(append([]byte(nil), payload...), "extra"...), 0o644)
		if err := Fetch(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		assertInstalled(t, o)
		if rs := s.Ranges(); len(rs) != 1 || rs[0] != "" {
			t.Errorf("ranges %q", rs)
		}
	})
	t.Run("416 with nothing to resume is an error", func(t *testing.T) {
		s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) {
			http.Error(w, "x", http.StatusRequestedRangeNotSatisfiable)
		})
		o := opts(s, t.TempDir())
		if err := Fetch(context.Background(), o); err == nil || s.hits != 1 {
			t.Fatalf("err = %v, hits = %d", err, s.hits)
		}
	})
}

func TestCorruptPartialIsDeletedAndNothingInstalled(t *testing.T) {
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(payload))
	})
	o := opts(s, t.TempDir())
	bad := append([]byte(nil), payload[:200<<10]...)
	bad[5] ^= 0xff
	os.WriteFile(o.Dest+".partial", bad, 0o644)
	err := Fetch(context.Background(), o)
	var hm *HashMismatchError
	if !errors.As(err, &hm) || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v", err)
	}
	assertNoFinal(t, o)
	if _, err := os.Stat(o.Dest + ".partial"); !os.IsNotExist(err) {
		t.Error("a partial that failed the hash check was kept")
	}
	if s.hits != 1 {
		t.Errorf("a hash mismatch must not be retried (%d requests)", s.hits)
	}
}

func TestRetriesAreCappedAndBackOff(t *testing.T) {
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	})
	o := opts(s, t.TempDir())
	var waits []time.Duration
	o.OnRetry = func(_ int, _ error, w time.Duration) { waits = append(waits, w) }
	err := Fetch(context.Background(), o)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 503 {
		t.Fatalf("err = %v", err)
	}
	if s.hits != 4 { // the first try and three retries
		t.Errorf("requests = %d, want 4", s.hits)
	}
	if len(waits) != 3 || !(waits[0] < waits[1] && waits[1] < waits[2]) {
		t.Errorf("waits = %v, want growing", waits)
	}
	assertNoFinal(t, o)
}

func TestNoRetryForA404(t *testing.T) {
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	o := opts(s, t.TempDir())
	err := Fetch(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "404") || s.hits != 1 {
		t.Fatalf("err = %v, requests = %d", err, s.hits)
	}
}

func TestCancelIsNotRetriedAndKeepsThePartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload[:50<<10])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	o := opts(s, t.TempDir())
	o.Progress = func(d, _ int64) {
		if d > 0 {
			cancel()
		}
	}
	err := Fetch(ctx, o)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if s.hits != 1 {
		t.Errorf("a cancel was retried (%d requests)", s.hits)
	}
	if info, err := os.Stat(o.Dest + ".partial"); err != nil || info.Size() == 0 {
		t.Errorf("the partial file must stay after a cancel: %v", err)
	}
}

func TestDiscardPartialOnFailure(t *testing.T) {
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	o := opts(s, t.TempDir())
	os.WriteFile(o.Dest+".partial", payload[:10], 0o644)
	o.DiscardPartialOnFailure = true
	if err := Fetch(context.Background(), o); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(o.Dest + ".partial"); !os.IsNotExist(err) {
		t.Error("partial kept although DiscardPartialOnFailure is set")
	}
}

func TestHeadersOnlyWhenSet(t *testing.T) {
	var accept, ua string
	s := newServer(t, func(_ *server, _ int, w http.ResponseWriter, r *http.Request) {
		accept, ua = r.Header.Get("Accept"), r.Header.Get("User-Agent")
		w.Write(payload)
	})
	o := opts(s, t.TempDir())
	o.UserAgent = "Test/1"
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if accept != "" && strings.Contains(accept, "github") || ua != "Test/1" {
		t.Errorf("accept %q, user-agent %q", accept, ua)
	}
}

func TestContentRangeParsing(t *testing.T) {
	cases := []struct {
		in                string
		start, end, total int64
		ok                bool
	}{
		{"bytes 100-199/200", 100, 199, 200, true},
		{"bytes 0-9/*", 0, 9, 0, true},
		{"bytes 5-1", 0, 0, 0, false},
		{"", 0, 0, 0, false},
		{"bytes x-9/10", 0, 0, 0, false},
	}
	for _, c := range cases {
		s, e, tot, ok := ParseContentRange(c.in)
		if c.in == "bytes 5-1" { // no total at all
			if ok {
				t.Errorf("%q accepted", c.in)
			}
			continue
		}
		if ok != c.ok || (ok && (s != c.start || e != c.end || tot != c.total)) {
			t.Errorf("%q -> %d %d %d %v", c.in, s, e, tot, ok)
		}
	}
}
