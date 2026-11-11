package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

const translateAPIPort = "18121"

var translateAPISupported = func() bool { return runtime.GOOS == "linux" }
var errAPIInput = errors.New("untranslatable input")
var errAPIRequest = errors.New("invalid request")

func translateAPIURL() string {
	return "http://" + net.JoinHostPort("localhost", translateAPIPort) + "/translate"
}

const translateTokenHeader = "X-Clipboard-Translator-Token"

// Tokens are created only for enable or connection-info requests.
func (a *app) ensureTranslateAPIToken() error {
	if a.conf.TranslateAPIToken == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		token := hex.EncodeToString(raw)
		if err := config.UpdateFile(a.dir, map[string]any{"translate_api_token": token}); err != nil {
			return err
		}
		a.conf.TranslateAPIToken = token
	}
	return nil
}

// The dedicated listener exposes no settings or engine administration routes.
// Crostini forwarding terminates on the container address, not loopback.
func (a *app) startTranslateAPI() error {
	if !translateAPISupported() || !a.conf.TranslateAPIEnabled || a.apiServer != nil {
		return nil
	}
	if err := a.ensureTranslateAPIToken(); err != nil {
		return err
	}
	addresses, warning := platform.TranslationAPIBindHosts()
	if warning != "" {
		a.tray.Notify("Clipboard Translator", warning, true)
	}
	listeners := make([]net.Listener, 0, len(addresses))
	for _, host := range addresses {
		ln, err := net.Listen("tcp4", net.JoinHostPort(host, translateAPIPort))
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return err
		}
		listeners = append(listeners, ln)
	}
	a.apiServer = &http.Server{Handler: translateAPIHandler(a.conf.TranslateAPIToken, a.askSettings), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 47 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	srv := a.apiServer
	for _, ln := range listeners {
		go func(ln net.Listener) { _ = srv.Serve(ln) }(ln)
	}
	log.Printf("translation endpoint listening on %v port %s (token required)", addresses, translateAPIPort)
	return nil
}
func (a *app) stopTranslateAPI() {
	if a.apiReply != nil {
		a.abortTranslation()
	}
	if srv := a.apiServer; srv != nil {
		a.apiServer = nil
		// Shutdown must not block the UI loop while handlers finish their replies.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				_ = srv.Close()
			}
		}()
	}
}

func translateAPIHandler(token string, ask func(settings.Request) settings.Reply) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(code int, message string) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		}
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(translateTokenHeader)), []byte(token)) != 1 {
			fail(401, "Invalid translation token")
			return
		}
		if r.URL.Path != "/translate" {
			fail(404, "Not found")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			fail(405, "Use POST")
			return
		}
		// Ordinary web pages cannot call the API: no CORS grants and no redirects.
		if origin := r.Header.Get("Origin"); origin != "" && !isExtensionOrigin(origin) {
			fail(403, "Origin is not allowed")
			return
		}
		var body struct {
			Text    string `json:"text"`
			Profile string `json:"profile,omitempty"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			fail(400, "Invalid translation JSON")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			fail(400, "Expected one JSON object")
			return
		}
		req := settings.NewActionRequest("translate", "", 46*time.Minute)
		req.Text, req.Profile = body.Text, body.Profile
		ctx, cancel := context.WithDeadline(r.Context(), req.Deadline)
		defer cancel()
		req.Context = ctx
		rep := ask(req)
		if rep.Err != nil {
			code := http.StatusBadGateway
			switch {
			case errors.Is(rep.Err, context.Canceled), errors.Is(rep.Err, context.DeadlineExceeded) && ctx.Err() != nil:
				fail(409, "request cancelled")
				return
			case errors.Is(rep.Err, settings.ErrBusy):
				code = 503
			case errors.Is(rep.Err, context.DeadlineExceeded):
				code = 504
			case errors.Is(rep.Err, errAPIInput):
				code = 422
			case errors.Is(rep.Err, errAPIRequest):
				code = 400
			}
			fail(code, rep.Err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"translation": rep.Translation})
	})
}
func isExtensionOrigin(origin string) bool {
	const prefix = "chrome-extension://"
	if len(origin) != len(prefix)+32 || origin[:len(prefix)] != prefix {
		return false
	}
	for _, c := range origin[len(prefix):] {
		if c < 'a' || c > 'p' {
			return false
		}
	}
	return true
}

func (a *app) configureTranslateAPI(req settings.Request) {
	enabled := req.Arg == "true"
	if req.Arg != "true" && req.Arg != "false" {
		req.Reply <- settings.Reply{Err: errors.New("enabled must be true or false")}
		return
	}
	if enabled && !translateAPISupported() {
		req.Reply <- settings.Reply{Err: errors.New("Translation endpoint is available only on Linux")}
		return
	}
	old := a.conf.TranslateAPIEnabled
	a.conf.TranslateAPIEnabled = enabled
	err := a.startTranslateAPI()
	if err == nil {
		err = config.UpdateFile(a.dir, map[string]any{"translate_api_enabled": enabled})
	}
	if err != nil {
		a.conf.TranslateAPIEnabled = old
		if !old {
			a.stopTranslateAPI()
		}
	} else if !enabled {
		a.stopTranslateAPI()
	}
	req.Reply <- settings.Reply{State: a.settingsState(), Err: err}
}

func (a *app) handleAPITranslation(req settings.Request) {
	if !a.conf.TranslateAPIEnabled {
		req.Reply <- settings.Reply{Err: errors.New("Translation endpoint is disabled")}
		return
	}
	if a.busy() || a.capturing != nil || a.pendingRead != nil {
		req.Reply <- settings.Reply{Err: settings.ErrBusy}
		return
	}
	// Profiles have no persistent IDs: use their configured hotkey as the ID,
	// with a zero-based numeric index accepted for simple clients.
	index := 0
	if req.Profile != "" {
		index = -1
		for i, p := range a.conf.Profiles {
			if p.Hotkey == req.Profile {
				index = i
				break
			}
		}
		if index < 0 {
			n, err := strconv.Atoi(req.Profile)
			if err == nil && n >= 0 && n < len(a.conf.Profiles) {
				index = n
			}
		}
	}
	if index < 0 {
		req.Reply <- settings.Reply{Err: fmt.Errorf("%w: unknown profile; use its hotkey or zero-based index", errAPIRequest)}
		return
	}
	p := a.conf.Profiles[index]
	reason, _ := p.Check(req.Text, a.conf.MaxChars)
	if reason != config.SkipNone {
		req.Reply <- settings.Reply{Err: fmt.Errorf("%w: %s", errAPIInput, config.SkipMessage(reason, utf8.RuneCountInString(req.Text), a.conf.MaxChars, p.Label()))}
		return
	}
	a.abortTranslation()
	a.apiReply = req.Reply
	a.apiContext = req.Context
	a.translateCaptured(p, 0, req.Text, false, nil)
	// Reload failures or a canceled startup must not leave an HTTP caller waiting.
	if !a.translating && a.apiReply != nil {
		a.finishAPI("", errors.New("Translation did not start"))
	}
}
func (a *app) finishAPI(text string, err error) {
	if a.apiReply != nil {
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("translation failed: %v", err)
		}
		a.apiReply <- settings.Reply{Translation: text, Err: err}
		a.apiReply = nil
		a.apiContext = nil
	}
}

func (a *app) tickAPIContext() {
	if a.apiContext != nil && a.apiContext.Err() != nil {
		err := a.apiContext.Err()
		a.finishAPI("", err)
		a.abortTranslation()
	}
}

// copyConnectionInfo shares token persistence with endpoint startup without opening a listener.
func (a *app) copyConnectionInfo() {
	if err := a.ensureTranslateAPIToken(); err != nil {
		a.tray.Notify("Clipboard Translator", "Could not save connection info: "+err.Error(), true)
		return
	}
	data, _ := json.Marshal(struct {
		Endpoint string `json:"endpoint"`
		Token    string `json:"token"`
	}{translateAPIURL(), a.conf.TranslateAPIToken})
	if err := a.cb.Write(string(data)); err != nil {
		a.tray.Notify("Clipboard Translator", "Could not copy connection info: "+err.Error(), true)
		return
	}
	a.tray.Notify("Clipboard Translator", "Connection info copied. The clipboard now holds the token in clear text.", false)
}
