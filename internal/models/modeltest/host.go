// Package modeltest is a fake model host (the Hugging Face URL layout) for
// tests of the download manager and of the code that drives it. It does not
// import package models, so tests inside models can use it too.
package modeltest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// Sum is the lowercase hex SHA-256 of b, as the catalog stores it.
func Sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Host serves one small "model" (key t1, repo test-repo, revision rev1) and a
// chat template. The exported fields are switches a test may flip, under Mu.
type Host struct {
	Model []byte
	Tpl   []byte
	Srv   *httptest.Server

	Mu          sync.Mutex
	Requests    []string      // "GET <path> Range=<header>"
	IgnoreRange bool          // answer 200 with the whole body even for a Range request
	ServeBytes  []byte        // serve these instead of Model (wrong content)
	Gate        chan struct{} // when set, the model body is sent in two halves and waits on Gate between them
	Status      int           // when set, answer this status for the model
	TplStatus   int
	TplBytes    []byte
	TplGate     chan struct{}
}

// NewHost starts the host; it is closed with the test.
func NewHost(t *testing.T) *Host {
	t.Helper()
	h := &Host{}
	h.Model = bytes.Repeat([]byte("0123456789abcdef"), 64<<10) // 1 MiB
	h.Tpl = bytes.Repeat([]byte("{# template #}"), 20)

	mux := http.NewServeMux()
	mux.HandleFunc("/pfnet/test-repo/resolve/rev1/", func(w http.ResponseWriter, r *http.Request) {
		h.Mu.Lock()
		h.Requests = append(h.Requests, "GET "+r.URL.Path+" Range="+r.Header.Get("Range"))
		ignore, body, gate, status := h.IgnoreRange, h.Model, h.Gate, h.Status
		if h.ServeBytes != nil {
			body = h.ServeBytes
		}
		if strings.HasSuffix(r.URL.Path, "chat_template.jinja") {
			ignore, body, gate, status = false, h.Tpl, h.TplGate, h.TplStatus
			if h.TplBytes != nil {
				body = h.TplBytes
			}
		}
		h.Mu.Unlock()
		if status != 0 {
			http.Error(w, "nope", status)
			return
		}
		switch {
		case gate != nil:
			start := 0
			if rg := r.Header.Get("Range"); rg != "" {
				fmt.Sscanf(rg, "bytes=%d-", &start)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
				w.Header().Set("Content-Length", fmt.Sprint(len(body)-start))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			}
			half := start + (len(body)-start)/2
			w.Write(body[start:half])
			w.(http.Flusher).Flush()
			select {
			case <-gate:
				w.Write(body[half:])
			case <-r.Context().Done():
			}
		case ignore:
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Write(body)
		default:
			http.ServeContent(w, r, "m", time.Time{}, bytes.NewReader(body))
		}
	})
	h.Srv = httptest.NewServer(mux)
	t.Cleanup(h.Srv.Close)
	return h
}

// Reqs is a copy of the requests seen so far.
func (h *Host) Reqs() []string {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	return append([]string(nil), h.Requests...)
}

// FailTemplate makes the template download fail: "http" answers 503, "hash"
// serves wrong content, "cancel" stalls half way until TplGate is released.
func (h *Host) FailTemplate(kind string) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	switch kind {
	case "http":
		h.TplStatus = http.StatusServiceUnavailable
	case "hash":
		h.TplBytes = bytes.Repeat([]byte("wrong template"), 20)
	case "cancel":
		h.TplGate = make(chan struct{})
	}
}

// RepairTemplate undoes FailTemplate.
func (h *Host) RepairTemplate() {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	h.TplStatus, h.TplBytes, h.TplGate = 0, nil, nil
}

// WaitFor polls cond for up to 20 seconds.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// WaitTemplatePartial waits until the template of model t1 is half downloaded
// (phase() == "template" and a partial file exists) and returns its size.
func WaitTemplatePartial(t *testing.T, dir string, phase func() string) int64 {
	t.Helper()
	var size int64
	WaitFor(t, "the template partial file", func() bool {
		info, err := os.Stat(filepath.Join(dir, "models", "chat_template.t1.jinja.partial"))
		if err == nil {
			size = info.Size()
		}
		return phase() == "template" && size > 0
	})
	return size
}

// CheckTemplateRecovery checks the end state of "model fine, template failed,
// template retried": both files complete, no partials, one model request, two
// template requests, the last one resuming at resumed bytes.
func (h *Host) CheckTemplateRecovery(t *testing.T, dir string, resumed int64) {
	t.Helper()
	for name, want := range map[string][]byte{"test-model.gguf": h.Model, "chat_template.t1.jinja": h.Tpl} {
		got, err := os.ReadFile(filepath.Join(dir, "models", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: content differs or read failed: %v", name, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "models", "*.partial")); len(left) != 0 {
		t.Errorf("partial files left: %v", left)
	}
	var models, templates int
	reqs := h.Reqs()
	for _, r := range reqs {
		if strings.Contains(r, "test-model.gguf") {
			models++
		} else if strings.Contains(r, "chat_template.jinja") {
			templates++
		}
	}
	if models != 1 || templates != 2 {
		t.Fatalf("want one model request and two template requests: %v", reqs)
	}
	wantRange := "Range="
	if resumed > 0 {
		wantRange += fmt.Sprintf("bytes=%d-", resumed)
	}
	if !strings.HasSuffix(reqs[len(reqs)-1], wantRange) {
		t.Errorf("template retry range: %v, want %s", reqs, wantRange)
	}
}
