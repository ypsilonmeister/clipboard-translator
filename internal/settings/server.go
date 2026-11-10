package settings

import (
	"context"
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

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/hotkeyspec"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
)

// The settings page is a small web page served by the app on 127.0.0.1 for as
// long as it is open. Everything it can change goes through the same validation
// as config.json (Config.resolveProfiles) and is applied on the UI thread: the
// handlers below run on other goroutines, so they never read or touch the
// app's state; they send a request to the main loop and wait for the answer.

//go:embed web
var settingsWeb embed.FS

const (
	TokenHeader     = "X-Settings-Token"
	settingsMaxBody = 64 << 10
)

// ProfileForm is one hotkey as the page edits it: the modifiers as check boxes
// and the key as a string (A-Z, 0-9, or F1-F12), so the page does not need to
// know the hotkey syntax.
type ProfileForm struct {
	Ctrl            bool     `json:"ctrl"`
	Alt             bool     `json:"alt"`
	Shift           bool     `json:"shift"`
	Win             bool     `json:"win"`
	Key             string   `json:"key"`
	Languages       []string `json:"languages,omitempty"`
	Target          string   `json:"target,omitempty"`
	Output          string   `json:"output,omitempty"`
	Input           string   `json:"input,omitempty"`
	CleanupNewlines string   `json:"cleanup_newlines,omitempty"`
}

// QuickstartGuide is the guide as the Settings page shows it (built by the app
// from its configuration, see guide.go).
type QuickstartGuide struct {
	Active  bool     `json:"active"` // a hotkey works, so the steps can be followed
	Intro   string   `json:"intro"`  // what to do now, or why nothing works yet
	Steps   []string `json:"steps,omitempty"`
	Extras  string   `json:"extras,omitempty"` // optional configuration, mentioned after the basics
	Initial bool     `json:"initial"`          // first run: the page opens the guide
}

// ErrConflict means the page saved on top of settings that changed meanwhile.
var ErrConflict = errors.New("The settings were changed while this page was open. Reload the settings to see the changes.")

// Form is everything the page edits.
type Form struct {
	BaseRevision      int           `json:"base_revision,omitempty"`
	Profiles          []ProfileForm `json:"profiles,omitempty"`
	AutoPaste         bool          `json:"auto_paste"`
	NotifyOnComplete  bool          `json:"notify_on_complete"`
	IdleUnloadMinutes int           `json:"idle_unload_minutes"`
	MaxChars          int           `json:"max_chars"`
	Changed           []string      `json:"changed,omitempty"`
}

type LanguageOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// State is what the page shows.
type State struct {
	ActualHotkeys []string `json:"actual_hotkeys,omitempty"`
	Wayland       bool     `json:"wayland,omitempty"`
	DesktopStatus string   `json:"desktop_status,omitempty"`
	Form
	Revision       int              `json:"revision"`
	Languages      []LanguageOption `json:"language_options"`
	MaxHotkeys     int              `json:"max_hotkeys"`
	WinKey         string           `json:"win_key"`  // what the Win modifier is called here
	Inactive       []string         `json:"inactive"` // hotkeys that could not be registered, with the reason
	NoActiveHotkey bool             `json:"no_active_hotkey"`
	Guide          QuickstartGuide  `json:"guide"`

	Models           []ModelState          `json:"models"`
	CustomModel      string                `json:"custom_model,omitempty"` // model_file when it is not one of the models above
	Engines          []EngineState         `json:"engines"`
	ConfiguredEngine string                `json:"configured_engine,omitempty"`
	RunningEngine    string                `json:"running_engine,omitempty"`
	Download         *models.DownloadState `json:"download,omitempty"`
	Busy             string                `json:"busy,omitempty"`   // what the app is doing now (loading a model ...)
	Notice           string                `json:"notice,omitempty"` // why the last switch did not work
	LicenseURL       string                `json:"license_url"`
}

type ModelState struct {
	Key               string `json:"key"`
	Name              string `json:"name"`
	Note              string `json:"note"`
	Size              string `json:"size"`
	Installed         bool   `json:"installed"` // GGUF availability, separate from the shared template
	TemplateInstalled bool   `json:"template_installed"`
	Active            bool   `json:"active"`
	Downloading       bool   `json:"downloading"`
	Partial           bool   `json:"partial"`
	PartialBytes      int64  `json:"partial_bytes"`
	HasFile           bool   `json:"has_file"`
	FileBytes         int64  `json:"file_bytes"`
	CanDelete         bool   `json:"can_delete"`
	CanDeletePartial  bool   `json:"can_delete_partial"`
	DeleteBlocked     string `json:"delete_blocked,omitempty"`
}

type EngineState struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}

// Request goes from a handler to the main loop. save is nil for "give me
// the state". The loop drops it once deadline has passed, so a save the page was
// told had failed is never applied later.
type Request struct {
	Save     *Form
	Action   string // "" (state or save), "use_model" or "engine"
	Arg      string // the model key or the engine name
	Kind     string // for "delete_model": "model" or "partial"
	Deadline time.Time
	Reply    chan Reply // buffered: the loop never blocks on a reader that gave up
}

type Reply struct {
	State    State
	Warnings []string
	Notice   string
	Err      error
}

var ErrBusy = errors.New("The app is busy (loading the model or restarting the server). Try again in a moment.")

func NewRequest(save *Form, wait time.Duration) Request {
	return Request{Save: save, Deadline: time.Now().Add(wait), Reply: make(chan Reply, 1)}
}

func NewActionRequest(action, arg string, wait time.Duration) Request {
	r := NewRequest(nil, wait)
	r.Action, r.Arg = action, arg
	return r
}

type Server struct {
	token string
	host  string // exactly what the browser sends as Host
	ln    net.Listener
	srv   *http.Server
	ask   func(Request) Reply
	dl    *models.Manager // owned by the app: a download outlives this server
	idle  time.Duration

	mu    sync.Mutex
	timer *time.Timer
	done  chan struct{}
	once  sync.Once
}

// Start listens on a free loopback port. The server closes itself
// after idle without a request (the page pings while it is open).
func Start(ask func(Request) Reply, dl *models.Manager, idle time.Duration) (*Server, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{token: hex.EncodeToString(raw), host: ln.Addr().String(), ln: ln, ask: ask, dl: dl, idle: idle, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.guard(false, s.serveIndex))
	mux.HandleFunc("/app.js", s.guard(false, s.serveAsset("web/app.js", "text/javascript; charset=utf-8")))
	mux.HandleFunc("/app.css", s.guard(false, s.serveAsset("web/app.css", "text/css; charset=utf-8")))
	mux.HandleFunc("/api/state", s.guard(false, s.handleState))
	mux.HandleFunc("/api/save", s.guard(true, s.handleSave))
	mux.HandleFunc("/api/model/download", s.guard(true, s.handleDownload))
	mux.HandleFunc("/api/model/cancel", s.guard(true, s.handleCancelDownload))
	mux.HandleFunc("/api/model/use", s.guard(true, s.handleAction("use_model", "key")))
	mux.HandleFunc("/api/model/delete", s.guard(true, s.handleDeleteModel))
	mux.HandleFunc("/api/engine", s.guard(true, s.handleAction("engine", "name")))
	mux.HandleFunc("/api/ping", s.guard(true, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("/api/bye", s.guard(true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		// Shut down only after this handler has written its reply: Close would cut
		// the connection first and the page would see EOF (seen on Linux CI).
		go s.shutdown()
	}))
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
	// Close may run before AfterFunc returns, so publish the timer under its lock.
	s.mu.Lock()
	s.timer = time.AfterFunc(idle, s.Close)
	s.mu.Unlock()
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// URL is the address to open in the browser; the token is part of it.
func (s *Server) URL() string { return "http://" + s.host + "/?t=" + s.token }

// Running reports whether the server still accepts requests (it may have closed
// itself after the idle time).
func (s *Server) Running() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *Server) Close() {
	s.once.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.timer.Stop()
		s.mu.Unlock()
		_ = s.srv.Close()
	})
}

// shutdown waits (briefly) for in-flight requests to finish, so their replies are
// written before the listener and connections go away.
func (s *Server) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	s.Close()
}

func (s *Server) touch() {
	s.mu.Lock()
	s.timer.Reset(s.idle)
	s.mu.Unlock()
}

// guard checks what keeps other web pages, other programs and DNS rebinding out:
// the Host must be our own address, the token must match (constant time), and a
// request that changes something must be JSON from our own origin.
func (s *Server) guard(write bool, h http.HandlerFunc) http.HandlerFunc {
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
		got := r.Header.Get(TokenHeader)
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

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := settingsWeb.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "missing page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(strings.ReplaceAll(string(b), "{{TOKEN}}", s.token)))
}

func (s *Server) serveAsset(name, contentType string) http.HandlerFunc {
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

type APIResponse struct {
	State    *State   `json:"state,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Notice   string   `json:"notice,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// roundTrip sends one request to the main loop and waits for its answer.
func (s *Server) roundTrip(req Request) (Reply, bool) {
	rep := s.ask(req)
	return rep, rep.Err == nil
}

// stateOf is the state to send back with an error, or nil when the loop never
// answered (busy): an empty state would blank the page.
func stateOf(rep Reply) *State {
	if len(rep.State.Profiles) == 0 {
		return nil
	}
	return &rep.State
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	rep, ok := s.roundTrip(NewRequest(nil, 8*time.Second))
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, APIResponse{Error: rep.Err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{State: &rep.State})
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		BaseRevision      *int           `json:"base_revision"`
		Profiles          *[]ProfileForm `json:"profiles"`
		AutoPaste         *bool          `json:"auto_paste"`
		NotifyOnComplete  *bool          `json:"notify_on_complete"`
		IdleUnloadMinutes *int           `json:"idle_unload_minutes"`
		MaxChars          *int           `json:"max_chars"`
		Changed           []string       `json:"changed"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Error: "The settings could not be read: " + err.Error()})
		return
	}
	form := Form{
		Changed: payload.Changed,
	}
	if payload.BaseRevision != nil {
		form.BaseRevision = *payload.BaseRevision
	}
	if payload.Profiles != nil {
		form.Profiles = *payload.Profiles
		if len(payload.Changed) == 0 {
			form.Changed = append(form.Changed, "profiles")
		}
	}
	if payload.AutoPaste != nil {
		form.AutoPaste = *payload.AutoPaste
		if len(payload.Changed) == 0 {
			form.Changed = append(form.Changed, "auto_paste")
		}
	}
	if payload.NotifyOnComplete != nil {
		form.NotifyOnComplete = *payload.NotifyOnComplete
		if len(payload.Changed) == 0 {
			form.Changed = append(form.Changed, "notify_on_complete")
		}
	}
	if payload.MaxChars != nil {
		form.MaxChars = *payload.MaxChars
		if len(payload.Changed) == 0 {
			form.Changed = append(form.Changed, "max_chars")
		}
	}
	if payload.IdleUnloadMinutes != nil {
		form.IdleUnloadMinutes = *payload.IdleUnloadMinutes
		if len(payload.Changed) == 0 {
			form.Changed = append(form.Changed, "idle_unload_minutes")
		}
	}
	rep, ok := s.roundTrip(NewRequest(&form, 8*time.Second))
	if !ok {
		status := http.StatusUnprocessableEntity
		if errors.Is(rep.Err, ErrBusy) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(rep.Err, ErrConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, APIResponse{State: stateOf(rep), Warnings: rep.Warnings, Error: rep.Err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{State: &rep.State, Warnings: rep.Warnings})
}

// decodeJSON reads a small JSON body into v and refuses unknown fields.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Error: "The request could not be read: " + err.Error()})
		return false
	}
	return true
}

// handleAction forwards "use this model" and "use this engine" to the main loop.
// The loop answers at once and then does the slow part; the page polls the state.
func (s *Server) handleAction(action, field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{}
		if !decodeJSON(w, r, &body) {
			return
		}
		rep, ok := s.roundTrip(NewActionRequest(action, body[field], 8*time.Second))
		if !ok {
			status := http.StatusUnprocessableEntity
			if errors.Is(rep.Err, ErrBusy) {
				status = http.StatusServiceUnavailable
			}
			writeJSON(w, status, APIResponse{State: stateOf(rep), Error: rep.Err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, APIResponse{State: &rep.State, Warnings: rep.Warnings})
	}
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if s.dl == nil {
		writeJSON(w, http.StatusNotImplemented, APIResponse{Error: "Downloads are not available."})
		return
	}
	if err := s.dl.Start(body.Key); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, APIResponse{Error: err.Error()})
		return
	}
	s.handleState(w, r)
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	if s.dl != nil {
		s.dl.Cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Kind != "model" && body.Kind != "partial" {
		writeJSON(w, http.StatusUnprocessableEntity, APIResponse{Error: "kind must be 'model' or 'partial'"})
		return
	}
	rep, ok := s.roundTrip(Request{
		Action:   "delete_model",
		Arg:      body.Key,
		Kind:     body.Kind,
		Deadline: time.Now().Add(8 * time.Second),
		Reply:    make(chan Reply, 1),
	})
	if !ok {
		status := http.StatusUnprocessableEntity
		if errors.Is(rep.Err, ErrBusy) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, APIResponse{State: stateOf(rep), Error: rep.Err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{State: &rep.State, Warnings: rep.Warnings, Notice: rep.Notice})
}

// LanguageOptions lists the model's languages by name, Japanese and English first.
func LanguageOptions() []LanguageOption {
	all := languages.All()
	out := make([]LanguageOption, 0, len(all))
	for _, l := range all {
		out = append(out, LanguageOption{Code: l.Code, Name: l.Name})
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

// WinKeyName is what the Win modifier is called on this system.
func WinKeyName(goos string) string {
	switch goos {
	case "darwin":
		return "Cmd"
	case "linux":
		return "Super"
	}
	return "Win"
}

// FormFromProfile splits a profile's hotkey into check boxes and a key name.
func FormFromProfile(p config.Profile, goos string) (ProfileForm, error) {
	spec, err := hotkeyspec.ParseNames(p.Hotkey, goos != "windows", goos == "darwin")
	if err != nil {
		return ProfileForm{}, err
	}
	return ProfileForm{Ctrl: spec.Ctrl, Alt: spec.Alt, Shift: spec.Shift, Win: spec.Win, Key: spec.Key,
		Languages: append([]string(nil), p.Languages...), Target: p.Target, Output: p.OutputMode(), Input: p.InputMode(),
		CleanupNewlines: p.CleanupNewlinesMode()}, nil
}

// hotkeyString writes the form's hotkey the way config.json does.
func (f ProfileForm) HotkeyString() string {
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
func (f Form) ToConfig(base config.Config) (config.Config, error) {
	c := base
	hasField := func(field string) bool {
		if len(f.Changed) == 0 {
			return true
		}
		for _, v := range f.Changed {
			if v == field {
				return true
			}
		}
		return false
	}
	if hasField("profiles") {
		if len(f.Profiles) == 0 {
			return base, errors.New("There must be at least one hotkey.")
		}
		c.Profiles = make([]config.Profile, len(f.Profiles))
		for i, p := range f.Profiles {
			if p.Target != "" && len(p.Languages) > 0 {
				return base, fmt.Errorf("profiles[%d]: use either two languages or one target language, not both", i)
			}
			c.Profiles[i] = config.Profile{Hotkey: p.HotkeyString(), Output: p.Output, Input: p.Input, CleanupNewlines: p.CleanupNewlines}
			if p.Target != "" {
				c.Profiles[i].Target = p.Target
			} else {
				c.Profiles[i].Languages = append([]string{}, p.Languages...)
			}
		}
	}
	if hasField("auto_paste") {
		c.AutoPaste = f.AutoPaste
	}
	if hasField("notify_on_complete") {
		c.NotifyOnComplete = f.NotifyOnComplete
	}
	if hasField("idle_unload_minutes") {
		if err := config.ValidateIdleUnloadMinutes(f.IdleUnloadMinutes); err != nil {
			return base, err
		}
		c.IdleUnloadMinutes = f.IdleUnloadMinutes
	}
	if hasField("max_chars") {
		if err := config.ValidateMaxChars(f.MaxChars); err != nil {
			return base, err
		}
		c.MaxChars = f.MaxChars
	}
	if err := c.ResolveProfiles(true); err != nil {
		return base, err
	}
	return c, nil
}
