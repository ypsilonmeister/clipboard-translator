package app

import (
	"errors"
	"log"
	"runtime"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

type pendingClipboardRead struct {
	profile  config.Profile
	win      uintptr
	deadline time.Time
}

func (a *app) desktopStatus() platform.DesktopStatus {
	if s, ok := a.cb.(platform.DesktopSession); ok {
		return s.DesktopStatus()
	}
	return platform.DesktopStatus{}
}

func (a *app) retryDesktop() {
	if s, ok := a.cb.(platform.DesktopSession); ok {
		a.abortTranslation()
		s.RetryDesktop()
		a.tickDesktop()
	}
}

func (a *app) tickDesktop() {
	s := a.desktopStatus()
	if !s.Wayland {
		return
	}
	stamp := s.Message + "|" + a.hotkeySummary()
	if stamp == a.desktopStamp {
		return
	}
	a.desktopStamp = stamp
	a.updateReadyTip()
	if !a.busy() && a.capturing == nil {
		a.refreshStatus()
	}
	if s.Ready && !a.conf.QuickstartShown {
		a.showQuickstart()
	}
}

// Clipboard operations are retried only from the UI loop. Each pending result
// retains its generation and is discarded by abortTranslation on a new request.
func (a *app) tickClipboard() {
	if p := a.pendingRead; p != nil {
		text, err := a.cb.Read()
		if errors.Is(err, platform.ErrClipboardPending) {
			if a.timeNow().Before(p.deadline) {
				return
			}
			err = errors.New("clipboard read timed out")
		}
		a.pendingRead = nil
		a.translateCaptured(p.profile, p.win, text, false, err)
	}
	if p := a.pendingResult; p != nil {
		if p.generation != a.generation {
			a.pendingResult = nil
		} else {
			a.handleResult(*p)
		}
	}
	if p := a.pendingHistory; p != nil {
		a.finishHistoryCopy(*p)
	}
}

func (a *app) finishHistoryCopy(text string) {
	err := a.cb.Write(text)
	if errors.Is(err, platform.ErrClipboardPending) {
		if a.pendingHistory == nil {
			a.pendingHistoryDeadline = a.timeNow().Add(8 * time.Second)
		}
		if a.timeNow().Before(a.pendingHistoryDeadline) {
			a.pendingHistory = &text
			return
		}
		err = errors.New("clipboard write timed out; retry copying from Recent translations")
	}
	a.pendingHistory = nil
	if err != nil {
		log.Printf("could not copy from history: %v", err)
		a.tray.Notify("Clipboard Translator", "Could not copy to the clipboard: "+err.Error(), true)
		return
	}
	a.tray.Notify("Clipboard Translator", "Copied. Paste with "+platform.PasteShortcut()+".", false)
}

func (a *app) startClipboardTranslation() {
	a.abortTranslation()
	a.resetIdleTimer()
	p := a.conf.Profiles[0]
	p.Input = config.InputClipboard
	source, err := a.cb.Read()
	if errors.Is(err, platform.ErrClipboardPending) {
		a.pendingRead = &pendingClipboardRead{profile: p, deadline: a.timeNow().Add(8 * time.Second)}
		return
	}
	a.translateCaptured(p, 0, source, false, err)
}

func (a *app) chromeOSManualMode() bool {
	s := a.desktopStatus()
	return runtime.GOOS == "linux" && s.Wayland && s.Manual
}
