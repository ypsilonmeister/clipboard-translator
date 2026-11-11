package app

import (
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
	"strconv"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

const translateAPIAddress = "0.0.0.0:18121"
const translateTokenHeader = "X-Clipboard-Translator-Token"

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
// IPv4 wildcard binding permits explicit Crostini port forwarding from ChromeOS.
func (a *app) startTranslateAPI() error {
	if err := a.ensureTranslateAPIToken(); err != nil {
		return err
	}
	if !a.conf.TranslateAPIEnabled || a.apiServer != nil {
		return nil
	}
	ln, err := net.Listen("tcp4", translateAPIAddress)
	if err != nil {
		return err
	}
	a.apiServer = &http.Server{Handler: translateAPIHandler(a.conf.TranslateAPIToken, a.askSettings), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 47 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	srv := a.apiServer
	go func() { _ = srv.Serve(ln) }()
	log.Printf("translation endpoint listening on %s (token required)", translateAPIAddress)
	return nil
}
func (a *app) stopTranslateAPI() {
	if a.apiServer != nil {
		_ = a.apiServer.Close()
		a.apiServer = nil
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
		rep := ask(req)
		if rep.Err != nil {
			code := 422
			if errors.Is(rep.Err, settings.ErrBusy) {
				code = 503
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
		req.Reply <- settings.Reply{Err: errors.New("Unknown profile; use its hotkey or zero-based index")}
		return
	}
	p := a.conf.Profiles[index]
	reason, _ := p.Check(req.Text, a.conf.MaxChars)
	if reason != config.SkipNone {
		req.Reply <- settings.Reply{Err: fmt.Errorf("Text cannot be translated: %s", reason)}
		return
	}
	a.abortTranslation()
	a.apiReply = req.Reply
	a.translateCaptured(p, 0, req.Text, false, nil)
	// Reload failures or a canceled startup must not leave an HTTP caller waiting.
	if !a.translating && a.apiReply != nil {
		a.finishAPI("", errors.New("Translation did not start"))
	}
}
func (a *app) finishAPI(text string, err error) {
	if a.apiReply != nil {
		a.apiReply <- settings.Reply{Translation: text, Err: err}
		a.apiReply = nil
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
	}{"http://localhost:18121/translate", a.conf.TranslateAPIToken})
	a.abortTranslation()
	a.finishHistoryCopy(string(data))
}
