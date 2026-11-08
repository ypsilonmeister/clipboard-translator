package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// app ties the tray, the llama-server supervisor, the clipboard and the hotkey
// together. Everything here runs on the one OS thread that owns the tray window
// and the hotkey; goroutines only talk to it through channels.
type app struct {
	dir      string
	conf     Config
	endpoint string
	paths    appPaths
	tray     Tray
	sup      *supervisor
	limiter  restartLimiter
	readyTip string

	activeBackend string // last known working backend ("cpu", "cuda", ...)

	hotkey    Hotkey                            // profile 0, the one the tray menu edits
	newHotkey func(spec string) (Hotkey, error) // newHotkey, replaceable in tests
	showErr   func(msg string)                  // showError, replaceable in tests
	// extras are the hotkeys of profiles 1.., in order; hk is nil when that
	// profile's hotkey could not be registered (the reason is in why).
	extras      []extraHotkey
	hotkeyLimit int // how many hotkeys this OS build can register, hotkeyLimit() in production

	settings       *settingsServer      // the settings page, while it is open
	settingsReqs   chan settingsRequest // page -> main loop
	settingsNotice string               // why the last switch started from the page did not work
	openURL        func(target string)  // openTarget, replaceable in tests
	dl             *downloadManager     // model downloads; they outlive the settings page

	confRevision int // bumped on every configuration change
	profilesRev  int // bumped when profiles (languages/hotkeys) change
	autoPasteRev int // bumped when auto_paste changes
	maxCharsRev  int // bumped when max_chars changes

	updates  chan updateResult
	checking bool
	latest   releaseInfo // newest release seen by "Check for updates"

	cb             Clipboard
	results        chan result
	generation     uint64 // bumped by every new request; older results are ignored
	cancelCurrent  context.CancelFunc
	translating    bool
	translateStart time.Time
	lastTranslSec  int
	hist           history
	foreground     func() uintptr                                        // foregroundWindow, replaceable in tests
	paste          func(target uintptr) error                            // pasteIntoForeground, replaceable in tests
	capture        func(cb Clipboard, win uintptr) (string, bool, error) // captureSelectedText, replaceable in tests
}

type extraHotkey struct {
	profile int // index into conf.Profiles
	hk      Hotkey
	why     string // why hk is nil
}

func newApp(dir string, conf Config, paths appPaths, tray Tray, serverLog io.Writer) *app {
	conf.normalizeProfiles()
	a := &app{
		confRevision: 1,
		profilesRev:  1,
		autoPasteRev: 1,
		maxCharsRev:  1,
		hotkeyLimit:  hotkeyLimit(),
		settingsReqs: make(chan settingsRequest, 8),
		dl:           newDownloadManager(dir),
		openURL:      openTarget,
		dir:          dir,
		conf:         conf,
		endpoint:     fmt.Sprintf("http://127.0.0.1:%d", conf.Port),
		paths:        paths,
		tray:         tray,
		limiter:      restartLimiter{max: 3, window: 10 * time.Minute},
		readyTip:     "Clipboard Translator - ready (" + conf.Hotkey + ")",

		newHotkey:  newHotkey,
		showErr:    showError,
		updates:    make(chan updateResult, 1),
		results:    make(chan result, 16),
		foreground: foregroundWindow,
		paste:      pasteIntoForeground,
		capture:    captureSelectedText,
	}
	tray.SetMenu(a.buildMenu)
	a.sup = &supervisor{
		model: paths.model, template: paths.template,
		contextTokens: conf.ContextTokens, port: conf.Port, endpoint: a.endpoint,
		logOut:         serverLog,
		startupTimeout: time.Duration(conf.StartupTimeoutSec) * time.Second,
		pidFile:        childRecordPath(dir, conf.Port),
	}
	lastSec := time.Duration(-1)
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		pumpMessages()
		a.serviceSettingsWhileBusy(label)
		if sec := elapsed / time.Second; sec != lastSec {
			lastSec = sec
			tray.SetStatus(fmt.Sprintf("Clipboard Translator - %s... (%ds)", label, sec))
		}
		return tray.QuitRequested()
	}
	return a
}

// failed reports an unrecoverable problem in the log and to the user.
func (a *app) failed(err error) {
	msg := err.Error()
	log.Print(msg)
	a.showErr(msg + "\nSee server.log and translator.log in the app folder.")
}

// startServer starts the preferred backend and falls back to CPU if that
// fails. It returns errQuit when the user quit while the model was loading.
// The fallback leaves conf.ServerFile alone: the preferred engine is tried
// again at the next start, and runningBackend tells what runs now.
func (a *app) startServer() error {
	err := a.sup.start(a.paths.server, a.conf.GPULayers, "loading model")
	if err == nil {
		a.activeBackend = currentBackend(a.conf.ServerFile)
		return nil
	}
	if errors.Is(err, errQuit) {
		return err
	}
	log.Printf("preferred backend failed: %v; see server.log", err)
	if a.paths.server == a.paths.fallback {
		return fmt.Errorf("The translation server failed to start: %w", err)
	}
	return a.startFallback()
}

// runningBackend is the engine ("cpu", "cuda", ...) of the llama-server that
// runs now. After startServer fell back to the CPU that is not the configured
// one, so the menu and the settings page ask this, not conf.ServerFile. With no
// server running (while one is restarted or switched) it is the last engine
// seen working, so the page does not show an engine that has not come up yet;
// before the first start it is the configured engine, the one that will be
// tried. It remembers what it sees in activeBackend.
func (a *app) runningBackend() string {
	if a.sup.Running() {
		rel, err := filepath.Rel(a.dir, a.sup.cmd.Path)
		if err == nil {
			b := currentBackend(rel)
			a.activeBackend = b
			return b
		}
	}
	if a.activeBackend != "" {
		return a.activeBackend
	}
	return currentBackend(a.conf.ServerFile)
}

// onFallback reports whether the server running now is the CPU fallback that
// startServer chose because the preferred engine did not start.
func (a *app) onFallback() bool {
	return a.sup.Running() && a.paths.server != a.paths.fallback && a.sup.cmd.Path == a.paths.fallback
}

// startFallback starts the CPU fallback build.
func (a *app) startFallback() error {
	if statErr := checkFile(a.paths.fallback); statErr != nil {
		return fmt.Errorf("CPU fallback unavailable: %w", statErr)
	}
	log.Print("falling back to CPU")
	if err := a.sup.start(a.paths.fallback, 0, "loading model (CPU)"); err != nil {
		if errors.Is(err, errQuit) {
			return err
		}
		return fmt.Errorf("CPU fallback failed: %w", err)
	}
	a.activeBackend = "cpu"
	return nil
}

// sleepPumping waits d while keeping the tray responsive. It reports true when
// the user asked to quit in the meantime.
func (a *app) sleepPumping(d time.Duration) bool {
	end := time.Now().Add(d)
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for time.Now().Before(end) {
		<-t.C
		pumpMessages()
		a.serviceSettingsWhileBusy("restarting the translation server")
		if a.tray.QuitRequested() {
			return true
		}
	}
	return false
}

// restartServer brings the server back after it died. It gives up (and reports
// false) when the user quits or the server keeps dying.
func (a *app) restartServer() bool {
	for {
		if !a.limiter.Allow(time.Now()) {
			a.failed(fmt.Errorf("The translation server keeps stopping (%d times in %s), so the app is quitting",
				a.limiter.max, a.limiter.window))
			return false
		}
		wait := restartBackoff(a.limiter.Attempt())
		log.Printf("restarting the server in %s (attempt %d of %d)", wait, a.limiter.Attempt(), a.limiter.max)
		a.tray.SetStatus("Clipboard Translator - server stopped, restarting...")
		a.tray.Notify("Clipboard Translator", "The translation server stopped. Restarting it...", true)
		if a.sleepPumping(wait) {
			log.Print("quit requested during restart")
			return false
		}
		err := a.startServer()
		if err == nil {
			break
		}
		if errors.Is(err, errQuit) {
			log.Print("quit requested during restart")
			return false
		}
		log.Printf("restart failed: %v", err)
	}
	log.Print("server restarted; ready")
	a.tray.SetStatus(a.readyTip)
	a.tray.Notify("Clipboard Translator", "The translation server is back. Ready.", false)
	return true
}

// recoverServer brings the translation server back after it died or after a
// failed engine switch left none. It reports false when the app should quit.
func (a *app) recoverServer() bool {
	a.abortTranslation() // a translation in flight can no longer succeed
	a.sup.Stop()
	return a.restartServer()
}

// run starts the server and then serves hotkey presses until the user quits.
func (a *app) run() {
	a.tray.SetStatus("Clipboard Translator - loading model...")
	defer a.sup.Stop()
	if err := a.startServer(); err != nil {
		if errors.Is(err, errQuit) {
			log.Print("quit requested during startup")
			return
		}
		a.failed(err)
		return
	}

	cb, err := newClipboard()
	if err != nil {
		log.Printf("clipboard initialization failed: %v", err)
		a.showErr(fmt.Sprintf("Clipboard initialization failed: %v", err))
		return
	}
	defer cb.Close()
	a.registerStartupHotkey()
	defer a.closeSettings()
	defer a.dl.Close() // closes the partial file of a running download before the process ends
	defer func() {     // a.hotkey changes when the user picks another one
		_ = a.hotkey.Close()
		for _, e := range a.extras {
			if e.hk != nil {
				_ = e.hk.Close()
			}
		}
	}()
	a.serve(cb)
}

// registerExtraHotkeys registers the hotkeys of profiles 1.. after profile 0. A
// hotkey another program owns, or one this OS build cannot hold, only disables
// its own profile: the others and the tray keep working.
func (a *app) registerExtraHotkeys() {
	a.extras = nil
	for i := 1; i < len(a.conf.Profiles); i++ {
		e := extraHotkey{profile: i}
		spec := a.conf.Profiles[i].Hotkey
		switch {
		case i >= a.hotkeyLimit:
			e.why = fmt.Sprintf("this build registers %d hotkey(s) at most", a.hotkeyLimit)
		default:
			hk, err := a.newHotkey(spec)
			if err != nil {
				e.why = err.Error()
			} else {
				e.hk = hk
			}
		}
		if e.hk == nil {
			log.Printf("hotkey %s (profile %d): %s", spec, i+1, e.why)
		}
		a.extras = append(a.extras, e)
	}
}

// hotkeySummary lists the working hotkeys with what each one does, for the
// start-up message. With a single profile that is just the hotkey.
func (a *app) hotkeySummary() string {
	var parts []string
	add := func(i int) {
		p := a.conf.Profiles[i]
		if len(a.conf.Profiles) == 1 {
			parts = append(parts, p.Hotkey)
			return
		}
		parts = append(parts, p.Hotkey+" ("+p.label()+")")
	}
	if !a.hasNoHotkey() {
		add(0)
	}
	for _, e := range a.extras {
		if e.hk != nil {
			add(e.profile)
		}
	}
	return strings.Join(parts, ", ")
}

// extraFailures describes the extra profiles whose hotkey is not active.
func (a *app) extraFailures() string {
	var lines []string
	for _, e := range a.extras {
		if e.hk == nil {
			lines = append(lines, fmt.Sprintf("%s (%s): %s", a.conf.Profiles[e.profile].Hotkey, a.conf.Profiles[e.profile].label(), e.why))
		}
	}
	return strings.Join(lines, "\n")
}

// registerStartupHotkey registers the configured hotkey. If another program
// owns it, the app still starts (without a hotkey) so the user can pick a free
// one from the tray menu instead of having to edit config.json.
func (a *app) registerStartupHotkey() {
	hk, err := a.newHotkey(a.conf.Hotkey)
	if err != nil {
		log.Printf("hotkey %s: %v", a.conf.Hotkey, err)
		a.hotkey = noHotkey{}
	} else {
		a.hotkey = hk
	}
	a.registerExtraHotkeys()
	a.updateReadyTip()
}

// updateReadyTip sets the idle tooltip from the hotkeys that work.
func (a *app) updateReadyTip() {
	if s := a.hotkeySummary(); s != "" {
		a.readyTip = "Clipboard Translator - ready (" + s + ")"
	} else {
		a.readyTip = noHotkeyTip
	}
}

func (a *app) serve(cb Clipboard) {
	a.cb = cb
	tray := a.tray
	tray.SetStatus(a.readyTip)
	if a.hasNoHotkey() {
		log.Printf("ready, but the hotkey %s could not be registered; waiting for the user to pick another", a.conf.Hotkey)
		tray.Notify("Clipboard Translator", fmt.Sprintf("Could not register the hotkey %s (another program may be using it). Choose another one from the tray menu: Hotkey.", a.conf.Hotkey), true)
	} else {
		log.Printf("ready: select text and press %s (or translate existing clipboard)", a.hotkeySummary())
		tray.Notify("Clipboard Translator", "Ready. Select text and press "+a.hotkeySummary()+", then paste with "+pasteShortcut()+".", false)
	}
	if f := a.extraFailures(); f != "" {
		tray.Notify("Clipboard Translator", "These hotkeys are not active:\n"+f, true)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	// The same tick also services the tray window, so never poll slower than 100ms
	// or the tray menu would feel frozen.
	pollInterval := time.Duration(a.conf.PollMS) * time.Millisecond
	if pollInterval > 100*time.Millisecond {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	stopFile := filepath.Join(a.dir, "STOP")
	defer os.Remove(stopFile)
	for {
		select {
		case <-sig:
			log.Print("stopping")
			a.cancelTranslation()
			return
		case <-a.sup.Exited():
			log.Print("llama-server exited unexpectedly")
			if !a.recoverServer() {
				return
			}
		case <-ticker.C:
			_, statErr := os.Stat(stopFile)
			if statErr == nil || tray.QuitRequested() {
				log.Print("stop requested")
				a.cancelTranslation()
				return
			}
			pressed, pressedProfile, err := a.pollHotkeys()
			if err != nil {
				log.Printf("hotkey error: %v", err)
				continue
			}
			// Pressed pumps messages, so "Quit" or a menu item may have been chosen just now.
			if tray.QuitRequested() {
				log.Print("stop requested")
				a.cancelTranslation()
				return
			}
			for {
				if tray.QuitRequested() {
					break
				}
				id, ok := tray.NextCommand()
				if !ok {
					break
				}
				a.handleCommand(id)
				if tray.QuitRequested() {
					break
				}
			}
			if tray.QuitRequested() { // e.g. Quit during an engine switch
				log.Print("stop requested")
				a.cancelTranslation()
				return
			}
			// An engine switch that failed twice leaves no server at all, and
			// Exited() is nil then; recover (or quit) rather than serve nothing.
			if !a.sup.Running() {
				log.Print("no translation server is running")
				if !a.recoverServer() {
					return
				}
				continue
			}
			a.tickTranslationStatus()
			if pressed {
				a.startTranslation(pressedProfile)
			}
		case req := <-a.settingsReqs:
			a.handleSettingsRequest(req)
		case st := <-a.dl.events:
			a.onDownloadFinished(st)
		case u := <-a.updates:
			a.onUpdateResult(u)
		case res := <-a.results:
			a.handleResult(res)
		}
	}
}

// pollHotkeys asks every registered hotkey whether it fired and returns the
// profile to run. Every hotkey must be asked each tick: on Windows the first
// call drains the message queue and the others read what it recorded. If two
// fired in one tick the later profile wins, as a newer request replaces an older
// one. An error from profile 0 is returned; one from another profile is logged.
func (a *app) pollHotkeys() (pressed bool, profile int, err error) {
	pressed, err = a.hotkey.Pressed()
	if err != nil {
		return false, 0, err
	}
	for _, e := range a.extras {
		if e.hk == nil {
			continue
		}
		if p, err := e.hk.Pressed(); err != nil {
			log.Printf("hotkey error (profile %d): %v", e.profile+1, err)
		} else if p {
			pressed, profile = true, e.profile
		}
	}
	return pressed, profile, nil
}

// cancelTranslation stops the request in flight, if any.
func (a *app) cancelTranslation() {
	if a.cancelCurrent != nil {
		a.cancelCurrent()
		a.cancelCurrent = nil
	}
}

// abortTranslation cancels the request in flight and makes sure its result,
// should it still arrive, is ignored.
func (a *app) abortTranslation() {
	a.cancelTranslation()
	a.generation++
	a.translating = false
}

// tickTranslationStatus keeps the tooltip's elapsed seconds current.
func (a *app) tickTranslationStatus() {
	if !a.translating {
		return
	}
	if sec := int(time.Since(a.translateStart) / time.Second); sec != a.lastTranslSec {
		a.lastTranslSec = sec
		a.tray.SetStatus(fmt.Sprintf("Clipboard Translator - translating... (%ds)", sec))
	}
}

// startTranslation reacts to the hotkey of profile index: it grabs the
// selection (or the clipboard) and translates it in the background. A new
// request cancels the older one and invalidates its result.
func (a *app) startTranslation(index int) {
	profile := a.conf.Profiles[index]
	win := a.foreground()
	a.abortTranslation()
	source, fromSelection, err := a.capture(a.cb, win)
	if err != nil {
		log.Printf("cannot capture selection: %v", err)
		a.tray.SetStatus(a.readyTip)
		if errors.Is(err, errCaptureModifiersHeld) || errors.Is(err, errCaptureFocusChanged) {
			// Say so: translating the old clipboard text instead would be a silent wrong answer.
			a.tray.Notify("Clipboard Translator", "Nothing was translated: "+err.Error()+". Release the keys and press the hotkey again.", true)
		}
		return
	}
	direction, ok := profile.direction(source, a.conf.MaxChars)
	if !ok {
		log.Print("skipped: empty, unsupported, URL, or too long")
		a.tray.SetStatus(a.readyTip)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.conf.TranslationTimeoutSec)*time.Second)
	a.cancelCurrent = cancel
	a.translating, a.translateStart, a.lastTranslSec = true, time.Now(), 0
	g := a.generation
	key := a.sup.apiKey // read here: the supervisor belongs to this thread
	from := direction.Source
	if from == "" {
		from = "auto"
	}
	log.Printf("translating %s -> %s", from, direction.Target)
	a.tray.SetStatus("Clipboard Translator - translating... (0s)")
	go func() {
		translated, err := translate(ctx, a.endpoint, key, source, direction, a.conf.MaxTokens)
		select {
		case a.results <- result{original: source, translated: translated, generation: g, err: err,
			direction: direction, window: win, fromSelection: fromSelection}:
		default:
		}
	}()
}

// handleResult puts a finished translation into the clipboard, remembers it,
// and pastes it when auto-paste is on and it is safe to.
func (a *app) handleResult(res result) {
	if res.generation != a.generation {
		return
	}
	a.translating = false
	a.tray.SetStatus(a.readyTip)
	if res.err != nil {
		if !errors.Is(res.err, context.Canceled) {
			log.Printf("translation failed: %v", res.err)
			msg := "See translator.log for details."
			if errors.Is(res.err, errTruncated) {
				msg = "The translation was cut off at the token limit, so the clipboard was left unchanged. Select less text, or raise max_tokens / context_tokens in config.json."
			}
			a.tray.Notify("Translation failed", msg, true)
		}
		return
	}
	if res.translated == res.original {
		log.Print("translation unchanged; clipboard left intact")
		return
	}
	// A sequence number is not a reliable test for a text change:
	// apps can update clipboard formats without changing the text.
	// Compare text AND replace under a single OpenClipboard lock.
	applied, err := applyTranslation(a.cb, res.original, res.translated)
	if err != nil {
		log.Printf("clipboard check/write failed: %v", err)
		return
	}
	if !applied {
		log.Print("clipboard text changed during translation; result discarded (new clipboard preserved)")
		return
	}
	a.hist.Add(historyEntry{Source: res.original, Translated: res.translated, Direction: res.direction})
	switch decidePaste(a.conf.AutoPaste, res.fromSelection, res.window, a.foreground()) {
	case pasteNow:
		// decidePaste just confirmed the window is still res.window; that is the
		// target pasteWithFocus must still find in front right before Ctrl+V.
		if err := a.paste(res.window); err != nil {
			log.Printf("auto-paste failed: %v", err)
			a.tray.Notify("Clipboard Translator", "Translation ready, but it could not be pasted ("+err.Error()+"). Press "+pasteShortcut()+".", true)
			return
		}
		log.Print("translation pasted")
		a.tray.SetStatus("Clipboard Translator - translation pasted")
	case pasteWindowChanged:
		log.Print("translation ready; the active window changed, so it was not pasted")
		a.tray.Notify("Clipboard Translator", "Translation ready in the clipboard. The active window changed, so it was not pasted. Press "+pasteShortcut()+".", false)
		a.tray.SetStatus("Clipboard Translator - translation ready (" + pasteShortcut() + ")")
	default:
		log.Print("translation ready in clipboard (", pasteShortcut(), " to paste)")
		a.tray.SetStatus("Clipboard Translator - translation ready (" + pasteShortcut() + ")")
	}
}

// pasteIntoForeground waits for the hotkey's modifiers to be released and then
// types Ctrl+V into target, but only if target is still the window in front.
func pasteIntoForeground(target uintptr) error {
	return pasteWithFocus(target, foregroundWindow, waitModifiersReleased, sendPaste)
}

// pasteWithFocus sends Ctrl+V only if target (the window the selection came
// from) is in front after the modifier wait, which can take up to a second and
// during which the user may have switched windows. On failure the translation
// stays on the clipboard.
func pasteWithFocus(target uintptr, foreground func() uintptr, wait func(time.Duration) bool, send func() error) error {
	if !wait(time.Second) {
		return errors.New("modifier keys are still held down")
	}
	if target == 0 || foreground() != target {
		return errors.New("the focused window changed while waiting to paste")
	}
	return send()
}
