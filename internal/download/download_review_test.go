package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scripted is a transport without a network: each request gets the answer of
// its turn, so a test can produce bodies a real server cannot (a read error
// that is no net.Error, a body without a length that just ends).
type scripted struct {
	reqs    []*http.Request
	answers []func(r *http.Request) *http.Response
}

func (s *scripted) RoundTrip(r *http.Request) (*http.Response, error) {
	i := len(s.reqs)
	s.reqs = append(s.reqs, r)
	if i >= len(s.answers) {
		return nil, fmt.Errorf("unexpected request %d", i+1)
	}
	resp := s.answers[i](r)
	resp.Request = r
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	return resp, nil
}

// brokenBody delivers data and then fails with err instead of io.EOF.
type brokenBody struct {
	r   io.Reader
	err error
}

func (b *brokenBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}
func (b *brokenBody) Close() error { return nil }

func full(code int, body []byte, length int64) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: length}
}

// rest answers a Range request with the rest of the payload.
func rest(r *http.Request) *http.Response {
	var from int
	if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &from); err != nil {
		return full(200, payload, int64(len(payload)))
	}
	resp := full(206, payload[from:], int64(len(payload)-from))
	resp.Header = http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", from, len(payload)-1, len(payload))}}
	return resp
}

func scriptedOpts(tr *scripted, dir string) Options {
	return Options{
		Client: &http.Client{Transport: tr}, URL: "http://host.invalid/f", Dest: filepath.Join(dir, "f.bin"), SHA256: sum(payload),
		StallTimeout: 5 * time.Second, Retries: 3, RetryBase: time.Millisecond,
	}
}

// HTTP/2 reports a transfer that broke off as a stream or GOAWAY error, which
// is neither a net.Error nor io.ErrUnexpectedEOF. It must be retried all the same.
func TestBodyErrorOfAnyTypeIsRetriedFromThePartial(t *testing.T) {
	half := len(payload) / 2
	tr := &scripted{answers: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return &http.Response{StatusCode: 200, ContentLength: int64(len(payload)),
				Body: &brokenBody{r: bytes.NewReader(payload[:half]), err: errors.New("stream error: stream ID 1; INTERNAL_ERROR")}}
		},
		rest,
	}}
	o := scriptedOpts(tr, t.TempDir())
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	if len(tr.reqs) != 2 || tr.reqs[1].Header.Get("Range") != fmt.Sprintf("bytes=%d-", half) {
		t.Errorf("the second request must continue at %d", half)
	}
}

// A body without a length that simply ends (a connection closed by a proxy)
// looks complete. With the size known it is an interrupted transfer: the
// partial file stays and the rest is fetched, instead of failing the hash
// check and throwing everything away.
func TestShortBodyWithoutLengthIsContinuedWhenTheSizeIsKnown(t *testing.T) {
	half := len(payload) / 2
	tr := &scripted{answers: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response { return full(200, payload[:half], -1) },
		rest,
	}}
	o := scriptedOpts(tr, t.TempDir())
	o.ExpectedSize = int64(len(payload))
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	if len(tr.reqs) != 2 || tr.reqs[1].Header.Get("Range") == "" {
		t.Errorf("requests: %d", len(tr.reqs))
	}
}

// endless never stops delivering.
type endless struct{ n int64 }

func (e *endless) Read(p []byte) (int, error) { e.n += int64(len(p)); return len(p), nil }
func (e *endless) Close() error               { return nil }

func TestServerSendingMoreThanTheExpectedSizeIsCutOff(t *testing.T) {
	body := &endless{}
	tr := &scripted{answers: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return &http.Response{StatusCode: 200, Body: body, ContentLength: -1}
		},
	}}
	o := scriptedOpts(tr, t.TempDir())
	o.ExpectedSize = int64(len(payload))
	err := Fetch(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v", err)
	}
	if len(tr.reqs) != 1 {
		t.Errorf("an oversized answer was retried (%d requests)", len(tr.reqs))
	}
	if body.n > int64(len(payload))+2*bufSize {
		t.Errorf("read %d bytes of a %d byte file", body.n, len(payload))
	}
	assertNoFinal(t, o)
	if _, err := os.Stat(o.Dest + ".partial"); !os.IsNotExist(err) {
		t.Error("the oversized partial file was kept")
	}
}

// Without a checksum nothing says the partial file belongs to this file.
func TestPartialIsNotContinuedWithoutAChecksum(t *testing.T) {
	tr := &scripted{answers: []func(*http.Request) *http.Response{rest}}
	o := scriptedOpts(tr, t.TempDir())
	o.SHA256 = ""
	os.WriteFile(o.Dest+".partial", []byte("left over from some other file"), 0o644)
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	if got := tr.reqs[0].Header.Get("Range"); got != "" {
		t.Errorf("Range = %q, want a full download", got)
	}
}

func TestRangeAnswerThatDoesNotContinueThePartialIsRefused(t *testing.T) {
	for name, cr := range map[string]string{
		"wrong start": fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)),
		"no header":   "",
	} {
		t.Run(name, func(t *testing.T) {
			tr := &scripted{answers: []func(*http.Request) *http.Response{
				func(*http.Request) *http.Response {
					resp := full(206, payload, int64(len(payload)))
					if cr != "" {
						resp.Header = http.Header{"Content-Range": {cr}}
					}
					return resp
				},
			}}
			o := scriptedOpts(tr, t.TempDir())
			before := payload[:1000]
			os.WriteFile(o.Dest+".partial", before, 0o644)
			if err := Fetch(context.Background(), o); err == nil || !strings.Contains(err.Error(), "range") {
				t.Fatalf("err = %v", err)
			}
			if len(tr.reqs) != 1 {
				t.Errorf("retried %d times", len(tr.reqs)-1)
			}
			assertNoFinal(t, o)
			if got, _ := os.ReadFile(o.Dest + ".partial"); !bytes.Equal(got, before) {
				t.Error("the partial file was changed by an answer that does not continue it")
			}
		})
	}
}

// The transport would ask for gzip on the first request and not on a Range
// request; the file on disk has to be one representation.
func TestBodyIsRequestedAndAcceptedUnencodedOnly(t *testing.T) {
	tr := &scripted{answers: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			resp := full(200, payload, int64(len(payload)))
			resp.Header = http.Header{"Content-Encoding": {"gzip"}}
			return resp
		},
	}}
	o := scriptedOpts(tr, t.TempDir())
	err := Fetch(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "gzip") || len(tr.reqs) != 1 {
		t.Fatalf("err = %v, requests = %d", err, len(tr.reqs))
	}
	if got := tr.reqs[0].Header.Get("Accept-Encoding"); got != "identity" {
		t.Errorf("Accept-Encoding = %q", got)
	}
	assertNoFinal(t, o)
}

// "Continuing ..." is only true once the server agreed to the range.
func TestOnResumeOnlyWhenTheServerContinues(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(*http.Request) *http.Response
		want   int
	}{
		{"206", rest, 1},
		{"200", func(*http.Request) *http.Response { return full(200, payload, int64(len(payload))) }, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := &scripted{answers: []func(*http.Request) *http.Response{c.answer}}
			o := scriptedOpts(tr, t.TempDir())
			os.WriteFile(o.Dest+".partial", payload[:1000], 0o644)
			calls := 0
			o.OnResume = func(have int64) {
				calls++
				if have != 1000 {
					t.Errorf("have = %d", have)
				}
			}
			if err := Fetch(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			assertInstalled(t, o)
			if calls != c.want {
				t.Errorf("OnResume called %d times, want %d", calls, c.want)
			}
		})
	}
}

// Hugging Face and GitHub answer with a redirect to a CDN on another host:
// the Range of a resumed download has to arrive there.
func TestRangeSurvivesARedirectToAnotherHost(t *testing.T) {
	var gotRange atomic.Value
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange.Store(r.Header.Get("Range"))
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(payload))
	}))
	defer cdn.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Another host name for the same loopback server.
		http.Redirect(w, r, strings.Replace(cdn.URL, "127.0.0.1", "localhost", 1)+"/blob", http.StatusFound)
	}))
	defer front.Close()
	o := Options{Client: front.Client(), URL: front.URL + "/f", Dest: filepath.Join(t.TempDir(), "f.bin"), SHA256: sum(payload), StallTimeout: 5 * time.Second}
	os.WriteFile(o.Dest+".partial", payload[:4096], 0o644)
	if err := Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	assertInstalled(t, o)
	if got, _ := gotRange.Load().(string); got != "bytes=4096-" {
		t.Errorf("Range at the redirect target = %q", got)
	}
}

// The wait between two attempts ends at once on a cancel.
func TestCancelDuringTheRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &scripted{answers: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response { return full(503, nil, 0) },
	}}
	o := scriptedOpts(tr, t.TempDir())
	o.RetryBase = time.Hour
	o.OnRetry = func(int, error, time.Duration) { cancel() }
	done := make(chan error, 1)
	go func() { done <- Fetch(ctx, o) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Fetch did not return after the cancel")
	}
}
