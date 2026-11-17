// Package download is the one HTTP file downloader behind both the first-run
// setup (internal/setup) and the Settings page's model download
// (internal/models). It resumes from "<dest>.partial" with a Range request,
// checks the SHA-256 before the file gets its final name, treats a connection
// that delivers no byte for a while as failed, and retries a few times, each
// time continuing from the partial file.
//
// It is a leaf package: it imports nothing of this module and never decides
// by itself to contact the network; callers run it only for something the
// user started (the setup, or the Download button).
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// DefaultStallTimeout is how long a transfer may deliver no byte before the
	// attempt fails. It is not a limit on the whole download.
	DefaultStallTimeout = 60 * time.Second
	// DefaultRetries is the number of automatic retries after a failed attempt.
	DefaultRetries = 3
	// DefaultRetryBase is the wait before the first retry; it doubles each time.
	DefaultRetryBase = 2 * time.Second

	// maxRetryWait caps the doubling wait between two attempts.
	maxRetryWait = 5 * time.Minute

	bufSize = 256 << 10
)

// defaultClient is used when Options.Client is nil. Like the callers' own
// clients it has no Timeout (the stall check is per read) and follows a
// redirect to another host (the CDN), but not one that steps down from https
// to http.
var defaultClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("refusing a redirect from https to http")
		}
		return nil
	},
}

// retryWait is base doubled attempt times, capped at maxRetryWait. It never
// overflows, whatever the number of retries a caller asks for.
func retryWait(base time.Duration, attempt int) time.Duration {
	if base >= maxRetryWait {
		return maxRetryWait
	}
	w := base
	for i := 0; i < attempt; i++ {
		w *= 2
		if w >= maxRetryWait {
			return maxRetryWait
		}
	}
	return w
}

// Options describes one download. Zero values are safe: a default client, no
// retries, the default stall timeout, no progress reports.
type Options struct {
	// Client must have no Timeout: that would cap the whole transfer. nil uses a
	// client that refuses a redirect from https to http.
	Client *http.Client
	URL    string
	Dest   string // final name; the data goes to Dest+".partial" first
	// SHA256 is lowercase or uppercase hex; empty skips the check. Without it a
	// partial file cannot be told from one of another file, so it is never
	// continued: the download starts from zero.
	SHA256 string

	UserAgent string
	Accept    string // sent only when set
	Label     string // how errors name the server; default is the URL

	// ExpectedSize is the exact size of the file, 0 when unknown. A partial file
	// larger than it is thrown away, a server that sends more is cut off, and a
	// body that ends before it counts as an interrupted transfer.
	ExpectedSize int64
	// DiskHint is appended to the "is the disk full?" message, e.g. the size the
	// model needs.
	DiskHint string

	StallTimeout time.Duration // 0 = DefaultStallTimeout
	Retries      int           // automatic retries after the first attempt
	RetryBase    time.Duration // 0 = DefaultRetryBase; doubles per retry, up to 5 minutes

	// DiscardPartialOnFailure removes the partial file when the download fails
	// for good (never on cancel). A SHA-256 mismatch removes it regardless.
	DiscardPartialOnFailure bool

	// Callbacks, all optional, all called from the goroutine running Fetch.
	Phase    func(phase string)                         // "checking" (hashing a partial file), "downloading"
	Progress func(done, total int64)                    // total is 0 when unknown
	OnResume func(have int64)                           // the server agreed to continue a partial file of have bytes
	OnRetry  func(n int, err error, wait time.Duration) // retry number n (1-based) starts after wait
}

// HashMismatchError reports a finished file whose SHA-256 is not the pinned
// one. The partial file is deleted.
type HashMismatchError struct{ Name string }

func (e *HashMismatchError) Error() string {
	return fmt.Sprintf("SHA-256 mismatch: the downloaded %s does not match the pinned checksum and was deleted; try again", e.Name)
}

// StatusError is an HTTP answer other than the expected 200/206.
type StatusError struct {
	Label string
	Code  int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s answered HTTP %d", e.Label, e.Code) }

// ErrStalled is wrapped by the error of an attempt that received no data for
// the stall timeout.
var ErrStalled = errors.New("download stalled")

// permanent marks an error no retry can fix (disk, rename, hash, bad range).
type permanent struct{ err error }

func (p *permanent) Error() string { return p.err.Error() }
func (p *permanent) Unwrap() error { return p.err }

// transient marks an error worth another attempt whatever its type: a body
// that broke off mid-transfer. HTTP/2 reports those as stream or GOAWAY errors
// that are neither a net.Error nor io.ErrUnexpectedEOF.
type transient struct{ err error }

func (t *transient) Error() string { return t.err.Error() }
func (t *transient) Unwrap() error { return t.err }

// errRestart is internal: the partial file was discarded, try again from zero.
var errRestart = errors.New("restart from the beginning")

// Fetch downloads o.URL to o.Dest. On success the file exists under its final
// name with a verified SHA-256 and no partial file is left. On failure no file
// has the final name; the partial file stays (for a later resume) unless it
// failed the hash check or DiscardPartialOnFailure is set. A cancelled ctx
// returns ctx.Err() and keeps the partial file.
func Fetch(ctx context.Context, o Options) error {
	if o.Client == nil {
		o.Client = defaultClient
	}
	if o.StallTimeout <= 0 {
		o.StallTimeout = DefaultStallTimeout
	}
	if o.RetryBase <= 0 {
		o.RetryBase = DefaultRetryBase
	}
	if o.Label == "" {
		o.Label = o.URL
	}
	partial := o.Dest + ".partial"
	restarted := false
	var err error
	for attempt := 0; ; {
		err = once(ctx, &o, partial)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errRestart) && !restarted {
			restarted = true
			continue
		}
		if !retryable(err) || attempt >= o.Retries {
			break
		}
		wait := retryWait(o.RetryBase, attempt)
		attempt++
		if o.OnRetry != nil {
			o.OnRetry(attempt, err, wait)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	if errors.Is(err, errRestart) {
		err = &StatusError{Label: o.Label, Code: http.StatusRequestedRangeNotSatisfiable}
	}
	if o.DiscardPartialOnFailure {
		_ = os.Remove(partial)
	}
	return err
}

func retryable(err error) bool {
	var p *permanent
	if errors.As(err, &p) {
		return false
	}
	if errors.Is(err, ErrStalled) {
		return true
	}
	var tr *transient
	if errors.As(err, &tr) {
		return true
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code >= 500 || se.Code == http.StatusRequestTimeout || se.Code == http.StatusTooManyRequests
	}
	// A *url.Error is a net.Error for every cause (a refused https->http
	// redirect, a blocked test host), so look at what it wraps.
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// once is one attempt: hash what is on disk, request the rest, append it.
func once(ctx context.Context, o *Options, partial string) error {
	h := sha256.New()
	var have int64
	if info, err := os.Stat(partial); err == nil && info.Mode().IsRegular() {
		if o.Phase != nil {
			o.Phase("checking")
		}
		n, err := hashFile(ctx, partial, h)
		if err != nil {
			return err
		}
		have = n
		if o.SHA256 == "" || (o.ExpectedSize > 0 && have > o.ExpectedSize) {
			// Longer than the file can be, or nothing to check it against: it
			// cannot be trusted to be a prefix of the file.
			if err := os.Remove(partial); err != nil {
				return &permanent{err}
			}
			h.Reset()
			have = 0
		}
	}
	if o.Phase != nil {
		o.Phase("downloading")
	}
	if o.Progress != nil {
		o.Progress(have, o.ExpectedSize)
	}

	// The stall timer covers the connect, the headers and every read.
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(o.StallTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	stallErr := func() error {
		return fmt.Errorf("%w: no data from %s for %s", ErrStalled, o.Label, o.StallTimeout)
	}

	req, err := http.NewRequestWithContext(actx, http.MethodGet, o.URL, nil)
	if err != nil {
		return &permanent{err}
	}
	if o.UserAgent != "" {
		req.Header.Set("User-Agent", o.UserAgent)
	}
	if o.Accept != "" {
		req.Header.Set("Accept", o.Accept)
	}
	// The bytes on disk must be the file itself: without this the transport asks
	// for gzip on the first request (and not on a Range request), and offsets
	// and Content-Length would describe two different representations.
	req.Header.Set("Accept-Encoding", "identity")
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		if stalled.Load() && ctx.Err() == nil {
			return stallErr()
		}
		return fmt.Errorf("could not reach %s: %w", o.Label, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
		if ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); ce != "" && ce != "identity" {
			return &permanent{fmt.Errorf("%s answered with a %s-encoded body although none was asked for", o.Label, ce)}
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	total := o.ExpectedSize // for progress
	var declared int64      // what this response says the body ends at; 0 = unknown
	switch resp.StatusCode {
	case http.StatusOK:
		if have > 0 { // the server ignored the range: start again
			h.Reset()
			have = 0
		}
		flags |= os.O_TRUNC
		if resp.ContentLength > 0 {
			total, declared = resp.ContentLength, resp.ContentLength
		}
	case http.StatusPartialContent:
		start, _, size, ok := ParseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != have {
			// What the body holds is unknown, so nothing is written. The partial
			// file is not at fault and stays: throwing away what may be several
			// GB because of one bad answer costs more than failing here.
			return &permanent{fmt.Errorf("%s answered with a range that does not continue the partial file (Content-Range %q); try again, and if it keeps happening delete %s to start over", o.Label, resp.Header.Get("Content-Range"), filepath.Base(partial))}
		}
		if size > 0 {
			total, declared = size, size
		}
		flags |= os.O_APPEND
		if have > 0 && o.OnResume != nil {
			o.OnResume(have)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		if have == 0 {
			return &StatusError{Label: o.Label, Code: resp.StatusCode}
		}
		// The partial file may already be everything: accept it if it checks out,
		// otherwise it is useless and the next try starts from zero.
		if o.SHA256 != "" && strings.EqualFold(hex.EncodeToString(h.Sum(nil)), o.SHA256) {
			return finish(o, partial, h, have)
		}
		if err := os.Remove(partial); err != nil {
			return &permanent{err}
		}
		return errRestart
	default:
		return &StatusError{Label: o.Label, Code: resp.StatusCode}
	}
	if o.Progress != nil {
		o.Progress(have, total)
	}

	f, err := os.OpenFile(partial, flags, 0o644)
	if err != nil {
		return &permanent{fmt.Errorf("could not write %s: %w", partial, err)}
	}
	buf := make([]byte, bufSize)
	var copyErr error
	tooLong := false
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			timer.Reset(o.StallTimeout)
			if _, werr := f.Write(buf[:n]); werr != nil {
				hint := ""
				if o.DiskHint != "" {
					hint = " " + o.DiskHint
				}
				copyErr = &permanent{fmt.Errorf("could not write %s (is the disk full?%s): %w", filepath.Base(partial), hint, werr)}
				break
			}
			h.Write(buf[:n])
			have += int64(n)
			if o.ExpectedSize > 0 && have > o.ExpectedSize {
				// Not the pinned file, and possibly endless: stop here.
				tooLong = true
				break
			}
			if o.Progress != nil {
				o.Progress(have, total)
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				copyErr = &transient{fmt.Errorf("the connection to %s broke off: %w", o.Label, rerr)}
			}
			break
		}
	}
	if err := f.Close(); err != nil && copyErr == nil {
		copyErr = &permanent{err}
	}
	if tooLong {
		_ = os.Remove(partial)
		return &permanent{fmt.Errorf("%s sent more than the %d bytes of %s; the partial file was deleted", o.Label, o.ExpectedSize, filepath.Base(o.Dest))}
	}
	if copyErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if stalled.Load() {
			return stallErr()
		}
		return copyErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if declared == 0 {
		// No length in the answer (chunked, or a body that ends with the
		// connection): a short file is a cut transfer, not a bad download.
		declared = o.ExpectedSize
	}
	if declared > 0 && have < declared {
		return fmt.Errorf("the connection to %s ended early: %w", o.Label, io.ErrUnexpectedEOF)
	}
	return finish(o, partial, h, have)
}

// finish checks the SHA-256 and gives the file its final name.
func finish(o *Options, partial string, h hash.Hash, have int64) error {
	if have == 0 {
		_ = os.Remove(partial)
		return &permanent{fmt.Errorf("empty download: %s", o.Label)}
	}
	if o.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), o.SHA256) {
		_ = os.Remove(partial)
		return &permanent{&HashMismatchError{Name: filepath.Base(o.Dest)}}
	}
	// A file of that name left by an earlier broken download is replaced.
	if info, err := os.Stat(o.Dest); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(o.Dest)
	}
	if err := os.Rename(partial, o.Dest); err != nil {
		return &permanent{err}
	}
	return nil
}

// hashFile feeds the file to h and returns its length, stopping when ctx is cancelled.
func hashFile(ctx context.Context, path string, h hash.Hash) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		k, err := f.Read(buf)
		if k > 0 {
			h.Write(buf[:k])
			n += int64(k)
		}
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}

// ParseContentRange reads the Content-Range of a 206 answer, "bytes 100-199/200";
// size is 0 when the total is "*". The unit is matched without regard to case
// and blanks around the unit, "-" and "/" are allowed, but the numbers have to
// make sense: start <= end, and end < size when the size is given. The
// unsatisfied form "bytes */200" is not a range and is refused.
func ParseContentRange(v string) (start, end, size int64, ok bool) {
	v = strings.TrimSpace(v)
	const unit = "bytes"
	if len(v) <= len(unit) || !strings.EqualFold(v[:len(unit)], unit) {
		return 0, 0, 0, false
	}
	v = v[len(unit):]
	if v[0] != ' ' && v[0] != '	' { // "bytes100-199/200" is not the unit
		return 0, 0, 0, false
	}
	rng, tot, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, 0, false
	}
	a, b, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, 0, false
	}
	if start, ok = parseOffset(a); !ok {
		return 0, 0, 0, false
	}
	if end, ok = parseOffset(b); !ok || end < start {
		return 0, 0, 0, false
	}
	if tot = strings.TrimSpace(tot); tot != "*" {
		if size, ok = parseOffset(tot); !ok || end >= size {
			return 0, 0, 0, false
		}
	}
	return start, end, size, true
}

// parseOffset reads a byte position: digits only (no sign), blanks around it allowed.
func parseOffset(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}
