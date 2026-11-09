package models

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

// DownloadTransport replaces the HTTP transport; tests set one that only
// reaches loopback, so no test can contact the real host.
var DownloadTransport http.RoundTripper

type DownloadState struct {
	Key   string `json:"key"`
	Phase string `json:"phase"` // checking, downloading, template, done, failed, cancelled
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

func (s DownloadState) Running() bool {
	return s.Phase == "checking" || s.Phase == "downloading" || s.Phase == "template"
}

type Manager struct {
	dir     string
	baseURL string
	version string // for the User-Agent
	client  *http.Client
	events  chan DownloadState // final states, for the main loop

	mu       sync.Mutex
	state    DownloadState
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	deleting map[string]bool
}

func NewManager(dir, version string) *Manager {
	m := &Manager{dir: dir, version: version, baseURL: modelHost, events: make(chan DownloadState, 4), deleting: make(map[string]bool)}
	m.client = &http.Client{
		Transport: DownloadTransport,
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

// Events delivers the final state of each download, for the main loop. The
// channel is buffered and never closed.
func (m *Manager) Events() <-chan DownloadState { return m.events }

// State is a copy of the current or last download (Key is empty before the first).
func (m *Manager) State() DownloadState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Downloading reports whether key is being downloaded now.
func (m *Manager) Downloading(key string) bool {
	s := m.State()
	return s.Key == key && s.Running()
}

// ClearKey clears the recorded state if it belongs to key and is not running.
func (m *Manager) ClearKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Key == key && !m.state.Running() {
		m.state = DownloadState{}
	}
}

// ReserveDelete reserves key for deletion so Start will refuse to download it
// while the reservation is held. If key is currently being downloaded, ReserveDelete
// returns an error. The returned release function must be called when deletion
// completes.
func (m *Manager) ReserveDelete(key string) (func(), error) {
	e, ok := ByKey(key)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Key == key && m.state.Running() {
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
func (m *Manager) Start(key string) error {
	e, ok := ByKey(key)
	if !ok {
		return fmt.Errorf("unknown model %q", key)
	}
	if e.Installed(m.dir) && e.TemplateInstalled(m.dir) {
		return fmt.Errorf("%s is already installed", e.Name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleting != nil && m.deleting[key] {
		return fmt.Errorf("%s is currently being deleted; try again in a moment", e.Name)
	}
	if m.state.Running() {
		return errors.New("another model is being downloaded; wait for it or cancel it first")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = DownloadState{Key: key, Phase: "checking", Total: e.Bytes}
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
func (m *Manager) Cancel() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close cancels a running download and waits until its file is closed.
func (m *Manager) Close() {
	m.Cancel()
	m.wg.Wait()
}

// SetBaseURL points the manager at another host. Tests use it to reach a fake one.
func (m *Manager) SetBaseURL(u string) { m.baseURL = u }

// SetState overwrites the current state without starting anything. It exists
// so that tests of other packages can stage "a download is running".
func (m *Manager) SetState(s DownloadState) { m.set(func(cur *DownloadState) { *cur = s }) }

func (m *Manager) set(f func(*DownloadState)) {
	m.mu.Lock()
	f(&m.state)
	m.mu.Unlock()
}

func (m *Manager) finish(key string, err error) {
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
	m.set(func(s *DownloadState) { *s = final })
	select {
	case m.events <- final:
	default:
	}
}

func (m *Manager) run(ctx context.Context, e Entry) {
	if err := os.MkdirAll(filepath.Join(m.dir, "models"), 0o755); err != nil {
		m.finish(e.Key, err)
		return
	}
	base := fmt.Sprintf("%s/pfnet/%s/resolve/%s/", m.baseURL, e.Repo, e.Revision)
	// A previous attempt may have verified the GGUF before the template failed.
	// Keep that file and resume only the missing part of the installation.
	if !e.Installed(m.dir) {
		if err := m.fetch(ctx, e, base+e.File, e.Path(m.dir), e.Sha256, true); err != nil {
			m.finish(e.Key, err)
			return
		}
	}
	if !e.TemplateInstalled(m.dir) {
		m.set(func(s *DownloadState) { s.Phase = "template" })
		tpl := e.TemplatePath(m.dir)
		if err := m.fetch(ctx, e, base+"chat_template.jinja", tpl, e.TemplateSha256, false); err != nil {
			m.finish(e.Key, err)
			return
		}
	}
	m.finish(e.Key, nil)
}

// fetch downloads url to dest through dest+".partial", continuing a partial file
// from an earlier attempt, and renames it only after its SHA-256 matches want.
func (m *Manager) fetch(ctx context.Context, e Entry, url, dest, want string, track bool) error {
	partial := dest + ".partial"
	h := sha256.New()
	var have int64
	if info, err := os.Stat(partial); err == nil && info.Mode().IsRegular() {
		if track {
			m.set(func(s *DownloadState) { s.Phase = "checking" })
		}
		n, err := hashFile(ctx, partial, h)
		if err != nil {
			return err
		}
		have = n
	}
	if track {
		m.set(func(s *DownloadState) { s.Phase, s.Done = "downloading", have })
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ClipboardTranslator/"+m.version)
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
		m.set(func(s *DownloadState) { s.Done, s.Total = have, total })
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
				m.set(func(s *DownloadState) { s.Done = have })
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
func (m *Manager) verifyAndMove(e Entry, partial, dest, want string, h hash.Hash) error {
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
