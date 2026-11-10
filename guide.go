package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// The quick start guide: select text -> hotkey -> paste. It is built from the
// running configuration, so a custom hotkey or language pair is shown as it is.
// It appears once as a tray notification (quickstart_shown in config.json), is
// always on the Settings page, and can be read again from the tray menu. It
// changes no setting; in particular it never turns on auto-paste.

// quickstartGuide is the guide as the Settings page shows it.
type quickstartGuide struct {
	Active  bool     `json:"active"` // a hotkey works, so the steps can be followed
	Intro   string   `json:"intro"`  // what to do now, or why nothing works yet
	Steps   []string `json:"steps,omitempty"`
	Extras  string   `json:"extras,omitempty"` // optional configuration, mentioned after the basics
	Initial bool     `json:"initial"`          // first run: the page opens the guide
}

// guideSteps are the three steps with the real hotkey, or nil when none works.
func (a *app) guideSteps() []string {
	sum := a.hotkeySummary()
	if sum == "" {
		return nil
	}
	steps := []string{
		"Select the text you want to translate, in any program.",
		"Press " + sum + ".",
		"Paste with " + platform.PasteShortcut() + " when the tray says the translation is ready.",
	}
	if len(a.conf.Profiles) == 1 {
		steps[1] += " It translates " + a.conf.Profiles[0].label() + "."
	}
	return steps
}

const guideExtras = "Other models, languages, hotkeys and automatic pasting are optional; you can change them in Settings whenever you like."

func (a *app) quickstartGuide() quickstartGuide {
	g := quickstartGuide{Initial: !a.conf.QuickstartShown}
	g.Steps = a.guideSteps()
	if g.Steps == nil {
		g.Intro = "The hotkey " + a.conf.Hotkey + " is not active (another program may be using it), so translating by hotkey does not work yet. Choose another hotkey under Hotkeys below and click Save."
		return g
	}
	g.Active = true
	g.Intro = "Translate your first text in three steps:"
	g.Extras = guideExtras
	return g
}

// guideNotification is the short text for the tray notification.
func (a *app) guideNotification() (msg string, isError bool) {
	steps := a.guideSteps()
	if steps == nil {
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
