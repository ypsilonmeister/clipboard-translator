package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
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
		{"Hello", "", 200}, {"こんにちは", "0", 200}, {"Hello", a.conf.Profiles[0].Hotkey, 200}, {"Hello", "missing", 400}, {"", "", 422},
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
func TestTranslateAPILazyToken(t *testing.T) {
	a := apiUnitApp(t)
	// A non-directory config path fails writes even when tests run as root.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("read only config"), 0400); err != nil {
		t.Fatal(err)
	}
	a.dir = file
	if err := a.startTranslateAPI(); err != nil {
		t.Fatal(err)
	}
	if a.conf.TranslateAPIToken != "" || a.apiServer != nil {
		t.Fatal("disabled startup changed token or listener")
	}
	a.conf.TranslateAPIEnabled = true
	old := translateAPISupported
	translateAPISupported = func() bool { return true }
	t.Cleanup(func() { translateAPISupported = old })
	a.conf.TranslateAPIEnabled = false
	req := settings.NewActionRequest("translate_api", "true", time.Second)
	a.configureTranslateAPI(req)
	if rep := <-req.Reply; rep.Err == nil || a.conf.TranslateAPIEnabled || a.apiServer != nil {
		t.Fatal("failed persistence enabled endpoint")
	}
	a.dir = t.TempDir()
	info := settings.NewActionRequest("translate_api_info", "", time.Second)
	a.handleSettingsRequest(info)
	if rep := <-info.Reply; rep.Err != nil {
		t.Fatal(rep.Err)
	}
	token := a.conf.TranslateAPIToken
	conf, err := config.Load(a.dir)
	if err != nil || len(token) != 64 || conf.TranslateAPIToken != token {
		t.Fatalf("token persistence: %v", err)
	}
	if err := a.startTranslateAPI(); err != nil {
		t.Fatal(err)
	}
	if a.conf.TranslateAPIToken != token || a.apiServer != nil {
		t.Fatal("restart changed token or listener")
	}
}

func TestTranslateAPINonLinux(t *testing.T) {
	old := translateAPISupported
	translateAPISupported = func() bool { return false }
	t.Cleanup(func() { translateAPISupported = old })
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	if err := a.startTranslateAPI(); err != nil {
		t.Fatal(err)
	}
	if a.apiServer != nil || a.conf.TranslateAPIToken != "" {
		t.Fatal("unsupported startup opened endpoint or generated token")
	}
	req := settings.NewActionRequest("translate_api", "true", time.Second)
	a.configureTranslateAPI(req)
	if rep := <-req.Reply; rep.Err == nil {
		t.Fatal("unsupported enable accepted")
	}
	if st := a.settingsState(); st.TranslateAPIAvailable || st.TranslateAPIEnabled {
		t.Fatal("settings offered unsupported endpoint")
	}
}

func TestTranslateAPIStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{errAPIInput, 422}, {errAPIRequest, 400}, {settings.ErrBusy, 503},
		{errors.New("engine crashed"), 502}, {fmt.Errorf("engine: %w", context.DeadlineExceeded), 504},
	} {
		h := translateAPIHandler("secret", func(settings.Request) settings.Reply { return settings.Reply{Err: tc.err} })
		req := httptest.NewRequest("POST", "/translate", strings.NewReader(`{"text":"Hello"}`))
		req.Header.Set(translateTokenHeader, "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%v: status %d", tc.err, w.Code)
		}
	}
}

func TestTranslateAPIDisableAborts(t *testing.T) {
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	a.translating = true
	shutdown := make(chan struct{})
	a.apiServer = &http.Server{}
	a.apiServer.RegisterOnShutdown(func() { close(shutdown) })
	canceled := false
	a.cancelCurrent = func() { canceled = true }
	a.apiReply = make(chan settings.Reply, 1)
	reply := a.apiReply
	req := settings.NewActionRequest("translate_api", "false", time.Second)
	a.configureTranslateAPI(req)
	if rep := <-req.Reply; rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rep := <-reply; rep.Err == nil || !canceled || a.translating || a.apiReply != nil {
		t.Fatal("disable failed to abort")
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("server did not shut down gracefully")
	}
}

type apiContextKey struct{}

func TestTranslateAPIDisconnect(t *testing.T) {
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), apiContextKey{}, "caller"))
	h := translateAPIHandler("secret", func(req settings.Request) settings.Reply {
		if req.Context == nil || req.Context.Value(apiContextKey{}) != "caller" {
			t.Fatal("HTTP context lost")
		}
		a.translating = true
		a.apiReply = req.Reply
		a.apiContext = req.Context
		canceled := false
		a.cancelCurrent = func() { canceled = true }
		cancel()
		a.tickAPIContext()
		if a.translating || a.apiReply != nil || !canceled {
			t.Fatal("disconnect retained busy state")
		}
		return <-req.Reply
	})
	req := httptest.NewRequest("POST", "/translate", strings.NewReader(`{"text":"Hello"}`)).WithContext(ctx)
	req.Header.Set(translateTokenHeader, "secret")
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestTranslateAPISkipLength(t *testing.T) {
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	a.conf.MaxChars = 3
	req := settings.NewActionRequest("translate", "", time.Second)
	req.Text = "Hello"
	a.handleAPITranslation(req)
	rep := <-req.Reply
	if !errors.Is(rep.Err, errAPIInput) || !strings.Contains(rep.Err.Error(), "5 characters") || !strings.Contains(rep.Err.Error(), "3-character limit") {
		t.Fatalf("reply=%+v", rep)
	}
}

// The read stays pending until a desktop owner focuses the control window.
type manualFocusClipboard struct {
	*asyncDesktopClipboard
	presented int
}

func (c *manualFocusClipboard) PresentControl() { c.presented++ }

func TestManualClipboardFocusAndTimeout(t *testing.T) {
	a := apiUnitApp(t)
	c := &manualFocusClipboard{asyncDesktopClipboard: &asyncDesktopClipboard{testClipboard: &testClipboard{}, readPending: true, state: platform.DesktopStatus{Wayland: true, Manual: true}}}
	a.cb = c
	a.handleCommand(cmdTranslateClipboard)
	if c.presented != 1 || a.pendingRead == nil {
		t.Fatal("manual read did not present control window")
	}
	a.pendingRead.deadline = time.Time{}
	a.tickClipboard()
	tray := a.tray.(*fakeTray)
	if len(tray.notes) == 0 || !strings.Contains(tray.notes[len(tray.notes)-1], "focus the app control window") {
		t.Fatalf("notifications=%+v", tray.notes)
	}
}

func TestTranslateAPIRequestContextCancelsEngine(t *testing.T) {
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	started := make(chan struct{})
	stopped := make(chan struct{})
	old := http.DefaultTransport
	http.DefaultTransport = apiCancelTransport{started: started, stopped: stopped}
	t.Cleanup(func() { http.DefaultTransport = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := settings.NewActionRequest("translate", "", time.Second)
	req.Text, req.Context = "Hello", ctx
	a.handleSettingsRequest(req)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("engine did not receive cancellation")
	}
	a.tickAPIContext()
	if a.busy() {
		t.Fatal("disconnected request left app busy")
	}
	if rep := <-req.Reply; !errors.Is(rep.Err, context.Canceled) {
		t.Fatalf("reply=%+v", rep)
	}
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(time.Second):
		t.Fatal("engine did not return")
	}
}

type apiCancelTransport struct{ started, stopped chan struct{} }

func (f apiCancelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	close(f.started)
	<-req.Context().Done()
	close(f.stopped)
	return nil, req.Context().Err()
}

func TestTranslateAPIDisableDuringReload(t *testing.T) {
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	a.translating = true
	canceled := false
	a.cancelCurrent = func() { canceled = true }
	a.apiReply = make(chan settings.Reply, 1)
	reply := a.apiReply
	req := settings.NewActionRequest("translate_api", "false", time.Second)
	a.settingsReqs <- req
	a.serviceSettingsWhileBusy("loading model")
	if rep := <-req.Reply; rep.Err != nil {
		t.Fatal(rep.Err)
	}
	if rep := <-reply; !errors.Is(rep.Err, context.Canceled) || !canceled || a.translating || a.conf.TranslateAPIEnabled {
		t.Fatal("disable during reload did not abort")
	}
}

func TestReReviewAPINeverPresentsManualControl(t *testing.T) {
	installAPIEngineTransport(t)
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	c := &manualFocusClipboard{asyncDesktopClipboard: &asyncDesktopClipboard{testClipboard: &testClipboard{}, state: platform.DesktopStatus{Wayland: true, Manual: true}}}
	a.cb = c
	req := settings.NewActionRequest("translate", "", time.Second)
	req.Text = "Hello"
	a.handleAPITranslation(req)
	if c.presented != 0 {
		t.Fatal("API stole focus")
	}
	select {
	case res := <-a.results:
		a.handleResult(res)
	case <-time.After(time.Second):
		t.Fatal("engine timed out")
	}
}

func TestReReviewStateDoesNotCreateToken(t *testing.T) {
	a := apiUnitApp(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0400); err != nil {
		t.Fatal(err)
	}
	a.dir = file
	req := settings.NewRequest(nil, time.Second)
	a.handleSettingsRequest(req)
	rep := <-req.Reply
	if rep.Err != nil || rep.State.TranslateAPIToken != "" || a.conf.TranslateAPIToken != "" {
		t.Fatalf("state created token or failed: %+v", rep)
	}
	a.conf.TranslateAPIToken = "existing"
	req = settings.NewRequest(nil, time.Second)
	a.handleSettingsRequest(req)
	if rep = <-req.Reply; rep.Err != nil || rep.State.TranslateAPIToken != "existing" {
		t.Fatal(rep)
	}
}

func TestReReviewCancellationStatus(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		h := translateAPIHandler("secret", func(settings.Request) settings.Reply {
			if deadline {
				return settings.Reply{Err: context.DeadlineExceeded}
			}
			return settings.Reply{Err: fmt.Errorf("wrapped: %w", context.Canceled)}
		})
		req := httptest.NewRequest("POST", "/translate", strings.NewReader(`{"text":"Hello"}`))
		if deadline {
			ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(-time.Second))
			defer cancel()
			req = req.WithContext(ctx)
		}
		req.Header.Set(translateTokenHeader, "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "request cancelled") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestReReviewRequestCancellationKeepsModelLoadAlive(t *testing.T) {
	a := apiUnitApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.apiContext = ctx
	a.apiReply = make(chan settings.Reply, 1)
	a.translating = true
	reply := a.apiReply
	if a.sup.UI("loading", 0) {
		t.Fatal("request cancellation became app quit")
	}
	if a.translating || !errors.Is((<-reply).Err, context.Canceled) {
		t.Fatal("request not cancelled")
	}
	a.sigQuit = true
	if !a.sup.UI("loading", 0) {
		t.Fatal("app quit no longer stops load")
	}
}

type noValueClipboard struct{ *asyncDesktopClipboard }

func (c *noValueClipboard) Read() (string, error) { return "", platform.ErrClipboardNoValue }
func TestReReviewManualNoValue(t *testing.T) {
	a := apiUnitApp(t)
	a.cb = &noValueClipboard{&asyncDesktopClipboard{testClipboard: &testClipboard{}, state: platform.DesktopStatus{Wayland: true, Manual: true}}}
	a.startClipboardTranslation()
	notes := a.tray.(*fakeTray).notes
	if len(notes) == 0 || !strings.Contains(notes[len(notes)-1], "focus the app control window") {
		t.Fatal(notes)
	}
}

func TestReReviewEnabledStateDoesNotCreateToken(t *testing.T) {
	a := apiUnitApp(t)
	a.conf.TranslateAPIEnabled = true
	a.dir = filepath.Join(t.TempDir(), "missing", "config")
	req := settings.NewRequest(nil, time.Second)
	a.handleSettingsRequest(req)
	rep := <-req.Reply
	if rep.Err != nil || rep.State.TranslateAPIToken != "" || a.conf.TranslateAPIToken != "" {
		t.Fatal(rep)
	}
	if _, err := os.Stat(a.dir); !os.IsNotExist(err) {
		t.Fatal("state wrote config", err)
	}
}

func TestReReviewManualEmptyClipboard(t *testing.T) {
	a := apiUnitApp(t)
	a.cb = &asyncDesktopClipboard{testClipboard: &testClipboard{}, state: platform.DesktopStatus{Wayland: true, Manual: true}}
	a.startClipboardTranslation()
	if a.translating || a.pendingRead != nil {
		t.Fatal("empty clipboard started translation")
	}
	if got := a.tray.(*fakeTray).status; got != config.SkipStatus(config.SkipEmpty, a.conf.Profiles[0].Label()) {
		t.Fatalf("empty clipboard status=%q", got)
	}
	for _, note := range a.tray.(*fakeTray).notes {
		if strings.Contains(note, "focus the app control window") {
			t.Fatal("empty clipboard treated as missing value")
		}
	}
}
