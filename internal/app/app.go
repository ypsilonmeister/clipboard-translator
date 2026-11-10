package app

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
	"unicode/utf8"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/update"
)

// app ties the tray, the llama-server supervisor, the clipboard and the hotkey
// together. Everything here runs on the one OS thread that owns the tray window
// and the hotkey; goroutines only talk to it through channels.
type app struct {
	dir      string
	conf     config.Config
	endpoint string
	paths    config.Paths
	tray     platform.Tray
	sup      *llamaserver.Supervisor
	limiter  llamaserver.RestartLimiter
	readyTip string

	modelUnloaded   bool // intentional stop; hotkeys load it again
	serverWork      int  // nested start/switch/recovery calls on the UI thread
	settingsServing bool
	idleSince       time.Time
	idleUnloadRev   int

	pendingDuringLoad *int
	sig               chan os.Signal
	sigQuit           bool

	activeBackend string // last known working backend ("cpu", "cuda", ...)

	hotkey    platform.Hotkey                            // profile 0
	newHotkey func(spec string) (platform.Hotkey, error) // newHotkey, replaceable in tests
	showErr   func(msg string)                           // showError, replaceable in tests
	// extras are the hotkeys of profiles 1.., in order; hk is nil when that
	// profile's hotkey could not be registered (the reason is in why).
	extras      []extraHotkey
	hotkeyLimit int // how many hotkeys this OS build can register, hotkeyLimit() in production

	settings       *settings.Server          // the settings page, while it is open
	settingsReqs   chan settings.Request     // page -> main loop
	settingsNotice string                    // why the last switch started from the page did not work
	settingsIdle   time.Duration             // idle timeout for the settings server; 0 uses settingsIdle
	openURL        func(target string) error // openTarget, replaceable in tests
	fallbackEditor func(path string) error   // startFallbackEditor, replaceable in tests
	askYesNo       func(msg string) bool     // askYesNo, replaceable in tests
	dl             *models.Manager           // model downloads; they outlive the settings page

	confRevision        int // bumped on every configuration change
	profilesRev         int // bumped when profiles (languages/hotkeys) change
	autoPasteRev        int // bumped when auto_paste changes
	notifyOnCompleteRev int // bumped when notify_on_complete changes
	maxCharsRev         int // bumped when max_chars changes

	updates  chan updateResult
	checking bool
	latest   update.ReleaseInfo // newest release seen by "Check for updates"

	cb             platform.Clipboard
	results        chan result
	generation     uint64 // bumped by every new request; older results are ignored
	cancelCurrent  context.CancelFunc
	translating    bool
	translateStart time.Time
	lastTranslSec  int
	hist           history
	foreground     func() uintptr                                                 // foregroundWindow, replaceable in tests
	paste          func(target uintptr) error                                     // pasteIntoForeground, replaceable in tests
	capture        func(cb platform.Clipboard, win uintptr) (string, bool, error) // captureSelectedText, replaceable in tests
	beginCapture   func(cb platform.Clipboard, win uintptr) platform.CaptureTask  // beginSelectionCapture; nil (or a nil task) uses capture
	capturing      *pendingCapture                                                // a selection copy the serve loop is still waiting for

	statusResetAt  time.Time
	lastSkipReason config.SkipReason
	lastSkipNotify time.Time
	now            func() time.Time // time.Now, replaceable in tests
}

// pendingCapture is a hotkey press whose selection is still being copied. The
// profile is a copy, so a Settings change meanwhile does not move it.
type pendingCapture struct {
	task    platform.CaptureTask
	profile config.Profile
	win     uintptr
	started time.Time
	noted   bool // the tray says "copying the selection..."
}

// copyingNoteAfter is how long a copy may take before the tray says so. Most
// copies finish well within it, so a normal press shows no extra status.
const copyingNoteAfter = 300 * time.Millisecond

type extraHotkey struct {
	profile int // index into conf.Profiles
	hk      platform.Hotkey
	why     string // why hk is nil
}

func newApp(dir string, conf config.Config, paths config.Paths, tray platform.Tray, serverLog io.Writer) *app {
	conf.NormalizeProfiles()
	a := &app{
		confRevision:        1,
		profilesRev:         1,
		autoPasteRev:        1,
		notifyOnCompleteRev: 1,
		maxCharsRev:         1,
		hotkeyLimit:         platform.HotkeyLimit(),
		settingsReqs:        make(chan settings.Request, 8),
		dl:                  models.NewManager(dir, version),
		openURL:             platform.OpenTarget,
		fallbackEditor:      platform.StartFallbackEditor,
		askYesNo:            platform.AskYesNo,
		dir:                 dir,
		conf:                conf,
		endpoint:            fmt.Sprintf("http://127.0.0.1:%d", conf.Port),
		paths:               paths,
		tray:                tray,
		limiter:             llamaserver.RestartLimiter{Max: 3, Window: 10 * time.Minute},
		readyTip:            "Clipboard Translator - ready (" + conf.Hotkey + ")",

		newHotkey:    platform.NewHotkey,
		showErr:      platform.ShowError,
		updates:      make(chan updateResult, 1),
		results:      make(chan result, 16),
		foreground:   platform.ForegroundWindow,
		paste:        pasteIntoForeground,
		capture:      platform.CaptureSelectedText,
		beginCapture: platform.BeginSelectionCapture,
		now:          time.Now,
	}
	tray.SetMenu(a.buildMenu)
	a.sup = &llamaserver.Supervisor{
		Model: paths.Model, Template: paths.Template,
		ContextTokens: conf.ContextTokens, Port: conf.Port, Endpoint: a.endpoint,
		LogOut:         serverLog,
		OnStarted:      a.serverStarted,
		StartupTimeout: time.Duration(conf.StartupTimeoutSec) * time.Second,
		PIDFile:        platform.ChildRecordPath(dir, conf.Port),
	}
	lastSec := time.Duration(-1)
	a.sup.UI = func(label string, elapsed time.Duration) bool {
		if a.pendingDuringLoad != nil {
			if pressed, profile, err := a.pollHotkeys(); err == nil && pressed {
				*a.pendingDuringLoad = profile
			}
			if a.hotkey == nil {
				platform.PumpMessages()
			}
		} else {
			platform.PumpMessages()
		}
		if a.quitRequested() {
			return true
		}
		a.serviceSettingsWhileBusy(label)
		if sec := elapsed / time.Second; sec != lastSec {
			lastSec = sec
			tray.SetStatus(fmt.Sprintf("Clipboard Translator - %s... (%ds)", label, sec))
		}
		return a.quitRequested()
	}
	return a
}

func (a *app) quitRequested() bool {
	if a.tray != nil && a.tray.QuitRequested() {
		return true
	}
	if _, err := os.Stat(filepath.Join(a.dir, "STOP")); err == nil {
		return true
	}
	if a.sig != nil {
		select {
		case <-a.sig:
			a.sigQuit = true
			return true
		default:
		}
	}
	return a.sigQuit
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
	defer a.beginServerWork()()
	if !models.IsCustomTemplate(a.conf.TemplateFile) {
		if m, known := models.ForFile(a.conf.ModelFile); known {
			if !models.VerifyFileSha256(a.sup.Template, m.TemplateSha256) {
				return fmt.Errorf("chat template %s does not match expected SHA-256 for %s", filepath.Base(a.sup.Template), m.Name)
			}
		}
	}
	err := a.sup.Start(a.paths.Server, a.conf.GPULayers, "loading model")
	if err == nil {
		a.activeBackend = config.CurrentBackend(a.conf.ServerFile)
		return nil
	}
	if errors.Is(err, llamaserver.ErrQuit) {
		return err
	}
	var dnf *llamaserver.DLLNotFoundError
	if errors.As(err, &dnf) && dnf.VCMissing {
		return err // every build needs the runtime; the CPU build cannot help
	}
	log.Printf("preferred backend failed: %v; see server.log", err)
	if a.paths.Server == a.paths.Fallback {
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
		rel, err := filepath.Rel(a.dir, a.sup.BinaryPath())
		if err == nil {
			b := config.CurrentBackend(rel)
			a.activeBackend = b
			return b
		}
	}
	if a.activeBackend != "" {
		return a.activeBackend
	}
	return config.CurrentBackend(a.conf.ServerFile)
}

// onFallback reports whether the server running now is the CPU fallback that
// startServer chose because the preferred engine did not start.
func (a *app) onFallback() bool {
	return a.sup.Running() && a.paths.Server != a.paths.Fallback && a.sup.BinaryPath() == a.paths.Fallback
}

// startFallback starts the CPU fallback build.
func (a *app) startFallback() error {
	defer a.beginServerWork()()
	if !models.IsCustomTemplate(a.conf.TemplateFile) {
		if m, known := models.ForFile(a.conf.ModelFile); known {
			if !models.VerifyFileSha256(a.sup.Template, m.TemplateSha256) {
				return fmt.Errorf("chat template %s does not match expected SHA-256 for %s", filepath.Base(a.sup.Template), m.Name)
			}
		}
	}
	if statErr := config.CheckFile(a.paths.Fallback); statErr != nil {
		return fmt.Errorf("CPU fallback unavailable: %w", statErr)
	}
	log.Print("falling back to CPU")
	if err := a.sup.Start(a.paths.Fallback, 0, "loading model (CPU)"); err != nil {
		if errors.Is(err, llamaserver.ErrQuit) {
			return err
		}
		var dnf *llamaserver.DLLNotFoundError
		if errors.As(err, &dnf) {
			return err // the same missing runtime stops both builds; say that, not "fallback failed"
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
		platform.PumpMessages()
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
	defer a.beginServerWork()()
	for {
		if !a.limiter.Allow(time.Now()) {
			a.failed(fmt.Errorf("The translation server keeps stopping (%d times in %s), so the app is quitting",
				a.limiter.Max, a.limiter.Window))
			return false
		}
		wait := llamaserver.RestartBackoff(a.limiter.Attempt())
		log.Printf("restarting the server in %s (attempt %d of %d)", wait, a.limiter.Attempt(), a.limiter.Max)
		a.cancelTransientStatus()
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
		if errors.Is(err, llamaserver.ErrQuit) {
			log.Print("quit requested during restart")
			return false
		}
		log.Printf("restart failed: %v", err)
	}
	log.Print("server restarted; ready")
	a.cancelTransientStatus()
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
	a.cancelTransientStatus()
	a.tray.SetStatus("Clipboard Translator - loading model...")
	defer a.sup.Stop()
	if err := a.startServer(); err != nil {
		if errors.Is(err, llamaserver.ErrQuit) {
			log.Print("quit requested during startup")
			return
		}
		a.failed(err)
		return
	}

	cb, err := platform.NewClipboard()
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
		parts = append(parts, p.Hotkey+" ("+p.Label()+")")
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
			lines = append(lines, fmt.Sprintf("%s (%s): %s", a.conf.Profiles[e.profile].Hotkey, a.conf.Profiles[e.profile].Label(), e.why))
		}
	}
	return strings.Join(lines, "\n")
}

// registerStartupHotkey registers the configured hotkey. If another program
// owns it, the app still starts (without a hotkey) so the user can pick a free
// one in Settings instead of having to edit config.json.
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
	if a.modelUnloaded {
		a.readyTip = "Clipboard Translator - idle (model unloaded); next hotkey loads the model"
		return
	}
	if s := a.hotkeySummary(); s != "" {
		a.readyTip = "Clipboard Translator - ready (" + s + ")"
	} else {
		a.readyTip = noHotkeyTip
	}
}

func (a *app) serve(cb platform.Clipboard) {
	a.cb = cb
	tray := a.tray
	tray.SetStatus(a.readyTip)
	if a.hasNoHotkey() {
		log.Printf("ready, but the hotkey %s could not be registered; waiting for the user to pick another", a.conf.Hotkey)
		tray.Notify("Clipboard Translator", fmt.Sprintf("Could not register the hotkey %s (another program may be using it). Open Settings... from the tray menu and choose another hotkey there.", a.conf.Hotkey), true)
	} else {
		log.Printf("ready: select text and press %s (or translate existing clipboard)", a.hotkeySummary())
		if !a.conf.QuickstartShown {
			a.showQuickstart() // first run only; the tray menu shows it again
		}
	}
	if f := a.extraFailures(); f != "" {
		tray.Notify("Clipboard Translator", "These hotkeys are not active:\n"+f, true)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	a.sig = sig
	defer func() { a.sig = nil }()
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
		if a.quitRequested() {
			log.Print("stop requested")
			a.cancelTranslation()
			return
		}
		// While a selection is being copied, check it every few milliseconds
		// instead of blocking this thread until the copy is done.
		var captureDue <-chan time.Time
		if a.capturing != nil {
			captureDue = time.After(a.capturing.task.Wait())
		}
		select {
		case <-captureDue:
			a.stepCapture()
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
			if a.quitRequested() {
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
			if a.quitRequested() {
				log.Print("stop requested")
				a.cancelTranslation()
				return
			}
			for {
				if a.quitRequested() {
					break
				}
				id, ok := tray.NextCommand()
				if !ok {
					break
				}
				a.handleCommand(id)
				if a.quitRequested() {
					break
				}
			}
			if a.quitRequested() { // e.g. Quit during an engine switch
				log.Print("stop requested")
				a.cancelTranslation()
				return
			}
			// An engine switch that failed twice leaves no server at all, and
			// Exited() is nil then; recover (or quit) rather than serve nothing.
			// An intentional unload instead waits for the next hotkey.
			if !a.sup.Running() && !a.modelUnloaded {
				log.Print("no translation server is running")
				if !a.recoverServer() {
					return
				}
				continue
			}
			a.tickTranslationStatus()
			a.tickTransientStatus()
			if pressed {
				a.startTranslation(pressedProfile)
			}
			a.tickIdleUnload()
		case req := <-a.settingsReqs:
			a.handleSettingsRequest(req)
		case st := <-a.dl.Events():
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
	if a.hotkey != nil {
		pressed, err = a.hotkey.Pressed()
		if err != nil {
			return false, 0, err
		}
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

func (a *app) timeNow() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *app) setTransientStatus(status string) {
	a.tray.SetStatus(status)
	a.statusResetAt = a.timeNow().Add(8 * time.Second)
}

func (a *app) cancelTransientStatus() {
	a.statusResetAt = time.Time{}
}

func (a *app) tickTransientStatus() {
	if a.translating {
		return
	}
	if !a.statusResetAt.IsZero() && !a.timeNow().Before(a.statusResetAt) {
		a.statusResetAt = time.Time{}
		a.tray.SetStatus(a.readyTip)
	}
}

func (a *app) shouldNotifySkip(reason config.SkipReason) bool {
	if reason != config.SkipTooLong {
		return false
	}
	now := a.timeNow()
	if a.lastSkipReason == reason && now.Sub(a.lastSkipNotify) < 15*time.Second {
		return false
	}
	a.lastSkipReason = reason
	a.lastSkipNotify = now
	return true
}

// abortTranslation cancels the request in flight and makes sure its result,
// should it still arrive, is ignored.
func (a *app) abortTranslation() {
	a.cancelTranslation()
	a.generation++
	a.translating = false
	if a.pendingDuringLoad != nil {
		*a.pendingDuringLoad = -1
	}
	a.resetIdleTimer()
	a.cancelTransientStatus()
	if a.capturing != nil {
		a.capturing = nil
		a.tray.SetStatus(a.readyTip) // do not leave "copying the selection..." behind
	}
}

// tickTranslationStatus keeps the tooltip's elapsed seconds current.
func (a *app) tickTranslationStatus() {
	if !a.translating {
		return
	}
	if sec := int(a.timeNow().Sub(a.translateStart) / time.Second); sec != a.lastTranslSec {
		a.lastTranslSec = sec
		a.tray.SetStatus(fmt.Sprintf("Clipboard Translator - translating... (%ds)", sec))
	}
}

// startTranslation reacts to the hotkey of profile index: it grabs the
// selection (or the clipboard) and translates it in the background. A new
// request cancels the older one and invalidates its result.
func (a *app) startTranslation(index int) {
	a.resetIdleTimer()
	profile := a.conf.Profiles[index]
	win := a.foreground()
	a.abortTranslation()
	if profile.InputMode() == config.InputClipboard {
		// The text is already on the clipboard: no Ctrl+C, no waiting for a copy,
		// and never a fresh selection (so no auto-paste).
		source, err := a.cb.Read()
		a.translateCaptured(profile, win, source, false, err)
		return
	}
	if a.beginCapture != nil {
		if task := a.beginCapture(a.cb, win); task != nil {
			a.capturing = &pendingCapture{task: task, profile: profile, win: win, started: a.timeNow()}
			a.stepCapture() // a copy that is already done goes on at once
			return
		}
	}
	source, fromSelection, err := a.capture(a.cb, win)
	a.translateCaptured(profile, win, source, fromSelection, err)
}

// stepCapture moves the pending selection copy on, and translates once it is done.
func (a *app) stepCapture() {
	p := a.capturing
	if p == nil {
		return
	}
	done, source, fromSelection, err := p.task.Step()
	if !done {
		if !p.noted && a.timeNow().Sub(p.started) >= copyingNoteAfter {
			p.noted = true
			a.tray.SetStatus("Clipboard Translator - copying the selection...")
		}
		return
	}
	a.capturing = nil
	a.translateCaptured(p.profile, p.win, source, fromSelection, err)
}

// translateCaptured checks the captured text and translates it in the background.
func (a *app) translateCaptured(profile config.Profile, win uintptr, source string, fromSelection bool, err error) {
	capturedAt := a.timeNow()
	if err != nil {
		if profile.InputMode() == config.InputClipboard {
			log.Printf("cannot read the clipboard: %v", err)
		} else {
			log.Printf("cannot capture selection: %v", err)
		}
		a.cancelTransientStatus()
		a.tray.SetStatus(a.readyTip)
		if errors.Is(err, platform.ErrCaptureModifiersHeld) || errors.Is(err, platform.ErrCaptureFocusChanged) {
			// Say so: translating the old clipboard text instead would be a silent wrong answer.
			a.tray.Notify("Clipboard Translator", "Nothing was translated: "+err.Error()+". Release the keys and press the hotkey again.", true)
		}
		return
	}
	reason, direction := profile.Check(source, a.conf.MaxChars)
	if reason != config.SkipNone {
		count := utf8.RuneCountInString(source)
		msg := config.SkipMessage(reason, count, a.conf.MaxChars, profile.Label())
		log.Printf("skipped (%s): %s", reason, msg)
		a.setTransientStatus(config.SkipStatus(reason, profile.Label()))
		if a.shouldNotifySkip(reason) {
			a.tray.Notify("Clipboard Translator", msg, true)
		}
		return
	}
	if a.modelUnloaded {
		a.tray.SetStatus("Clipboard Translator - loading model...")
		a.translating = true
		initialGen := a.generation
		pendingProfile := -1
		a.pendingDuringLoad = &pendingProfile
		err := a.startServer()
		a.pendingDuringLoad = nil
		if err != nil {
			a.translating = false
			a.modelUnloaded = true
			a.updateReadyTip()
			a.refreshStatus()
			if !errors.Is(err, llamaserver.ErrQuit) {
				a.failed(err)
			}
			return
		}
		for {
			if a.quitRequested() {
				break
			}
			id, ok := a.tray.NextCommand()
			if !ok {
				break
			}
			a.handleCommand(id)
		}
		if a.quitRequested() || !a.translating || a.generation != initialGen {
			if a.translating {
				a.abortTranslation()
			}
			return
		}
		if pendingProfile >= 0 {
			a.abortTranslation()
			a.startTranslation(pendingProfile)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.conf.TranslationTimeoutSec)*time.Second)
	a.cancelCurrent = cancel
	a.cancelTransientStatus()
	a.translating, a.translateStart, a.lastTranslSec = true, a.timeNow(), 0
	g := a.generation
	key := a.sup.APIKey() // read here: the supervisor belongs to this thread
	from := direction.Source
	if from == "" {
		from = "auto"
	}
	log.Printf("translating %s -> %s", from, direction.Target)
	a.tray.SetStatus("Clipboard Translator - translating... (0s)")
	outputMode := profile.OutputMode()
	maxTokens := a.conf.MaxTokens
	go func() {
		translated, err := llamaserver.Translate(ctx, a.endpoint, key, source, direction, maxTokens)
		select {
		case a.results <- result{original: source, translated: translated, generation: g, err: err,
			direction: direction, window: win, fromSelection: fromSelection, output: outputMode,
			capturedAt: capturedAt}:
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
	a.resetIdleTimer()
	if res.err != nil {
		if !errors.Is(res.err, context.Canceled) {
			log.Printf("translation failed: %v", res.err)
			msg := "See translator.log for details."
			if errors.Is(res.err, llamaserver.ErrTruncated) {
				msg = "The translation was cut off at the token limit, so no translation was put on the clipboard. Select less text, or raise max_tokens / context_tokens in config.json."
			}
			a.tray.Notify("Translation failed", msg, true)
		}
		a.cancelTransientStatus()
		a.tray.SetStatus(a.readyTip)
		return
	}
	if res.translated == res.original {
		log.Print("translation unchanged; clipboard left intact")
		a.setTransientStatus("Clipboard Translator - translation is the same as the source; the clipboard still holds the source")
		return
	}
	// A sequence number is not a reliable test for a text change:
	// apps can update clipboard formats without changing the text.
	// Compare text AND replace under a single OpenClipboard lock.
	replacement := res.translated
	if res.output == config.OutputBilingual {
		replacement = formatBilingual(res.original, res.translated)
	}
	applied, err := applyTranslation(a.cb, res.original, replacement)
	if err != nil {
		log.Printf("clipboard check/write failed: %v", err)
		a.tray.SetStatus(a.readyTip)
		a.tray.Notify("Clipboard Translator", "Could not write translation to the clipboard: "+err.Error(), true)
		return
	}
	if !applied {
		log.Print("clipboard text changed during translation; result discarded (new clipboard preserved)")
		current, readErr := a.cb.Read()
		if readErr != nil {
			log.Printf("clipboard change diagnostics: could not read current clipboard: %v", readErr)
		} else {
			var elapsed time.Duration
			if !res.capturedAt.IsZero() {
				elapsed = a.timeNow().Sub(res.capturedAt)
			}
			log.Printf("clipboard change diagnostics: %s", platform.ClassifyClipboardDiff(res.original, current, elapsed))
		}
		a.tray.SetStatus(a.readyTip)
		a.tray.Notify("Clipboard Translator", "The clipboard changed while translating, so the result was dropped and the new clipboard text was kept.", false)
		return
	}
	a.hist.Add(historyEntry{Source: res.original, Translated: res.translated, Direction: res.direction})
	action := decidePaste(a.conf.AutoPaste, res.fromSelection, res.window, a.foreground())
	switch action {
	case pasteNow:
		// decidePaste just confirmed the window is still res.window; that is the
		// target pasteWithFocus must still find in front right before Ctrl+V.
		if err := a.paste(res.window); err != nil {
			log.Printf("auto-paste failed: %v", err)
			a.tray.SetStatus("Clipboard Translator - translation ready (" + platform.PasteShortcut() + ")")
			a.tray.Notify("Clipboard Translator", "Translation ready, but it could not be pasted ("+err.Error()+"). Press "+platform.PasteShortcut()+".", true)
			return
		}
		log.Print("translation pasted")
		a.tray.SetStatus("Clipboard Translator - translation pasted")
	case pasteWindowChanged:
		log.Print("translation ready; the active window changed, so it was not pasted")
		a.tray.Notify("Clipboard Translator", "Translation ready in the clipboard. The active window changed, so it was not pasted. Press "+platform.PasteShortcut()+".", false)
		a.tray.SetStatus("Clipboard Translator - translation ready (" + platform.PasteShortcut() + ")")
	default:
		log.Print("translation ready in clipboard (", platform.PasteShortcut(), " to paste)")
		a.tray.SetStatus("Clipboard Translator - translation ready (" + platform.PasteShortcut() + ")")
	}
	if a.conf.NotifyOnComplete && action == pasteNone {
		title, msg := platform.CompletionNotification()
		a.tray.Notify(title, msg, false)
	}
}

// pasteIntoForeground waits for the hotkey's modifiers to be released and then
// types Ctrl+V into target, but only if target is still the window in front.
func pasteIntoForeground(target uintptr) error {
	return pasteWithFocus(target, platform.ForegroundWindow, platform.WaitModifiersReleased, platform.SendPaste)
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
