package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/testsupport"
)

func TestTranslateAPIAuthenticationAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, token, method, path, body, origin string
		status                                  int
	}{
		{"missing token", "", "POST", "/translate", `{"text":"Hello"}`, "", 401},
		{"wrong token", "wrong", "POST", "/translate", `{"text":"Hello"}`, "", 401},
		{"happy path", "secret", "POST", "/translate", `{"text":"Hello","profile":"0"}`, "", 200},
		{"bad JSON", "secret", "POST", "/translate", `{`, "", 400},
		{"trailing JSON", "secret", "POST", "/translate", `{"text":"Hello"} {}`, "", 400},
		{"unknown key", "secret", "POST", "/translate", `{"text":"Hello","url":"https://example.com"}`, "", 400},
		{"wrong method", "secret", "GET", "/translate", "", "", 405},
		{"no settings access", "secret", "POST", "/api/state", `{}`, "", 404},
		{"web origin", "secret", "POST", "/translate", `{"text":"Hello"}`, "https://example.com", 403},
		{"extension origin", "secret", "POST", "/translate", `{"text":"Hello"}`, "chrome-extension://abcdefghijklmnopabcdefghijklmnop", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := translateAPIHandler("secret", func(req settings.Request) settings.Reply {
				calls++
				if req.Action != "translate" || req.Text != "Hello" {
					t.Fatalf("request=%+v", req)
				}
				return settings.Reply{Translation: "こんにちは"}
			})
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set(translateTokenHeader, tc.token)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 200 {
				var body map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["translation"] != "こんにちは" {
					t.Fatalf("body=%s", w.Body.String())
				}
			} else if calls != 0 {
				t.Fatal("invalid request reached engine")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("unexpected CORS grant")
			}
		})
	}
}

// Exercise the real app coordinator with the same fake llama-server process
// used by hotkey, reload and engine-switch tests. HTTP input is in memory.
func TestTranslateAPIWithFakeEngine(t *testing.T) {
	a, _, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.conf.TranslateAPIEnabled = true
	cb := &testClipboard{text: "unrelated clipboard"}
	a.cb = cb
	h := translateAPIHandler("secret", func(req settings.Request) settings.Reply {
		a.handleSettingsRequest(req)
		if a.translating {
			select {
			case res := <-a.results:
				a.handleResult(res)
			case <-time.After(5 * time.Second):
				t.Fatal("translation timed out")
			}
		}
		return <-req.Reply
	})
	for _, tc := range []struct {
		text, profile string
		status        int
	}{
		{"Hello", "", 200}, {"こんにちは", "0", 200}, {"Hello", a.conf.Profiles[0].Hotkey, 200}, {"Hello", "missing", 422}, {"", "", 422},
	} {
		b, _ := json.Marshal(map[string]string{"text": tc.text, "profile": tc.profile})
		req := httptest.NewRequest("POST", "/translate", strings.NewReader(string(b)))
		req.Header.Set(translateTokenHeader, "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if tc.status == 200 && !strings.Contains(w.Body.String(), testsupport.FakeTranslation) {
			t.Fatalf("body=%s", w.Body.String())
		}
		if cb.text != "unrelated clipboard" {
			t.Fatal("HTTP request changed app clipboard")
		}
	}
	a.conf.Profiles[0].Output = config.OutputBilingual
	req := settings.NewActionRequest("translate", "", time.Second)
	req.Text = "Hello"
	a.handleSettingsRequest(req)
	a.handleResult(<-a.results)
	rep := <-req.Reply
	if rep.Err != nil || !strings.Contains(rep.Translation, "Hello") || !strings.Contains(rep.Translation, testsupport.FakeTranslation) {
		t.Fatalf("reply=%+v", rep)
	}
}

func TestTranslateAPIBusyDisabledAndCancel(t *testing.T) {
	a := apiUnitApp(t)
	req := settings.NewActionRequest("translate", "", time.Second)
	req.Text = "Hello"
	a.handleSettingsRequest(req)
	if rep := <-req.Reply; rep.Err == nil {
		t.Fatal("disabled API accepted request")
	}
	a.conf.TranslateAPIEnabled = true
	a.translating = true
	req = settings.NewActionRequest("translate", "", time.Second)
	req.Text = "Hello"
	a.handleSettingsRequest(req)
	if rep := <-req.Reply; rep.Err != settings.ErrBusy {
		t.Fatalf("reply=%+v", rep)
	}
	a.apiReply = make(chan settings.Reply, 1)
	reply := a.apiReply
	a.abortTranslation()
	if rep := <-reply; rep.Err == nil {
		t.Fatal("cancel did not answer HTTP caller")
	}
}

func TestManualClipboardActionSkipsCaptureAndPaste(t *testing.T) {
	a, _, cb, pastes, _ := uxApp(t)
	a.endpoint = translateStub(t, "こんにちは").URL
	a.conf.AutoPaste = true
	cb.text = "Hello"
	a.capture = nil
	a.beginCapture = nil
	a.handleCommand(cmdTranslateClipboard)
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(5 * time.Second):
		t.Fatal("translation timed out")
	}
	if cb.text != "こんにちは" || *pastes != 0 {
		t.Fatalf("clipboard=%q pastes=%d", cb.text, *pastes)
	}
}

// This transport fixture exercises real prompt construction and result handling
// without opening sockets, making coordinator checks runnable in restricted CI.
type apiEngineTransport struct{ targets chan string }

func (f apiEngineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "127.0.0.1:18119" || req.URL.Path != "/v1/chat/completions" {
		return nil, fmt.Errorf("unexpected engine destination %s", req.URL)
	}
	var body struct {
		Kwargs map[string]string `json:"chat_template_kwargs"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	f.targets <- body.Kwargs["target_lang"]
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"translated text"},"finish_reason":"stop"}]}`))}, nil
}
func apiUnitApp(t *testing.T) *app {
	t.Helper()
	a := newApp(t.TempDir(), config.Default(), config.Paths{}, &fakeTray{}, io.Discard)
	a.cb = &testClipboard{text: "untouched clipboard"}
	t.Cleanup(a.dl.Close)
	return a
}
func installAPIEngineTransport(t *testing.T) chan string {
	t.Helper()
	targets := make(chan string, 8)
	old := http.DefaultTransport
	http.DefaultTransport = apiEngineTransport{targets}
	t.Cleanup(func() { http.DefaultTransport = old })
	return targets
}
func TestTranslateAPICoordinatorInMemory(t *testing.T) {
	targets := installAPIEngineTransport(t)
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	for _, tc := range []struct{ text, target, profile string }{{"Hello", "ja", ""}, {"こんにちは", "en", "0"}, {"Hello", "ja", "Ctrl+Alt+E"}} {
		req := settings.NewActionRequest("translate", "", time.Second)
		req.Text = tc.text
		req.Profile = tc.profile
		a.handleSettingsRequest(req)
		if !a.translating {
			t.Fatalf("did not start: %+v", <-req.Reply)
		}
		select {
		case res := <-a.results:
			a.handleResult(res)
		case <-time.After(time.Second):
			t.Fatal("engine timed out")
		}
		rep := <-req.Reply
		if rep.Err != nil || rep.Translation != "translated text" {
			t.Fatalf("reply=%+v", rep)
		}
		if target := <-targets; target != tc.target {
			t.Fatalf("target=%q want=%q", target, tc.target)
		}
		if text, _ := a.cb.Read(); text != "untouched clipboard" {
			t.Fatal("HTTP translation touched clipboard")
		}
	}
}
func TestManualClipboardInMemory(t *testing.T) {
	targets := installAPIEngineTransport(t)
	a := apiUnitApp(t)
	cb := a.cb.(*testClipboard)
	cb.text = "Hello"
	a.conf.AutoPaste = true
	a.foreground = func() uintptr { return 1 }
	a.capture = nil
	a.beginCapture = nil
	a.paste = func(uintptr) error { t.Fatal("manual action auto-pasted"); return nil }
	a.handleCommand(cmdTranslateClipboard)
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(time.Second):
		t.Fatal("engine timed out")
	}
	if cb.text != "translated text" || <-targets != "ja" {
		t.Fatalf("clipboard=%q", cb.text)
	}
}
func TestTranslateAPIDisabledTokenPersists(t *testing.T) {
	a := apiUnitApp(t)
	if err := a.startTranslateAPI(); err != nil {
		t.Fatal(err)
	}
	if a.apiServer != nil {
		t.Fatal("disabled endpoint started listening")
	}
	token := a.conf.TranslateAPIToken
	if len(token) != 64 {
		t.Fatalf("token length=%d", len(token))
	}
	conf, err := config.Load(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if conf.TranslateAPIToken != token || conf.TranslateAPIEnabled {
		t.Fatal("token not persisted with endpoint off")
	}
	b := newApp(a.dir, conf, config.Paths{}, &fakeTray{}, io.Discard)
	t.Cleanup(b.dl.Close)
	if err := b.startTranslateAPI(); err != nil {
		t.Fatal(err)
	}
	if b.conf.TranslateAPIToken != token || b.apiServer != nil {
		t.Fatal("restart changed token or opened listener")
	}
}
