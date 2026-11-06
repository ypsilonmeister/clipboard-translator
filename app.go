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

	hotkey    Hotkey
	newHotkey func(spec string) (Hotkey, error) // newHotkey, replaceable in tests
	showErr   func(msg string)                  // showError, replaceable in tests

	updates  chan updateResult
	checking bool
	latest   releaseInfo // newest release seen by "Check for updates"
}

func newApp(dir string, conf Config, paths appPaths, tray Tray, serverLog io.Writer) *app {
	a := &app{
		dir:      dir,
		conf:     conf,
		endpoint: fmt.Sprintf("http://127.0.0.1:%d", conf.Port),
		paths:    paths,
		tray:     tray,
		limiter:  restartLimiter{max: 3, window: 10 * time.Minute},
		readyTip: "Clipboard Translator - ready (" + conf.Hotkey + ")",

		newHotkey: newHotkey,
		showErr:   showError,
		updates:   make(chan updateResult, 1),
	}
	tray.SetMenu(a.buildMenu)
	a.sup = &supervisor{
		model: paths.model, template: paths.template,
		contextTokens: conf.ContextTokens, port: conf.Port, endpoint: a.endpoint,
		logOut:         serverLog,
		startupTimeout: time.Duration(conf.StartupTimeoutSec) * time.Second,
	}
	lastSec := time.Duration(-1)
	a.sup.ui = func(label string, elapsed time.Duration) bool {
		pumpMessages()
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
func (a *app) startServer() error {
	err := a.sup.start(a.paths.server, a.conf.GPULayers, "loading model")
	if err == nil || errors.Is(err, errQuit) {
		return err
	}
	log.Printf("preferred backend failed: %v; see server.log", err)
	if a.paths.server == a.paths.fallback {
		return fmt.Errorf("The translation server failed to start: %w", err)
	}
	if _, statErr := os.Stat(a.paths.fallback); statErr != nil {
		return fmt.Errorf("CPU fallback unavailable: %w", statErr)
	}
	log.Print("falling back to CPU")
	if err := a.sup.start(a.paths.fallback, 0, "loading model (CPU)"); err != nil {
		if errors.Is(err, errQuit) {
			return err
		}
		return fmt.Errorf("CPU fallback failed: %w", err)
	}
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
		showError(fmt.Sprintf("Clipboard initialization failed: %v", err))
		return
	}
	defer cb.Close()
	a.hotkey, err = a.newHotkey(a.conf.Hotkey)
	if err != nil {
		log.Printf("hotkey: %v", err)
		showError(fmt.Sprintf("Could not register the hotkey %s: %v", a.conf.Hotkey, err))
		return
	}
	defer func() { _ = a.hotkey.Close() }() // a.hotkey changes when the user picks another one
	a.serve(cb)
}

func (a *app) serve(cb Clipboard) {
	conf, tray := a.conf, a.tray
	log.Printf("ready: select text and press %s (or translate existing clipboard)", conf.Hotkey)
	tray.SetStatus(a.readyTip)
	tray.Notify("Clipboard Translator", "Ready. Select text and press "+conf.Hotkey+", then paste with Ctrl+V.", false)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	results := make(chan result, 16)
	// The same tick also services the tray window, so never poll slower than 100ms
	// or the tray menu would feel frozen.
	pollInterval := time.Duration(conf.PollMS) * time.Millisecond
	if pollInterval > 100*time.Millisecond {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	stopFile := filepath.Join(a.dir, "STOP")
	defer os.Remove(stopFile)
	var generation uint64
	var cancelCurrent context.CancelFunc
	cancelTranslation := func() {
		if cancelCurrent != nil {
			cancelCurrent()
			cancelCurrent = nil
		}
	}
	for {
		select {
		case <-sig:
			log.Print("stopping")
			cancelTranslation()
			return
		case <-a.sup.Exited():
			log.Print("llama-server exited unexpectedly")
			cancelTranslation()
			generation++ // a translation in flight can no longer succeed
			a.sup.Stop()
			if !a.restartServer() {
				return
			}
		case <-ticker.C:
			_, statErr := os.Stat(stopFile)
			if statErr == nil || tray.QuitRequested() {
				log.Print("stop requested")
				cancelTranslation()
				return
			}
			pressed, err := a.hotkey.Pressed()
			if err != nil {
				log.Printf("hotkey error: %v", err)
				continue
			}
			// Pressed pumps messages, so "Quit" or a menu item may have been chosen just now.
			if tray.QuitRequested() {
				log.Print("stop requested")
				cancelTranslation()
				return
			}
			for {
				id, ok := tray.NextCommand()
				if !ok {
					break
				}
				a.handleCommand(id)
			}
			if tray.QuitRequested() { // e.g. Quit during an engine switch
				log.Print("stop requested")
				cancelTranslation()
				return
			}
			if !pressed {
				continue
			}
			// A new request cancels older ones and invalidates their results.
			generation++
			cancelTranslation()
			source, _, err := captureSelectedText(cb)
			if err != nil {
				log.Printf("cannot capture selection: %v", err)
				tray.SetStatus(a.readyTip)
				continue
			}
			if !shouldTranslate(source, conf.MaxChars) {
				log.Print("skipped: empty, unsupported, URL, or too long")
				tray.SetStatus(a.readyTip)
				continue
			}
			direction, _ := detectDirection(source)
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(conf.TranslationTimeoutSec)*time.Second)
			cancelCurrent = cancel
			g := generation
			log.Printf("translating %s -> %s", direction.Source, direction.Target)
			tray.SetStatus("Clipboard Translator - translating...")
			go func(src string, g uint64) {
				translated, err := translate(ctx, a.endpoint, src, direction, conf.MaxTokens)
				select {
				case results <- result{original: src, translated: translated, generation: g, err: err}:
				default:
				}
			}(source, g)
		case u := <-a.updates:
			a.onUpdateResult(u)
		case res := <-results:
			if res.generation != generation {
				continue
			}
			tray.SetStatus(a.readyTip)
			if res.err != nil {
				if !errors.Is(res.err, context.Canceled) {
					log.Printf("translation failed: %v", res.err)
					tray.Notify("Translation failed", "See translator.log for details.", true)
				}
				continue
			}
			if res.translated == res.original {
				log.Print("translation unchanged; clipboard left intact")
				continue
			}
			// A sequence number is not a reliable test for a text change:
			// apps can update clipboard formats without changing the text.
			// Compare text AND replace under a single OpenClipboard lock.
			applied, err := applyTranslation(cb, res.original, res.translated)
			if err != nil {
				log.Printf("clipboard check/write failed: %v", err)
				continue
			}
			if !applied {
				log.Print("clipboard text changed during translation; result discarded (new clipboard preserved)")
				continue
			}
			log.Print("translation ready in clipboard (Ctrl+V to paste)")
			tray.SetStatus("Clipboard Translator - translation ready (Ctrl+V)")
		}
	}
}
