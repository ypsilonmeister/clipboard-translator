package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The settings page is a small web page served by the app on 127.0.0.1 for as
// long as it is open. Everything it can change goes through the same validation
// as config.json (Config.resolveProfiles) and is applied on the UI thread: the
// handlers below run on other goroutines, so they never read or touch the
// app's state; they send a request to the main loop and wait for the answer.

//go:embed settings_web
var settingsWeb embed.FS

const (
	settingsTokenHeader = "X-Settings-Token"
	settingsMaxBody     = 64 << 10
)

// profileForm is one hotkey as the page edits it: the modifiers as check boxes
// and the key as a letter, so the page does not need to know the hotkey syntax.
type profileForm struct {
	Ctrl      bool     `json:"ctrl"`
	Alt       bool     `json:"alt"`
	Shift     bool     `json:"shift"`
	Win       bool     `json:"win"`
	Key       string   `json:"key"`
	Languages []string `json:"languages,omitempty"`
	Target    string   `json:"target,omitempty"`
}

// settingsForm is everything the page edits.
type settingsForm struct {
	Profiles  []profileForm `json:"profiles"`
	AutoPaste bool          `json:"auto_paste"`
}

type languageOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// settingsState is what the page shows.
type settingsState struct {
	settingsForm
	Languages  []languageOption `json:"language_options"`
	MaxHotkeys int              `json:"max_hotkeys"`
	WinKey     string           `json:"win_key"`  // what the Win modifier is called here
	Inactive   []string         `json:"inactive"` // hotkeys that could not be registered, with the reason
}

// settingsRequest goes from a handler to the main loop. save is nil for "give me
// the state". The loop drops it once deadline has passed, so a save the page was
// told had failed is never applied later.
type settingsRequest struct {
	save     *settingsForm
	deadline time.Time
	reply    chan settingsReply // buffered: the loop never blocks on a reader that gave up
}

type settingsReply struct {
	state    settingsState
	warnings []string
	err      error
}

var errSettingsBusy = errors.New("The app is busy (loading the model or restarting the server). Try again in a moment.")

func newSettingsRequest(save *settingsForm, wait time.Duration) settingsRequest {
	return settingsRequest{save: save, deadline: time.Now().Add(wait), reply: make(chan settingsReply, 1)}
}

type settingsServer struct {
	token string
	host  string // exactly what the browser sends as Host
	ln    net.Listener
	srv   *http.Server
	ask   func(settingsRequest) settingsReply
	idle  time.Duration

	mu    sync.Mutex
	timer *time.Timer
	done  chan struct{}
	once  sync.Once
}

// startSettingsServer listens on a free loopback port. The server closes itself
// after idle without a request (the page pings while it is open).
func startSettingsServer(ask func(settingsRequest) settingsReply, idle time.Duration) (*settingsServer, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &settingsServer{token: hex.EncodeToString(raw), host: ln.Addr().String(), ln: ln, ask: ask, idle: idle, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.guard(false, s.serveIndex))
	mux.HandleFunc("/app.js", s.guard(false, s.serveAsset("settings_web/app.js", "text/javascript; charset=utf-8")))
	mux.HandleFunc("/app.css", s.guard(false, s.serveAsset("settings_web/app.css", "text/css; charset=utf-8")))
	mux.HandleFunc("/api/state", s.guard(false, s.handleState))
	mux.HandleFunc("/api/save", s.guard(true, s.handleSave))
	mux.HandleFunc("/api/ping", s.guard(true, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("/api/bye", s.guard(true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go s.Close()
	}))
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
	s.timer = time.AfterFunc(idle, s.Close)
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// URL is the address to open in the browser; the token is part of it.
func (s *settingsServer) URL() string { return "http://" + s.host + "/?t=" + s.token }

// Running reports whether the server still accepts requests (it may have closed
// itself after the idle time).
func (s *settingsServer) Running() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *settingsServer) Close() {
	s.once.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.timer.Stop()
		s.mu.Unlock()
		_ = s.srv.Close()
	})
}

func (s *settingsServer) touch() {
	s.mu.Lock()
	s.timer.Reset(s.idle)
	s.mu.Unlock()
}

// guard checks what keeps other web pages, other programs and DNS rebinding out:
// the Host must be our own address, the token must match (constant time), and a
// request that changes something must be JSON from our own origin.
func (s *settingsServer) guard(write bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("Cache-Control", "no-store")
		hdr.Set("Referrer-Policy", "no-referrer")
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.Host != s.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		got := r.Header.Get(settingsTokenHeader)
		if got == "" {
			got = r.URL.Query().Get("t")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if write {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.host {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, settingsMaxBody)
		} else if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.touch()
		h(w, r)
	}
}

func (s *settingsServer) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := settingsWeb.ReadFile("settings_web/index.html")
	if err != nil {
		http.Error(w, "missing page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(strings.ReplaceAll(string(b), "{{TOKEN}}", s.token)))
}

func (s *settingsServer) serveAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := settingsWeb.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(b)
	}
}

type apiResponse struct {
	State    *settingsState `json:"state,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// roundTrip sends one request to the main loop and waits for its answer.
func (s *settingsServer) roundTrip(req settingsRequest) (settingsReply, bool) {
	rep := s.ask(req)
	return rep, rep.err == nil
}

// stateOf is the state to send back with an error, or nil when the loop never
// answered (busy): an empty state would blank the page.
func stateOf(rep settingsReply) *settingsState {
	if len(rep.state.Profiles) == 0 {
		return nil
	}
	return &rep.state
}

func (s *settingsServer) handleState(w http.ResponseWriter, r *http.Request) {
	rep, ok := s.roundTrip(newSettingsRequest(nil, 8*time.Second))
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Error: rep.err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{State: &rep.state})
}

func (s *settingsServer) handleSave(w http.ResponseWriter, r *http.Request) {
	var form settingsForm
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&form); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Error: "The settings could not be read: " + err.Error()})
		return
	}
	rep, ok := s.roundTrip(newSettingsRequest(&form, 8*time.Second))
	if !ok {
		status := http.StatusUnprocessableEntity
		if errors.Is(rep.err, errSettingsBusy) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, apiResponse{State: stateOf(rep), Warnings: rep.warnings, Error: rep.err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{State: &rep.state, Warnings: rep.warnings})
}

// languageOptions lists the model's languages by name, Japanese and English first.
func languageOptions() []languageOption {
	out := make([]languageOption, 0, len(languages))
	for code, l := range languages {
		out = append(out, languageOption{Code: code, Name: l.name})
	}
	sort.Slice(out, func(i, j int) bool {
		rank := func(c string) int {
			switch c {
			case "ja":
				return 0
			case "en":
				return 1
			}
			return 2
		}
		if ri, rj := rank(out[i].Code), rank(out[j].Code); ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// winKeyName is what the Win modifier is called on this system.
func winKeyName(goos string) string {
	switch goos {
	case "darwin":
		return "Cmd"
	case "linux":
		return "Super"
	}
	return "Win"
}

// formFromProfile splits a profile's hotkey into check boxes and a letter.
func formFromProfile(p Profile, goos string) (profileForm, error) {
	spec, err := parseHotkeyNames(p.Hotkey, goos != "windows", goos == "darwin")
	if err != nil {
		return profileForm{}, err
	}
	return profileForm{Ctrl: spec.ctrl, Alt: spec.alt, Shift: spec.shift, Win: spec.win, Key: string(rune(spec.key)),
		Languages: append([]string(nil), p.Languages...), Target: p.Target}, nil
}

// hotkeyString writes the form's hotkey the way config.json does.
func (f profileForm) hotkeyString() string {
	var parts []string
	for _, m := range []struct {
		on   bool
		name string
	}{{f.Ctrl, "Ctrl"}, {f.Alt, "Alt"}, {f.Shift, "Shift"}, {f.Win, "Win"}} {
		if m.on {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, strings.ToUpper(strings.TrimSpace(f.Key))), "+")
}

// toConfig is base with the form's profiles and auto-paste, validated like a
// config.json read at start-up. base is not modified.
func (f settingsForm) toConfig(base Config) (Config, error) {
	if len(f.Profiles) == 0 {
		return base, errors.New("There must be at least one hotkey.")
	}
	c := base
	c.Profiles = make([]Profile, len(f.Profiles))
	for i, p := range f.Profiles {
		if p.Target != "" && len(p.Languages) > 0 {
			return base, fmt.Errorf("profiles[%d]: use either two languages or one target language, not both", i)
		}
		c.Profiles[i] = Profile{Hotkey: p.hotkeyString()}
		if p.Target != "" {
			c.Profiles[i].Target = p.Target
		} else {
			c.Profiles[i].Languages = append([]string{}, p.Languages...)
		}
	}
	c.AutoPaste = f.AutoPaste
	if err := c.resolveProfiles(true); err != nil {
		return base, err
	}
	return c, nil
}
