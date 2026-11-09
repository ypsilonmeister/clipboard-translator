package models

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/models/modeltest"
)

func sum(b []byte) string { return modeltest.Sum(b) }

// dlFixture is a fake model host (see package modeltest) plus a Manager aimed at it.
type dlFixture struct {
	*modeltest.Host
	t   *testing.T
	dir string
	m   *Manager
}

func newDLFixture(t *testing.T) *dlFixture {
	t.Helper()
	f := &dlFixture{Host: modeltest.NewHost(t), t: t, dir: t.TempDir()}
	saved := Catalog
	Catalog = []Entry{{Key: "t1", Name: "Test model", File: "test-model.gguf", Repo: "test-repo", Revision: "rev1",
		Sha256: sum(f.Model), TemplateSha256: sum(f.Tpl), SizeLabel: "1 MB", MinBytes: 1 << 20, Bytes: int64(len(f.Model))}}
	t.Cleanup(func() { Catalog = saved })
	f.m = NewManager(f.dir, "dev")
	f.m.baseURL = f.Srv.URL
	t.Cleanup(f.m.Close)
	return f
}

func (f *dlFixture) wait(want string) DownloadState {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s := f.m.State(); s.Phase == want {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("download never reached %q: %+v", want, f.m.State())
	return DownloadState{}
}

func (f *dlFixture) Path(name string) string { return filepath.Join(f.dir, "models", name) }

func TestDownloadRetriesOnlyTemplate(t *testing.T) {
	for _, kind := range []string{"http", "hash", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := newDLFixture(t)
			f.FailTemplate(kind)
			if err := f.m.Start("t1"); err != nil {
				t.Fatal(err)
			}
			var resumed int64
			if kind == "cancel" {
				resumed = modeltest.WaitTemplatePartial(t, f.dir, func() string { return f.m.State().Phase })
				f.m.Cancel()
				f.wait("cancelled")
			} else {
				s := f.wait("failed")
				if (kind == "http" && !strings.Contains(s.Error, "503")) || (kind == "hash" && !strings.Contains(s.Error, "SHA-256")) {
					t.Fatalf("unexpected failure: %+v", s)
				}
				if _, err := os.Stat(f.Path("chat_template.t1.jinja.partial")); !os.IsNotExist(err) {
					t.Fatalf("failed template partial should be absent: %v", err)
				}
			}
			if got, err := os.ReadFile(f.Path("test-model.gguf")); err != nil || !bytes.Equal(got, f.Model) {
				t.Fatalf("verified GGUF was not kept: %v", err)
			}
			if TemplateInstalled(f.dir) {
				t.Fatal("failed template counts as installed")
			}
			f.RepairTemplate()
			if err := f.m.Start("t1"); err != nil {
				t.Fatal(err)
			}
			f.wait("done")
			f.CheckTemplateRecovery(t, f.dir, resumed)
		})
	}
}

func TestDownloadFreshModelAndTemplate(t *testing.T) {
	f := newDLFixture(t)
	if err := f.m.Start("t1"); err != nil {
		t.Fatal(err)
	}
	s := f.wait("done")
	if s.Done != int64(len(f.Model)) || s.Total != int64(len(f.Model)) {
		t.Errorf("progress at the end: %+v", s)
	}
	got, err := os.ReadFile(f.Path("test-model.gguf"))
	if err != nil || !bytes.Equal(got, f.Model) {
		t.Fatalf("model file: %v", err)
	}
	if tpl, err := os.ReadFile(f.Path("chat_template.t1.jinja")); err != nil || !bytes.Equal(tpl, f.Tpl) {
		t.Errorf("template: %v", err)
	}
	if left, _ := filepath.Glob(f.Path("*.partial")); len(left) != 0 {
		t.Errorf("partial files left behind: %v", left)
	}
	select {
	case ev := <-f.m.events:
		if ev.Phase != "done" || ev.Key != "t1" {
			t.Errorf("event: %+v", ev)
		}
	default:
		t.Error("the main loop is not told that the download finished")
	}
	// Everything came from the pinned revision of the catalog entry.
	for _, r := range f.Reqs() {
		if !strings.HasPrefix(r, "GET /pfnet/test-repo/resolve/rev1/") {
			t.Errorf("request outside the pinned revision: %s", r)
		}
	}
	// An installed model is refused, and so is an unknown one.
	if err := f.m.Start("t1"); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Errorf("second download of an installed model: %v", err)
	}
	if err := f.m.Start("nope"); err == nil {
		t.Error("unknown model accepted")
	}
}

func TestDownloadKeepsExistingTemplate(t *testing.T) {
	f := newDLFixture(t)
	os.MkdirAll(f.Path(""), 0o755)
	os.WriteFile(f.Path("chat_template.jinja"), f.Tpl, 0o644)
	f.m.Start("t1")
	f.wait("done")
	for _, r := range f.Reqs() {
		if strings.Contains(r, "chat_template") {
			t.Errorf("a template that is already there was fetched again: %s", r)
		}
	}
	if b, _ := os.ReadFile(f.Path("chat_template.jinja")); !bytes.Equal(b, f.Tpl) {
		t.Error("the existing template was replaced")
	}
}

func TestDownloadResumesAPartialFile(t *testing.T) {
	f := newDLFixture(t)
	os.MkdirAll(f.Path(""), 0o755)
	const have = 400 << 10
	os.WriteFile(f.Path("test-model.gguf.partial"), f.Model[:have], 0o644)
	f.m.Start("t1")
	f.wait("done")
	if got, _ := os.ReadFile(f.Path("test-model.gguf")); !bytes.Equal(got, f.Model) {
		t.Fatal("resumed file differs from the model")
	}
	if r := f.Reqs()[0]; !strings.HasSuffix(r, fmt.Sprintf("Range=bytes=%d-", have)) {
		t.Errorf("a partial file must be continued with a Range request: %s", r)
	}
}

func TestDownloadStartsOverWhenTheServerIgnoresRange(t *testing.T) {
	f := newDLFixture(t)
	f.IgnoreRange = true
	os.MkdirAll(f.Path(""), 0o755)
	os.WriteFile(f.Path("test-model.gguf.partial"), f.Model[:300<<10], 0o644)
	f.m.Start("t1")
	f.wait("done")
	if got, _ := os.ReadFile(f.Path("test-model.gguf")); !bytes.Equal(got, f.Model) {
		t.Fatal("a server that answers 200 to a Range request must not leave a doubled or mixed file")
	}
}

func TestDownloadCompletePartialGetsVerified(t *testing.T) {
	f := newDLFixture(t)
	os.MkdirAll(f.Path(""), 0o755)
	os.WriteFile(f.Path("test-model.gguf.partial"), f.Model, 0o644) // everything, never renamed
	f.m.Start("t1")
	f.wait("done") // the host answers 416; the file is checked and moved
	if got, _ := os.ReadFile(f.Path("test-model.gguf")); !bytes.Equal(got, f.Model) {
		t.Fatal("model not installed from the complete partial file")
	}
}

func TestDownloadRejectsAWrongHashAndDeletesIt(t *testing.T) {
	f := newDLFixture(t)
	wrong := append([]byte(nil), f.Model...)
	wrong[1000] ^= 0xff
	f.ServeBytes = wrong
	f.m.Start("t1")
	s := f.wait("failed")
	if !strings.Contains(s.Error, "does not match") {
		t.Errorf("error: %q", s.Error)
	}
	for _, n := range []string{"test-model.gguf", "test-model.gguf.partial"} {
		if _, err := os.Stat(f.Path(n)); err == nil {
			t.Errorf("%s must not exist after a hash mismatch", n)
		}
	}
	// The next attempt starts from nothing and succeeds with the right bytes.
	f.Mu.Lock()
	f.ServeBytes = nil
	f.Mu.Unlock()
	if err := f.m.Start("t1"); err != nil {
		t.Fatal(err)
	}
	f.wait("done")
}

func TestDownloadCorruptPartialIsDeletedNotInstalled(t *testing.T) {
	f := newDLFixture(t)
	os.MkdirAll(f.Path(""), 0o755)
	bad := append([]byte(nil), f.Model[:200<<10]...)
	bad[5] ^= 0xff
	os.WriteFile(f.Path("test-model.gguf.partial"), bad, 0o644)
	f.m.Start("t1")
	s := f.wait("failed")
	if !strings.Contains(s.Error, "does not match") {
		t.Errorf("error: %q", s.Error)
	}
	if _, err := os.Stat(f.Path("test-model.gguf")); err == nil {
		t.Error("a model built on a corrupt partial file was installed")
	}
}

func TestDownloadCancelKeepsThePartialAndResumes(t *testing.T) {
	f := newDLFixture(t)
	f.Gate = make(chan struct{})
	f.m.Start("t1")
	// Wait until the first half arrived.
	deadline := time.Now().Add(10 * time.Second)
	for f.m.State().Done < int64(len(f.Model)/2) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := f.m.Start("t1"); err == nil || !strings.Contains(err.Error(), "another model") {
		t.Errorf("one download at a time: %v", err)
	}
	if !f.m.Downloading("t1") || f.m.Downloading("other") {
		t.Error("Downloading is wrong")
	}
	f.m.Cancel()
	f.wait("cancelled")
	info, err := os.Stat(f.Path("test-model.gguf.partial"))
	if err != nil || info.Size() < int64(len(f.Model)/2) {
		t.Fatalf("the partial file must stay after a cancel: %v", err)
	}
	if _, err := os.Stat(f.Path("test-model.gguf")); err == nil {
		t.Error("a cancelled download produced a model")
	}
	// Continue: only the rest is requested.
	f.Mu.Lock()
	f.Gate = nil
	f.Mu.Unlock()
	f.m.Start("t1")
	f.wait("done")
	if got, _ := os.ReadFile(f.Path("test-model.gguf")); !bytes.Equal(got, f.Model) {
		t.Fatal("the continued file differs from the model")
	}
	rs := f.Reqs()
	if last := rs[len(rs)-2]; !strings.Contains(last, "Range=bytes=") || strings.HasSuffix(last, "Range=") {
		t.Errorf("the second try must continue with a Range request: %v", rs)
	}
}

func TestDownloadHTTPErrorAndUnwritableFolder(t *testing.T) {
	f := newDLFixture(t)
	f.Status = 404
	f.m.Start("t1")
	if s := f.wait("failed"); !strings.Contains(s.Error, "404") {
		t.Errorf("error: %q", s.Error)
	}

	g := newDLFixture(t)
	os.WriteFile(filepath.Join(g.dir, "models"), []byte("a file, not a folder"), 0o644)
	g.m.Start("t1")
	g.wait("failed")
}

func TestDownloadFollowsARedirectToAnotherHostButNotDownToHTTP(t *testing.T) {
	f := newDLFixture(t)
	// A "CDN" on another port, reached by a redirect.
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "m", time.Time{}, bytes.NewReader(f.Model))
	}))
	defer cdn.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "chat_template.jinja") {
			http.ServeContent(w, r, "t", time.Time{}, bytes.NewReader(f.Tpl))
			return
		}
		http.Redirect(w, r, cdn.URL+"/blob", http.StatusFound)
	}))
	defer front.Close()
	f.m.baseURL = front.URL
	f.m.Start("t1")
	f.wait("done")

	// https -> http is refused.
	h := newDLFixture(t)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(h.Model)
	}))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/blob", http.StatusFound)
	}))
	defer tls.Close()
	h.m.baseURL = tls.URL
	h.m.client.Transport = tls.Client().Transport
	h.m.Start("t1")
	s := h.wait("failed")
	if !strings.Contains(s.Error, "https to http") {
		t.Errorf("error: %q", s.Error)
	}
	if _, err := os.Stat(h.Path("test-model.gguf")); err == nil {
		t.Error("a model arrived over a downgraded connection")
	}
}

// The default host must be unreachable from tests: testsupport.LoopbackOnly turns any attempt into an error.
func TestDownloadsCannotReachTheRealHostInTests(t *testing.T) {
	m := NewManager(t.TempDir(), "dev")
	t.Cleanup(m.Close)
	if m.baseURL != modelHost {
		t.Fatalf("default host: %s", m.baseURL)
	}
	if err := m.Start("2b"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.State().Running() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s := m.State(); s.Phase != "failed" || !strings.Contains(s.Error, "a test tried to reach huggingface.co") {
		t.Fatalf("the guard did not stop the request: %+v", s)
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
		{"", 0, 0, 0, false},
		{"bytes x-9/10", 0, 0, 0, false},
		{"bytes 1-2", 0, 0, 0, false},
	}
	for _, c := range cases {
		s, e, n, ok := parseContentRange(c.in)
		if ok != c.ok || (ok && (s != c.start || e != c.end || n != c.total)) {
			t.Errorf("%q: %d %d %d %v", c.in, s, e, n, ok)
		}
	}
}

// The catalog is pinned in four places. The Python test checks the two Unix
// scripts against setup.ps1; this one checks the Go copy against setup.ps1.
func TestCatalogMatchesSetupScript(t *testing.T) {
	ps1, err := os.ReadFile(filepath.Join("..", "..", "setup.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ps1)
	get := func(entry, field string) string {
		m := regexp.MustCompile(`\b` + field + ` = (?:'([^']*)'|([0-9A-Za-z]+))`).FindStringSubmatch(entry)
		if m == nil {
			t.Fatalf("setup.ps1 entry lacks %s:\n%s", field, entry)
		}
		return m[1] + m[2]
	}
	if len(Catalog) != 3 {
		t.Fatalf("catalog has %d entries", len(Catalog))
	}
	for _, e := range Catalog {
		start := strings.Index(text, "'"+e.Key+"' = @{")
		if start < 0 {
			t.Fatalf("setup.ps1 has no entry for %s", e.Key)
		}
		end := strings.Index(text[start:], "}")
		entry := text[start : start+end]
		for field, want := range map[string]string{"Repo": e.Repo, "File": e.File, "Revision": e.Revision, "Sha256": e.Sha256,
			"TemplateSha256": e.TemplateSha256, "Size": e.SizeLabel} {
			if got := get(entry, field); got != want {
				t.Errorf("%s %s: setup.ps1 has %q, models.go has %q", e.Key, field, got, want)
			}
		}
		if mb := get(entry, "MinBytes"); mb != minBytesLiteral(e.MinBytes) {
			t.Errorf("%s MinBytes: setup.ps1 %q, models.go %d", e.Key, mb, e.MinBytes)
		}
	}
}

func minBytesLiteral(n int64) string {
	if n%(1<<30) == 0 {
		return fmt.Sprintf("%dGB", n>>30)
	}
	return fmt.Sprintf("%dMB", n>>20)
}
