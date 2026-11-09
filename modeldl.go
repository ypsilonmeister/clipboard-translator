package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// The download manager fetches a model from Hugging Face when the user asks for
// it on the settings page. It belongs to the app, not to the settings server: a
// download of several GB outlives the page. It is the only code that contacts
// the model host, only on a click, only for the pinned revision of a catalog
// entry, and it refuses a file whose SHA-256 is not the pinned one.

const modelHost = "https://huggingface.co"

// downloadTransport replaces the HTTP transport; tests set one that only
// reaches loopback, so no test can contact the real host.
var downloadTransport http.RoundTripper

type downloadState struct {
	Key   string `json:"key"`
	Phase string `json:"phase"` // checking, downloading, template, done, failed, cancelled
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

func (s downloadState) running() bool {
	return s.Phase == "checking" || s.Phase == "downloading" || s.Phase == "template"
}

type downloadManager struct {
	dir     string
	baseURL string
	client  *http.Client
	events  chan downloadState // final states, for the main loop

	mu       sync.Mutex
	state    downloadState
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	deleting map[string]bool
}

func newDownloadManager(dir string) *downloadManager {
	m := &downloadManager{dir: dir, baseURL: modelHost, events: make(chan downloadState, 4), deleting: make(map[string]bool)}
	m.client = &http.Client{
		Transport: downloadTransport,
		// Hugging Face answers with a redirect to its CDN, a different host, so
		// hosts may change; only a step down from https to http is refused.
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
	return m
}

// State is a copy of the current or last download (Key is empty before the first).
func (m *downloadManager) State() downloadState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Downloading reports whether key is being downloaded now.
func (m *downloadManager) Downloading(key string) bool {
	s := m.State()
	return s.Key == key && s.running()
}

// ClearKey clears the recorded state if it belongs to key and is not running.
func (m *downloadManager) ClearKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Key == key && !m.state.running() {
		m.state = downloadState{}
	}
}

// ReserveDelete reserves key for deletion so Start will refuse to download it
// while the reservation is held. If key is currently being downloaded, ReserveDelete
// returns an error. The returned release function must be called when deletion
// completes.
func (m *downloadManager) ReserveDelete(key string) (func(), error) {
	e, ok := modelByKey(key)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Key == key && m.state.running() {
		return nil, fmt.Errorf("%s is currently being downloaded", e.Name)
	}
	if m.deleting == nil {
		m.deleting = make(map[string]bool)
	}
	if m.deleting[key] {
		return nil, fmt.Errorf("%s is currently being deleted; try again in a moment", e.Name)
	}
	m.deleting[key] = true
	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.deleting, key)
			m.mu.Unlock()
		})
	}
	return release, nil
}

// Start downloads missing installation files in the background, one model at
// a time. A model with both its GGUF and the shared template is refused.
func (m *downloadManager) Start(key string) error {
	e, ok := modelByKey(key)
	if !ok {
		return fmt.Errorf("unknown model %q", key)
	}
	if e.installed(m.dir) && e.templateInstalled(m.dir) {
		return fmt.Errorf("%s is already installed", e.Name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleting != nil && m.deleting[key] {
		return fmt.Errorf("%s is currently being deleted; try again in a moment", e.Name)
	}
	if m.state.running() {
		return errors.New("another model is being downloaded; wait for it or cancel it first")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = downloadState{Key: key, Phase: "checking", Total: e.Bytes}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, e)
	}()
	return nil
}

// Cancel stops the running download. The partial file stays, so the next
// download of that model continues where this one stopped.
func (m *downloadManager) Cancel() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close cancels a running download and waits until its file is closed.
func (m *downloadManager) Close() {
	m.Cancel()
	m.wg.Wait()
}

func (m *downloadManager) set(f func(*downloadState)) {
	m.mu.Lock()
	f(&m.state)
	m.mu.Unlock()
}

func (m *downloadManager) finish(key string, err error) {
	final := m.State()
	switch {
	case err == nil:
		final.Phase, final.Error = "done", ""
	case errors.Is(err, context.Canceled):
		final.Phase, final.Error = "cancelled", ""
	default:
		final.Phase, final.Error = "failed", err.Error()
		log.Printf("model download %s: %v", key, err)
	}
	m.set(func(s *downloadState) { *s = final })
	select {
	case m.events <- final:
	default:
	}
}

func (m *downloadManager) run(ctx context.Context, e modelEntry) {
	if err := os.MkdirAll(filepath.Join(m.dir, "models"), 0o755); err != nil {
		m.finish(e.Key, err)
		return
	}
	base := fmt.Sprintf("%s/pfnet/%s/resolve/%s/", m.baseURL, e.Repo, e.Revision)
	// A previous attempt may have verified the GGUF before the template failed.
	// Keep that file and resume only the missing part of the installation.
	if !e.installed(m.dir) {
		if err := m.fetch(ctx, e, base+e.File, e.path(m.dir), e.Sha256, true); err != nil {
			m.finish(e.Key, err)
			return
		}
	}
	if !e.templateInstalled(m.dir) {
		m.set(func(s *downloadState) { s.Phase = "template" })
		tpl := e.templatePath(m.dir)
		if err := m.fetch(ctx, e, base+"chat_template.jinja", tpl, e.TemplateSha256, false); err != nil {
			m.finish(e.Key, err)
			return
		}
	}
	m.finish(e.Key, nil)
}

// fetch downloads url to dest through dest+".partial", continuing a partial file
// from an earlier attempt, and renames it only after its SHA-256 matches want.
func (m *downloadManager) fetch(ctx context.Context, e modelEntry, url, dest, want string, track bool) error {
	partial := dest + ".partial"
	h := sha256.New()
	var have int64
	if info, err := os.Stat(partial); err == nil && info.Mode().IsRegular() {
		if track {
			m.set(func(s *downloadState) { s.Phase = "checking" })
		}
		n, err := hashFile(ctx, partial, h)
		if err != nil {
			return err
		}
		have = n
	}
	if track {
		m.set(func(s *downloadState) { s.Phase, s.Done = "downloading", have })
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ClipboardTranslator/"+version)
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the model host: %w", err)
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	total := e.Bytes
	switch resp.StatusCode {
	case http.StatusOK:
		if have > 0 { // the server ignored the range: start again
			h.Reset()
			have = 0
			flags |= os.O_TRUNC
		}
		if resp.ContentLength > 0 {
			total = resp.ContentLength
		}
		flags |= os.O_APPEND
	case http.StatusPartialContent:
		start, _, size, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != have {
			return fmt.Errorf("the model host answered with a range that does not continue the partial file (%q)", resp.Header.Get("Content-Range"))
		}
		if size > 0 {
			total = size
		}
		flags |= os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		// The partial file may already be everything: fall through to the check.
		if have == 0 {
			return errors.New("the model host refused the request (HTTP 416)")
		}
		return m.verifyAndMove(e, partial, dest, want, h)
	default:
		if !track {
			_ = os.Remove(partial)
		}
		return fmt.Errorf("the model host answered HTTP %d", resp.StatusCode)
	}
	if track {
		m.set(func(s *downloadState) { s.Done, s.Total = have, total })
	}

	f, err := os.OpenFile(partial, flags, 0o644)
	if err != nil {
		if !track {
			_ = os.Remove(partial)
		}
		return fmt.Errorf("could not write %s: %w", partial, err)
	}
	buf := make([]byte, 256<<10)
	var copyErr error
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				copyErr = fmt.Errorf("could not write %s (is the disk full? %s needs about %s): %w", filepath.Base(partial), e.Name, e.SizeLabel, werr)
				break
			}
			h.Write(buf[:n])
			have += int64(n)
			if track {
				m.set(func(s *downloadState) { s.Done = have })
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				copyErr = rerr
			}
			break
		}
	}
	if err := f.Close(); err != nil && copyErr == nil {
		copyErr = err
	}
	if copyErr != nil {
		if ctx.Err() != nil {
			return ctx.Err() // cancelled: the partial file stays for the next try
		}
		if !track {
			_ = os.Remove(partial)
		}
		return copyErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.verifyAndMove(e, partial, dest, want, h)
}

// verifyAndMove gives the downloaded file its final name only if its SHA-256 is
// the pinned one. A file that does not match is deleted.
func (m *downloadManager) verifyAndMove(e modelEntry, partial, dest, want string, h hash.Hash) error {
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		_ = os.Remove(partial)
		return fmt.Errorf("the downloaded %s does not match the expected SHA-256 and was deleted; try again", filepath.Base(dest))
	}
	// A file of that name that is too small is a broken earlier download.
	if info, err := os.Stat(dest); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(dest)
	}
	return os.Rename(partial, dest)
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

// parseContentRange reads "bytes 100-199/200".
func parseContentRange(v string) (start, end, size int64, ok bool) {
	v = strings.TrimSpace(strings.TrimPrefix(v, "bytes "))
	rng, tot, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, 0, false
	}
	a, b, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, 0, false
	}
	var err error
	if start, err = strconv.ParseInt(a, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if end, err = strconv.ParseInt(b, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if tot != "*" {
		if size, err = strconv.ParseInt(tot, 10, 64); err != nil {
			return 0, 0, 0, false
		}
	}
	return start, end, size, true
}
