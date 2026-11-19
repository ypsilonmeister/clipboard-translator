package app

import (
	"errors"
	"log"
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

// chooseShortcuts opens the desktop's own shortcut chooser. The binding flips to
// bound when the desktop reports ShortcutsChanged; tickDesktop then refreshes.
func (a *app) chooseShortcuts() {
	c, ok := a.cb.(platform.ShortcutChooser)
	if !ok {
		return
	}
	if err := c.ChooseShortcutKeys(); err != nil {
		a.tray.Notify("Clipboard Translator", "Could not open the shortcut chooser: "+err.Error(), true)
	}
	a.tickDesktop()
}

func (a *app) tickDesktop() {
	s := a.desktopStatus()
	if !s.Wayland {
		return
	}
	stamp := s.Message + "|" + a.hotkeySummary()
	for _, c := range s.Capabilities() {
		stamp += "|" + c.State.String()
	}
	if stamp == a.desktopStamp {
		return
	}
	a.desktopStamp = stamp
	if t, ok := a.tray.(platform.CapabilityTray); ok {
		t.SetCapabilities(capabilityLines(s))
	}
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
			if a.desktopStatus().ManualClipboard {
				err = errors.New("clipboard read timed out; focus the app control window and try Translate clipboard again")
			}
		}
		a.pendingRead = nil
		a.translateCaptured(p.profile, p.win, text, false, platform.FallbackNone, err)
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
	a.trayStateResetAt = time.Time{}
	a.setTrayState(platform.TrayBusy)
	a.resetIdleTimer()
	p := a.conf.Profiles[0]
	p.Input = config.InputClipboard
	a.presentManualControl()
	source, err := a.cb.Read()
	if errors.Is(err, platform.ErrClipboardPending) {
		a.pendingRead = &pendingClipboardRead{profile: p, deadline: a.timeNow().Add(8 * time.Second)}
		return
	}
	a.translateCaptured(p, 0, source, false, platform.FallbackNone, err)
}

func (a *app) chromeOSProfile() bool {
	s := a.desktopStatus()
	return s.Crostini && s.PortalCapabilitiesMissing
}

// The platform decides whether the read needs the focused control window: with
// only the hotkey missing, the portal clipboard is used and nothing is shown.
func (a *app) presentManualControl() {
	if s := a.desktopStatus(); s.Manual || s.ManualClipboard {
		if window, ok := a.cb.(interface{ PresentControl() }); ok {
			window.PresentControl()
		}
	}
}

// capabilityLines are the per-capability status lines (#133 P1-6) for the menu
// and the Settings page. Capabilities the backend did not evaluate are left out.
func capabilityLines(s platform.DesktopStatus) []string {
	var lines []string
	for _, c := range s.Capabilities() {
		name := map[string]string{"hotkey": "Hotkey", "clipboard": "Clipboard", "capture": "Selection capture"}[c.Name]
		var state string
		switch c.State {
		case platform.CapabilityAvailable:
			state = "ready"
		case platform.CapabilityPending:
			state = "waiting for permission"
		case platform.CapabilityWaiting:
			state = "reconnecting (unlock the screen if it is locked)"
		case platform.CapabilityRefused:
			state = "needs attention (Retry Wayland permissions)"
		case platform.CapabilityMissing:
			state = "not provided by this desktop"
			switch c.Name {
			case "hotkey":
				state += "; use Translate clipboard"
			case "clipboard":
				state += "; uses the control window"
			case "capture":
				state += "; the clipboard text is translated"
			}
		default:
			continue
		}
		lines = append(lines, name+": "+state)
	}
	return lines
}
