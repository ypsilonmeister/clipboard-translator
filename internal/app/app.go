package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/textstruct"
	"github.com/ypsilonmeister/clipboard-translator/internal/update"
)

// app ties the tray, the llama-server supervisor, the clipboard and the hotkey
// together. Everything here runs on the one OS thread that owns the tray window
// and the hotkey; goroutines only talk to it through channels.
type app struct {
	dir        string
	conf       config.Config
	endpoint   string
	paths      config.Paths
	tray       platform.Tray
	cursorHint platform.CursorHint
	sup        *llamaserver.Supervisor
	limiter    llamaserver.RestartLimiter
	readyTip   string

	modelUnloaded   bool // intentional stop; hotkeys load it again
	serverWork      int  // nested start/switch/recovery calls on the UI thread
	settingsServing bool
	idleSince       time.Time
	idleUnloadRev   int

	pendingDuringLoad *int
	deferredCmds      []int // menu commands picked during a slow call, for the main loop
	sig               chan os.Signal
	sigQuit           bool

	activeBackend string // last known working backend ("cpu", "cuda", ...)
	// fallbackPinned is set once the preferred engine failed and the CPU build
	// took over: reloads then start the CPU build directly instead of paying for
	// the failing engine again. Cleared when the user picks an engine or the
	// model changes. fallbackNotifiedFile is the configured server_file whose
	// failure was announced.
	// Both belong to the UI thread.
	fallbackPinned       bool
	fallbackNotifiedFile string

	hotkey    platform.Hotkey                            // profile 0
	newHotkey func(spec string) (platform.Hotkey, error) // newHotkey, replaceable in tests
	showErr   func(msg string)                           // showError, replaceable in tests
	// extras are the hotkeys of profiles 1.., in order; hk is nil when that
	// profile's hotkey could not be registered (the reason is in why).
	extras      []extraHotkey
	hotkeyLimit int // how many hotkeys this OS build can register, hotkeyLimit() in production

	settings       *settings.Server // the settings page, while it is open
	apiContext     context.Context
	apiReply       chan settings.Reply
	apiServer      *http.Server
	apiAuth        *translateAPIAuth
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
	pings    chan struct{}
	checking bool
	latest   update.ReleaseInfo // newest release seen by "Check for updates"

	cb                     platform.Clipboard
	results                chan result
	progress               chan translationProgress
	chunkIndex             int
	chunkTotal             int
	telephoneRound         int
	telephoneTotal         int
	generation             uint64 // bumped by every new request; older results are ignored
	cancelCurrent          context.CancelFunc
	translating            bool
	translateStart         time.Time
	lastTranslSec          int
	hist                   history
	foreground             func() uintptr                                                 // foregroundWindow, replaceable in tests
	paste                  func(target uintptr) error                                     // pasteIntoForeground, replaceable in tests
	capture                func(cb platform.Clipboard, win uintptr) (string, bool, error) // captureSelectedText, replaceable in tests
	beginCapture           func(cb platform.Clipboard, win uintptr) platform.CaptureTask  // beginSelectionCapture; nil (or a nil task) uses capture
	capturing              *pendingCapture                                                // a selection copy the serve loop is still waiting for
	pendingRead            *pendingClipboardRead
	pendingResult          *result
	pendingResultDeadline  time.Time
	pendingHistory         *string
	pendingHistoryDeadline time.Time
	noCopyNotified         bool // the "no selection was copied" notice was shown this run (UI thread only)
	elevatedNotified       bool // the "administrator window" notice was shown this run (UI thread only)
	desktopStamp           string

	trayState        platform.TrayState
	trayStateResetAt time.Time

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
		cursorHint:          platform.NewCursorHint(conf.ShowCursorHint),
		limiter:             llamaserver.RestartLimiter{Max: 3, Window: 10 * time.Minute},
		readyTip:            "Clipboard Translator - ready (" + conf.Hotkey + ")",

		newHotkey:    platform.NewHotkey,
		showErr:      platform.ShowError,
		updates:      make(chan updateResult, 1),
		pings:        make(chan struct{}, 1),
		results:      make(chan result, 16),
		progress:     make(chan translationProgress, 16),
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
				if a.conf.ShowCursorHint {
					a.cursorHint.Show()
				}
			}
		}
		// noHotkey does not pump messages, so always service the UI here.
		platform.PumpMessages()
		if a.quitRequested() {
			return true
		}
		a.tickAPIContext()
		a.serviceTrayWhileBusy()
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
	if a.fallbackPinned && a.paths.Server != a.paths.Fallback {
		// The preferred engine already failed this session; do not wait for it again.
		err := a.startFallback()
		if err != nil {
			a.fallbackPinned = false // the CPU build is broken too: let the next start try everything
		}
		return err
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
	if err := a.startFallback(); err != nil {
		return err
	}
	a.fallbackPinned = true
	if a.fallbackNotifiedFile != a.conf.ServerFile {
		a.fallbackNotifiedFile = a.conf.ServerFile
		label := backendLabel(config.CurrentBackend(a.conf.ServerFile))
		a.tray.Notify("Clipboard Translator", fmt.Sprintf("The %s engine did not start. Using the CPU engine, which is slower. Open Settings to retry it or to keep the CPU engine.", label), true)
	}
	return nil
}

// clearFallback forgets a remembered fallback: the preferred engine is tried
// (and announced) again at the next start.
func (a *app) clearFallback() {
	a.fallbackPinned = false
	a.fallbackNotifiedFile = ""
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

// fallbackInUse reports whether the CPU build stands in for the configured
// engine: it runs now, or it is what the next load starts. The two differ
// after a model switch on the fallback, which runs the CPU build but lets the
// next load try the preferred engine again, and while the model is unloaded.
func (a *app) fallbackInUse() bool {
	return a.fallbackPinned || a.onFallback()
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
		if a.quitRequested() {
			return true
		}
		a.tickAPIContext()
		a.serviceTrayWhileBusy()
		a.serviceSettingsWhileBusy("restarting the translation server")
		if a.quitRequested() {
			return true
		}
	}
	return false
}

// nextCommand returns the next menu command for the main loop: first the ones
// serviceTrayWhileBusy put aside during a slow call, then what the tray holds.
func (a *app) nextCommand() (int, bool) {
	if len(a.deferredCmds) > 0 {
		id := a.deferredCmds[0]
		a.deferredCmds = a.deferredCmds[1:]
		return id, true
	}
	return a.tray.NextCommand()
}

// safeWhileBusy reports whether a menu command may run inside a slow call
// (a model load, the wait before a restart). Those commands neither start nor
// stop a server and do not begin a translation, so they cannot nest another
// slow call in this one. "Cancel translation" only bumps the generation; the
// caller that is loading the model for that translation sees it afterwards.
//
// Settings and the quick start guide describe the hotkeys, which the first
// model load at startup has not registered yet (a.hotkey is nil until then):
// there they wait, or the guide would promise a hotkey that may turn out taken.
func (a *app) safeWhileBusy(id int) bool {
	switch id {
	case cmdCancel, cmdOpenConfig, cmdPlamoLicense, cmdHistoryClear:
		return true
	case cmdSettings, cmdQuickstart:
		return a.hotkey != nil
	}
	return false
}

// serviceTrayWhileBusy runs the menu commands the user picked while the main
// loop is inside a slow call: PumpMessages lets the menu open there, so its
// items must not sit unanswered until the model has loaded. Commands that are
// not safe to run now keep their order and run when the main loop is back, and
// so does everything picked after one of them (a "Clear history" must not
// overtake the history copy picked before it). Only "Cancel translation" never
// waits: it is the one command whose point is lost by waiting.
func (a *app) serviceTrayWhileBusy() {
	for {
		if a.quitRequested() {
			return
		}
		id, ok := a.tray.NextCommand()
		if !ok {
			return
		}
		if id == cmdCancel || (len(a.deferredCmds) == 0 && a.safeWhileBusy(id)) {
			a.handleCommand(id)
		} else {
			a.deferredCmds = append(a.deferredCmds, id)
		}
	}
}

// restartServer brings the server back after it died. It gives up (and reports
// false) when the user quits or the server keeps dying.
func (a *app) restartServer() bool {
	defer a.beginServerWork()()
	fellBack := false // this restart announced a CPU fallback
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
		announced := a.fallbackNotifiedFile
		err := a.startServer()
		fellBack = fellBack || a.fallbackNotifiedFile != announced
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
	if !fellBack {
		// The fallback notice already says that a server runs, and on which
		// engine; "back" right after it would take its place on the screen.
		a.tray.Notify("Clipboard Translator", "The translation server is back. Ready.", false)
	}
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
	defer a.sup.Stop()
	defer a.cursorHint.Close()
	if err := a.startProfileEngine(); err != nil {
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
	a.cb = cb
	a.registerStartupHotkey()
	defer a.closeSettings()
	if err := a.startTranslateAPI(); err != nil {
		log.Printf("translation endpoint: %v", err)
		a.tray.Notify("Clipboard Translator", "Could not start translation endpoint: "+err.Error(), true)
	}
	defer a.stopTranslateAPI()
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

func (a *app) startProfileEngine() error {
	if !a.conf.NeedsTranslation() {
		a.modelUnloaded = true
		return nil
	}
	a.tray.SetStatus("Clipboard Translator - loading model...")
	return a.startServer()
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
// Portal shortcuts may differ from the requested key in config.json.
func (a *app) hotkeyLabel(index int) string {
	var hk platform.Hotkey
	if index == 0 {
		hk = a.hotkey
	} else {
		for _, e := range a.extras {
			if e.profile == index {
				hk = e.hk
				break
			}
		}
	}
	if described, ok := hk.(interface{ TriggerDescription() string }); ok {
		return described.TriggerDescription()
	}
	return a.conf.Profiles[index].Hotkey
}

func (a *app) hotkeySummary() string {
	var parts []string
	add := func(i int) {
		p := a.conf.Profiles[i]
		label := a.hotkeyLabel(i)
		if label == "" {
			return
		}
		if len(a.conf.Profiles) == 1 {
			parts = append(parts, label)
			return
		}
		parts = append(parts, label+" ("+p.Label()+")")
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
	if s := a.desktopStatus(); s.Wayland && !s.Ready {
		a.readyTip = "Clipboard Translator - " + s.Message
		return
	}
	if a.chromeOSProfile() {
		a.readyTip = "Clipboard Translator - ready"
		if a.modelUnloaded {
			a.readyTip = "Clipboard Translator - idle (model unloaded); next translation loads the model"
		}
		return
	}
	if a.modelUnloaded && a.conf.NeedsTranslation() {
		a.readyTip = "Clipboard Translator - idle (model unloaded); next hotkey loads the model"
		return
	}
	if s := a.hotkeySummary(); s != "" {
		a.readyTip = "Clipboard Translator - ready (" + s + ")"
		// Windows reserves one of szTip's 128 UTF-16 units for the terminator.
		if len(utf16.Encode([]rune(a.readyTip))) > 127 {
			count := 0
			if !a.hasNoHotkey() && a.hotkeyLabel(0) != "" {
				count++
			}
			for _, e := range a.extras {
				if e.hk != nil && a.hotkeyLabel(e.profile) != "" {
					count++
				}
			}
			label := "hotkeys"
			if count == 1 {
				label = "hotkey"
			}
			a.readyTip = fmt.Sprintf("Clipboard Translator - ready (%d %s)", count, label)
		}
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
		log.Print(readyMessage(a.hotkeySummary()))
		// A second balloon here could replace the startup fallback notice.
		if a.fallbackNotifiedFile == "" && !a.conf.QuickstartShown && !a.desktopStatus().Wayland {
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
			if a.needsLeadingPump() {
				platform.PumpMessages()
			}
			a.tickDesktop()
			a.tickAPIContext()
			a.tickClipboard()
			if a.quitRequested() {
				log.Print("stop requested")
				a.cancelTranslation()
				return
			}
			pressed, pressedProfile, err := a.pollHotkeys()
			if err != nil {
				if !a.desktopStatus().Wayland {
					log.Printf("hotkey error: %v", err)
				}
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
				id, ok := a.nextCommand()
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
		case p := <-a.progress:
			a.onProgress(p)
		case res := <-a.results:
			a.handleResult(res)
		case <-a.pings:
			a.showAlreadyRunning()
		}
	}
}

// needsLeadingPump reports whether the tick must service the UI before it asks
// the hotkeys. A working hotkey pumps messages in its own Pressed, and a pump
// before it would swallow the press: PumpMessages drops WM_HOTKEY on Windows and
// returns the press on macOS, where Pressed returns that same value. So pump here
// only when no hotkey will (none registered), or on Wayland, where the portal
// session is serviced by this call.
func (a *app) needsLeadingPump() bool {
	if a.desktopStatus().Wayland {
		return true
	}
	if !a.hasNoHotkey() {
		return false
	}
	for _, e := range a.extras {
		if e.hk != nil {
			return false
		}
	}
	return true
}

// pollHotkeys asks every registered hotkey whether it fired and returns the
// profile to run. Every hotkey must be asked each tick: on Windows the first
// call drains the message queue and the others read what it recorded. If two
// fired in one tick the later profile wins, as a newer request replaces an older
// one. An error from profile 0 is returned; one from another profile is logged.
func (a *app) pollHotkeys() (pressed bool, profile int, err error) {
	if a.hotkey != nil {
		pressed, err = a.hotkey.Pressed()
	}
	for _, e := range a.extras {
		if e.hk == nil {
			continue
		}
		if p, err := e.hk.Pressed(); err != nil {
			if !a.desktopStatus().Wayland {
				log.Printf("hotkey error (profile %d): %v", e.profile+1, err)
			}
		} else if p {
			pressed, profile = true, e.profile
		}
	}
	return pressed, profile, err
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

const (
	trayDoneDuration    = 3 * time.Second
	trayProblemDuration = 5 * time.Second
)

func (a *app) setTrayState(state platform.TrayState) {
	if a.trayState == state {
		return
	}
	a.trayState = state
	if a.tray != nil {
		a.tray.SetState(state)
	}
}

func (a *app) setTrayStateWithTimeout(state platform.TrayState, d time.Duration) {
	a.trayStateResetAt = a.timeNow().Add(d)
	a.setTrayState(state)
}

func (a *app) tickTrayState() {
	if a.translating || a.capturing != nil || a.pendingRead != nil {
		return
	}
	if !a.trayStateResetAt.IsZero() && !a.timeNow().Before(a.trayStateResetAt) {
		a.trayStateResetAt = time.Time{}
		a.setTrayState(platform.TrayIdle)
	}
}

func (a *app) setTransientStatus(status string) {
	a.tray.SetStatus(status)
	a.statusResetAt = a.timeNow().Add(8 * time.Second)
	a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
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
	a.tickTrayState()
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
	a.cursorHint.Hide()
	a.resetTranslation()
}

func (a *app) resetTranslation() {
	a.finishAPI("", context.Canceled)
	if c, ok := a.cb.(interface{ CancelPendingRead() }); ok {
		c.CancelPendingRead()
	}
	a.cancelTranslation()
	a.generation++
	a.pendingRead = nil
	a.pendingResult = nil
	a.pendingResultDeadline = time.Time{}
	a.pendingHistory = nil
	a.translating = false
	a.chunkIndex = 0
	a.chunkTotal = 0
	a.telephoneRound = 0
	a.telephoneTotal = 0
	if a.pendingDuringLoad != nil {
		*a.pendingDuringLoad = -1
	}
	a.resetIdleTimer()
	a.cancelTransientStatus()
	if a.trayState == platform.TrayBusy {
		a.trayStateResetAt = a.timeNow()
	}
	if a.capturing != nil {
		a.capturing = nil
		a.tray.SetStatus(a.readyTip) // do not leave "copying the selection..." behind
	}
}

func (a *app) onProgress(p translationProgress) {
	if p.generation != a.generation || !a.translating {
		return
	}
	if p.telephoneTotal > 0 {
		a.telephoneRound = p.telephoneRound
		a.telephoneTotal = p.telephoneTotal
	}
	a.chunkIndex = p.chunkIndex
	a.chunkTotal = p.totalChunks
	a.updateTranslationStatus()
}

func (a *app) updateTranslationStatus() {
	if !a.translating {
		return
	}
	if a.telephoneTotal > 0 {
		a.tray.SetStatus(fmt.Sprintf("Clipboard Translator - round %d of %d... (%ds)", a.telephoneRound, a.telephoneTotal, a.lastTranslSec))
		return
	}
	if a.chunkTotal > 1 {
		a.tray.SetStatus(fmt.Sprintf("Clipboard Translator - translating chunk %d of %d... (%ds)", a.chunkIndex, a.chunkTotal, a.lastTranslSec))
	} else {
		a.tray.SetStatus(fmt.Sprintf("Clipboard Translator - translating... (%ds)", a.lastTranslSec))
	}
}

// tickTranslationStatus keeps the tooltip's elapsed seconds current.
func (a *app) tickTranslationStatus() {
	if !a.translating {
		return
	}
	if sec := int(a.timeNow().Sub(a.translateStart) / time.Second); sec != a.lastTranslSec {
		a.lastTranslSec = sec
		a.updateTranslationStatus()
	}
}

// startTranslation reacts to the hotkey of profile index: it grabs the
// selection (or the clipboard) and translates it in the background. A new
// request cancels the older one and invalidates its result.
func (a *app) startTranslation(index int) {
	a.startHotkeyTranslation(index, false)
}

func (a *app) startHotkeyTranslation(index int, queuedDuringLoad bool) {
	a.resetIdleTimer()
	profile := a.conf.Profiles[index]
	win := a.foreground()
	a.resetTranslation()
	a.trayStateResetAt = time.Time{}
	a.setTrayState(platform.TrayBusy)
	if a.conf.ShowCursorHint && !queuedDuringLoad {
		a.cursorHint.Show()
	}
	if profile.InputMode() == config.InputClipboard {
		// The text is already on the clipboard: no Ctrl+C, no waiting for a copy,
		// and never a fresh selection (so no auto-paste).
		source, err := a.cb.Read()
		if errors.Is(err, platform.ErrClipboardPending) {
			a.pendingRead = &pendingClipboardRead{profile: profile, win: win, deadline: a.timeNow().Add(8 * time.Second)}
			a.tray.SetStatus("Clipboard Translator - reading the clipboard...")
			return
		}
		a.translateCaptured(profile, win, source, false, platform.FallbackNone, err)
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
	a.translateCaptured(profile, win, source, fromSelection, platform.FallbackNone, err)
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
	a.translateCaptured(p.profile, p.win, source, fromSelection, p.task.Fallback(), err)
}

// translateCaptured checks the captured text and translates it in the background.
//
// fallback says the text is what was already on the clipboard because no
// selection could be copied (see platform.CaptureFallback). It is only ever set
// by a selection capture, so an input "clipboard" profile is never affected.
func (a *app) translateCaptured(profile config.Profile, win uintptr, source string, fromSelection bool, fallback platform.CaptureFallback, err error) {
	capturedAt := a.timeNow()
	switch fallback {
	case platform.FallbackNoCopy:
		log.Print("no selection was copied; using the text already on the clipboard")
	case platform.FallbackElevated:
		// Said now, whatever becomes of the clipboard text: the advice (copy
		// yourself) is what the user needs, also when there is nothing to translate.
		a.noteElevatedWindow()
	}
	if err != nil {
		a.cursorHint.Hide()
		if profile.InputMode() == config.InputClipboard {
			log.Printf("cannot read the clipboard: %v", err)
		} else {
			log.Printf("cannot capture selection: %v", err)
		}
		a.cancelTransientStatus()
		a.tray.SetStatus(a.readyTip)
		if fallback == platform.FallbackElevated {
			// The notice shows once per run; the tooltip says it every time.
			a.setTransientStatus("Clipboard Translator - cannot copy from an administrator window; press Ctrl+C, then the hotkey")
		}
		if a.desktopStatus().Wayland || errors.Is(err, platform.ErrCaptureModifiersHeld) || errors.Is(err, platform.ErrCaptureFocusChanged) {
			// Say so: translating the old clipboard text instead would be a silent wrong answer.
			msg := "Nothing was translated: " + err.Error() + "."
			if !a.desktopStatus().Manual { // manual mode has no hotkey to press again
				msg += " Release the keys and press the hotkey again."
			}
			a.tray.Notify("Clipboard Translator", msg, true)
		}
		a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		return
	}
	reason, direction := profile.Check(source, a.conf.MaxChars)
	if reason != config.SkipNone {
		a.cursorHint.Hide()
		count := utf8.RuneCountInString(source)
		msg := config.SkipMessage(reason, count, a.conf.MaxChars, profile.Label())
		log.Printf("skipped (%s): %s", reason, msg)
		a.setTransientStatus(config.SkipStatus(reason, profile.Label()))
		if a.shouldNotifySkip(reason) {
			a.tray.Notify("Clipboard Translator", msg, true)
		}
		a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		return
	}
	if profile.ActionMode() == config.ActionTidy {
		a.cancelTransientStatus()
		a.translating = true
		a.handleResult(result{original: source, translated: textstruct.Tidy(source, profile.Rules, profile.PunctStyle),
			generation: a.generation, window: win, fromSelection: fromSelection,
			fallback: fallback, capturedAt: capturedAt, action: config.ActionTidy})
		return
	}
	if a.modelUnloaded {
		a.tray.SetStatus("Clipboard Translator - loading model...")
		a.translating = true
		initialGen := a.generation
		pendingProfile := -1
		a.pendingDuringLoad = &pendingProfile
		fromAPI := a.apiReply != nil // a cancel during the load clears apiReply
		err := a.startServer()
		a.pendingDuringLoad = nil
		if err != nil {
			a.cursorHint.Hide()
			a.translating = false
			a.modelUnloaded = true
			a.updateReadyTip()
			a.refreshStatus()
			// Answer the HTTP caller first, and never with a modal box: the app
			// keeps running here, so a box would hold /translate until someone
			// dismissed it and come back at every retry.
			apiErr := err
			if a.apiContext != nil && a.apiContext.Err() != nil {
				apiErr = a.apiContext.Err()
			}
			a.finishAPI("", apiErr)
			if !errors.Is(err, llamaserver.ErrQuit) {
				log.Printf("could not load the model again: %v", err)
				if !fromAPI {
					// The pointer to the logs comes first: a notification may cut a long reason short.
					a.tray.Notify("Clipboard Translator", "Nothing was translated: the model could not be loaded again. See server.log and translator.log in the app folder. "+err.Error(), true)
				}
			}
			a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
			return
		}
		// A cancel during the load (menu, API caller gone, endpoint switched
		// off) has already bumped the generation and cleared translating.
		loadGen := a.generation
		for {
			if a.quitRequested() {
				break
			}
			id, ok := a.nextCommand()
			if !ok {
				break
			}
			a.handleCommand(id)
		}
		if a.quitRequested() {
			if a.translating {
				a.abortTranslation()
			} else {
				a.cursorHint.Hide()
			}
			return
		}
		if a.generation != loadGen {
			// A command that waited for the load replaced or cancelled this
			// request; what it started (Translate clipboard) is the newest one.
			if a.capturing == nil && a.pendingRead == nil {
				a.refreshStatus() // not "loading model..." any more, unless it translates
			}
			return
		}
		if pendingProfile >= 0 {
			// A hotkey pressed during the load is the newest request, also
			// after a cancel (abortTranslation resets pendingProfile).
			// Its hint is already anchored at the position of that press.
			a.startHotkeyTranslation(pendingProfile, true)
			return
		}
		if !a.translating || loadGen != initialGen {
			if a.translating {
				a.abortTranslation()
			}
			// Not "loading model..." any more, which the load kept writing
			// over the menu's "cancelled".
			if a.statusResetAt.IsZero() {
				a.refreshStatus()
			} else {
				a.setTransientStatus(cancelledStatus)
			}
			a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
			return
		}
	}
	parent := context.Background()
	if a.apiContext != nil {
		parent = a.apiContext
	}
	// translation_timeout_seconds limits each llama-server request, not the
	// whole job (#173); the job ends when it is done, aborted or superseded.
	ctx, cancel := context.WithCancel(parent)
	timeout := time.Duration(a.conf.TranslationTimeoutSec) * time.Second
	a.cancelCurrent = cancel
	a.cancelTransientStatus()
	a.translating, a.translateStart, a.lastTranslSec = true, a.timeNow(), 0
	action := profile.ActionMode()
	if action == config.ActionTelephone {
		a.telephoneRound = 1
		a.telephoneTotal = 10
		a.chunkIndex = 0
		a.chunkTotal = 0
	} else {
		a.chunkIndex, a.chunkTotal = 1, 0
	}
	g := a.generation
	key := a.sup.APIKey() // read here: the supervisor belongs to this thread
	from := direction.Source
	if from == "" {
		from = "auto"
	}
	log.Printf("translating %s -> %s", from, direction.Target)
	a.updateTranslationStatus()
	outputMode := profile.OutputMode()
	maxTokens := a.conf.MaxTokens
	budget := chunkBudget(a.conf.MaxChars) // configuration belongs to the UI thread
	promptText := source
	if profile.CleanupNewlinesMode() == config.CleanupNewlinesAuto {
		promptText = textstruct.CleanSpuriousNewlines(source, direction.Source)
	}
	go func() {
		var translated string
		var err error
		if action == config.ActionTelephone {
			translated, err = a.translateTelephone(ctx, g, key, promptText, direction, maxTokens, budget, timeout)
		} else {
			translated, err = a.translatePrompt(ctx, g, key, promptText, direction, maxTokens, budget, timeout)
		}
		select {
		case a.results <- result{original: source, translated: translated, generation: g, err: err,
			direction: direction, window: win, fromSelection: fromSelection, fallback: fallback, output: outputMode,
			capturedAt: capturedAt, action: action}:
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
	noun, verb, ongoing, activity := "translation", "translated", "translating", "translation"
	if res.action == config.ActionTidy {
		noun, verb, ongoing, activity = "tidied text", "tidied", "tidying", "tidying"
	}
	a.translating = false
	a.chunkIndex = 0
	a.chunkTotal = 0
	a.telephoneRound = 0
	a.telephoneTotal = 0
	a.resetIdleTimer()
	if a.apiReply != nil {
		text := res.translated
		if res.output == config.OutputBilingual && res.action != config.ActionTelephone && res.err == nil {
			text = formatBilingual(res.original, text)
		}
		a.finishAPI(text, res.err)
		a.refreshStatus()
		if res.err != nil {
			a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		} else {
			a.setTrayState(platform.TrayIdle)
		}
		return
	}
	if res.err != nil {
		a.cursorHint.Hide()
		if !errors.Is(res.err, context.Canceled) {
			log.Printf("translation failed: %v", res.err)
			msg := translationFailureMessage(res.err)
			a.tray.Notify("Translation failed", msg, true)
		}
		a.cancelTransientStatus()
		a.tray.SetStatus(a.readyTip)
		a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		return
	}
	if res.action != config.ActionTelephone && res.translated == res.original {
		a.cursorHint.Hide()
		log.Print(noun + " unchanged; clipboard left intact")
		a.setTransientStatus("Clipboard Translator - " + noun + " is the same as the source; the clipboard still holds the source")
		if res.action == config.ActionTidy {
			a.setTrayState(platform.TrayIdle)
		} else {
			a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		}
		return
	}
	// A sequence number is not a reliable test for a text change:
	// apps can update clipboard formats without changing the text.
	// Compare text AND replace under a single OpenClipboard lock.
	replacement := res.translated
	if res.output == config.OutputBilingual && res.action != config.ActionTelephone {
		replacement = formatBilingual(res.original, res.translated)
	}
	applied, err := applyTranslation(a.cb, res.original, replacement)
	if errors.Is(err, platform.ErrClipboardPending) {
		if a.pendingResult == nil {
			a.pendingResultDeadline = a.timeNow().Add(8 * time.Second)
		}
		if !a.timeNow().Before(a.pendingResultDeadline) {
			a.cursorHint.Hide()
			a.pendingResult = nil
			a.hist.Add(historyEntry{Source: res.original, Translated: res.translated, Direction: res.direction})
			a.tray.SetStatus(a.readyTip)
			a.tray.Notify("Clipboard Translator", "Clipboard transfer timed out. The "+noun+" is available in Recent translations.", true)
			a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
			return
		}
		a.pendingResult = &res
		a.tray.SetStatus("Clipboard Translator - writing the " + noun + " to the clipboard...")
		a.setTrayState(platform.TrayBusy)
		return
	}
	a.cursorHint.Hide()
	a.pendingResult = nil
	if err != nil {
		a.hist.Add(historyEntry{Source: res.original, Translated: res.translated, Direction: res.direction})
		log.Printf("clipboard check/write failed: %v", err)
		a.tray.SetStatus(a.readyTip)
		a.tray.Notify("Clipboard Translator", "The "+noun+" is kept in Recent translations in the tray menu. Could not write "+noun+" to the clipboard: "+err.Error(), true)
		a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		return
	}
	if !applied {
		a.hist.Add(historyEntry{Source: res.original, Translated: res.translated, Direction: res.direction})
		log.Print("clipboard text changed during " + activity + "; result kept in history (new clipboard preserved)")
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
		a.tray.Notify("Clipboard Translator", "The clipboard changed while "+ongoing+". Your new clipboard text was kept; copy the "+noun+" from Recent translations in the tray menu.", false)
		a.setTrayStateWithTimeout(platform.TrayProblem, trayProblemDuration)
		return
	}
	a.hist.Add(historyEntry{Source: res.original, Translated: res.translated, Direction: res.direction})
	readyStatus := a.readyStatus(res.fallback)
	if res.action == config.ActionTidy {
		readyStatus = "Clipboard Translator - tidied text ready (" + platform.PasteShortcut() + ")"
		if res.fallback != platform.FallbackNone {
			readyStatus = "Clipboard Translator - tidied the clipboard text (" + platform.PasteShortcut() + ")"
		}
	}
	a.setTrayStateWithTimeout(platform.TrayDone, trayDoneDuration)
	action := decidePaste(a.conf.AutoPaste && !a.desktopStatus().Wayland, res.fromSelection, res.window, a.foreground())
	switch action {
	case pasteNow:
		// decidePaste just confirmed the window is still res.window; that is the
		// target pasteWithFocus must still find in front right before Ctrl+V.
		if err := a.paste(res.window); err != nil {
			log.Printf("auto-paste failed: %v", err)
			a.tray.SetStatus(readyStatus)
			a.tray.Notify("Clipboard Translator", strings.ToUpper(noun[:1])+noun[1:]+" ready, but it could not be pasted ("+err.Error()+"). Press "+platform.PasteShortcut()+".", true)
			return
		}
		log.Print(noun + " pasted")
		a.tray.SetStatus("Clipboard Translator - " + noun + " pasted")
	case pasteWindowChanged:
		log.Print(noun + " ready; the active window changed, so it was not pasted")
		a.tray.Notify("Clipboard Translator", strings.ToUpper(noun[:1])+noun[1:]+" ready in the clipboard. The active window changed, so it was not pasted. Press "+platform.PasteShortcut()+".", false)
		a.tray.SetStatus(readyStatus)
	default:
		log.Print(noun+" ready in clipboard (", platform.PasteShortcut(), " to paste)")
		a.tray.SetStatus(readyStatus)
	}
	// The fallback notice replaces the completion notice for this result: a
	// second balloon would take the place of the first on Windows.
	if res.action != config.ActionTidy && res.fallback == platform.FallbackNoCopy && a.noteNoCopy() {
		return
	}
	if a.conf.NotifyOnComplete && action == pasteNone {
		title, msg := platform.CompletionNotification()
		if res.action == config.ActionTidy {
			title, msg = "Text tidy", "Text "+verb+" and ready on the clipboard. Press "+platform.PasteShortcut()+"."
		}
		a.tray.Notify(title, msg, false)
	}
}

// readyStatus is the tray text for a translation that is on the clipboard. A
// translation of the old clipboard text says so, because the user may have
// expected the selection to be translated.
func (a *app) readyStatus(fallback platform.CaptureFallback) string {
	tail := "(" + platform.PasteShortcut() + ")"
	switch fallback {
	case platform.FallbackNoCopy:
		return "Clipboard Translator - translated the clipboard text; no selection was copied " + tail
	case platform.FallbackElevated:
		return "Clipboard Translator - translated the clipboard text; cannot copy from an administrator window " + tail
	}
	return "Clipboard Translator - translation ready " + tail
}

// noteNoCopy tells the user once per run that the clipboard text, not the
// selection, was translated, and reports whether it did. It is called when that
// translation is on the clipboard and nowhere earlier: a press that ends in a
// skip, a failure or a dropped result must neither claim a translation nor use
// up the one notice. Copying first on purpose is a normal way to use the
// hotkey, so this is only a notice.
func (a *app) noteNoCopy() bool {
	if a.noCopyNotified {
		return false
	}
	a.noCopyNotified = true
	a.tray.Notify("Clipboard Translator", "No selection was copied, so the text already on the clipboard was translated. If you copy first on purpose, set \"Text from\" to Clipboard for this hotkey in Settings.", false)
	return true
}

// noteElevatedWindow tells the user once per run why the selection of an
// administrator window was not copied, and what to do instead.
func (a *app) noteElevatedWindow() {
	log.Print("the foreground window runs as administrator; using the text already on the clipboard")
	if a.elevatedNotified {
		return
	}
	a.elevatedNotified = true
	a.tray.Notify("Clipboard Translator", "Cannot copy the selection from a window that runs as administrator. Press Ctrl+C yourself, then the hotkey.", false)
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

func readyMessage(hotkeys string) string {
	if hotkeys == "" {
		return "ready: copy text and choose Translate clipboard, then paste"
	}
	return fmt.Sprintf("ready: select text and press %s (or translate existing clipboard)", hotkeys)
}

func (a *app) ping() {
	select {
	case a.pings <- struct{}{}:
	default:
	}
}

func (a *app) showAlreadyRunning() {
	log.Print("another instance started; showing tray notification")
	a.tray.Notify("Clipboard Translator", "Clipboard Translator is already running. Its icon is here in the tray.", false)
}
