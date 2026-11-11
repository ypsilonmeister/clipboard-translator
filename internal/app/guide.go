package app

import (
	"fmt"
	"log"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

// The quick start guide: select text -> hotkey -> paste. It is built from the
// running configuration, so a custom hotkey or language pair is shown as it is.
// It appears once as a tray notification (quickstart_shown in config.json), is
// always on the Settings page, and can be read again from the tray menu. It
// changes no setting; in particular it never turns on auto-paste.

// guideSteps uses the manual ChromeOS flow or the active desktop hotkey.
func (a *app) guideSteps() []string {
	if a.chromeOSManualMode() {
		return []string{
			"ChromeOS Settings → Developers → Linux → Port forwarding: add TCP 18121.",
			"Install/load the Chrome extension.",
			"In the app menu, use \"Copy connection info\", and paste it into the extension options.",
			"Select text in Chrome → right-click → \"Translate with Clipboard Translator\" → paste with Ctrl+V.",
		}
	}
	if s := a.desktopStatus(); s.Wayland && !s.Ready {
		return nil
	}
	sum := a.hotkeySummary()
	if sum == "" {
		return nil
	}
	steps := []string{
		"Select the text you want to translate, in any program.",
		"Press " + sum + ".",
		"Paste with " + platform.PasteShortcut() + " when the tray says the translation is ready.",
	}
	if a.desktopStatus().Wayland {
		steps[1] += " Release the hotkey modifiers before continuing."
	}
	if len(a.conf.Profiles) == 1 {
		steps[1] += " It translates " + a.conf.Profiles[0].Label() + "."
	}
	return steps
}

const guideExtras = "Other models, languages, hotkeys and automatic pasting are optional; you can change them in Settings whenever you like."

func (a *app) quickstartGuide() settings.QuickstartGuide {
	g := settings.QuickstartGuide{Initial: !a.conf.QuickstartShown}
	g.Steps = a.guideSteps()
	if g.Steps == nil {
		if s := a.desktopStatus(); s.Wayland {
			g.Intro = s.Message + " Use Retry Wayland permissions in the control window when available."
			return g
		}
		g.Intro = "The hotkey " + a.conf.Hotkey + " is not active (another program may be using it), so translating by hotkey does not work yet. Choose another hotkey under Hotkeys below and click Save."
		return g
	}
	if a.chromeOSManualMode() {
		g.Active = true
		g.Intro = "Translate your first text in four steps:"
		return g
	}
	g.Active = true
	g.Intro = "Translate your first text in three steps:"
	g.Extras = guideExtras
	if a.desktopStatus().Wayland {
		g.Extras = "Automatic paste is disabled on Wayland. The desktop chooses the active shortcuts; the requested keys remain editable below."
	}
	return g
}

// guideNotification is the short text for the tray notification.
func (a *app) guideNotification() (msg string, isError bool) {
	steps := a.guideSteps()
	if steps == nil {
		if s := a.desktopStatus(); s.Wayland {
			return s.Message + " Use Retry Wayland permissions in the control window when available.", true
		}
		return fmt.Sprintf("The hotkey %s is not active (another program may be using it). Open Settings... from the tray menu and choose another hotkey.", a.conf.Hotkey), true
	}
	return "Ready. " + strings.Join(steps, " "), false
}

// showQuickstart notifies the guide, and remembers that it was shown once the
// steps could really be followed. With no working hotkey it points at Settings
// and stays unseen, so the guide comes back when a hotkey works.
func (a *app) showQuickstart() {
	msg, isErr := a.guideNotification()
	a.tray.Notify("Clipboard Translator", msg, isErr)
	if !isErr && !a.conf.QuickstartShown {
		a.conf.QuickstartShown = true
		log.Print("quick start guide shown")
		a.saveConfig(map[string]any{"quickstart_shown": true})
	}
}
