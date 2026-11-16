package models

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/download"
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
	stall   time.Duration      // no-data limit of a transfer; 0 = download.DefaultStallTimeout
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

// fetch downloads url to dest through the shared downloader: it continues a
// partial file from an earlier attempt, gives up on a connection that stops
// delivering, and renames only after the SHA-256 matches want. There is no
// automatic retry here: a failed download is shown on the page and the user
// decides to click Download again (which then continues from the partial file).
func (m *Manager) fetch(ctx context.Context, e Entry, url, dest, want string, track bool) error {
	o := download.Options{
		Client:       m.client,
		URL:          url,
		Dest:         dest,
		SHA256:       want,
		UserAgent:    "ClipboardTranslator/" + m.version,
		Label:        "the model host",
		ExpectedSize: 0,
		DiskHint:     fmt.Sprintf("%s needs about %s", e.Name, e.SizeLabel),
		StallTimeout: m.stall,
		// A template is small; do not keep a broken partial of it around.
		DiscardPartialOnFailure: !track,
	}
	if track {
		o.ExpectedSize = e.Bytes
		o.Phase = func(p string) { m.set(func(s *DownloadState) { s.Phase = p }) }
		o.Progress = func(done, total int64) {
			m.set(func(s *DownloadState) {
				s.Done = done
				if total > 0 {
					s.Total = total
				}
			})
		}
	}
	return download.Fetch(ctx, o)
}
