package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

type deniedDesktopHotkey struct{}

func (deniedDesktopHotkey) Pressed() (bool, error) { return false, errors.New("shortcut denied") }
func (deniedDesktopHotkey) Close() error           { return nil }

func TestDesktopPrimaryDenialDoesNotHideOtherProfiles(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkey = deniedDesktopHotkey{}
	fired := true
	a.extras = []extraHotkey{{profile: 1, hk: pressable{fired: &fired}}}
	pressed, profile, err := a.pollHotkeys()
	if !pressed || profile != 1 || err == nil {
		t.Fatal("active extra profile must survive primary refusal")
	}
}

type asyncDesktopClipboard struct {
	*testClipboard
	readPending, writePending bool
	state                     platform.DesktopStatus
	retries                   int
}

func (c *asyncDesktopClipboard) Read() (string, error) {
	if c.readPending {
		return "", platform.ErrClipboardPending
	}
	return c.testClipboard.Read()
}
func (c *asyncDesktopClipboard) Write(s string) error {
	if c.writePending {
		return platform.ErrClipboardPending
	}
	return c.testClipboard.Write(s)
}
func (c *asyncDesktopClipboard) ReplaceIfUnchanged(old, s string) (bool, error) {
	if c.readPending || c.writePending {
		return false, platform.ErrClipboardPending
	}
	return c.testClipboard.ReplaceIfUnchanged(old, s)
}
func (c *asyncDesktopClipboard) DesktopStatus() platform.DesktopStatus { return c.state }
func (c *asyncDesktopClipboard) RetryDesktop()                         { c.retries++ }

func TestDesktopPendingResultGenerationAndCompletion(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "new-request"}[abort], func(t *testing.T) {
			a, tray, base, pastes, _ := uxApp(t)
			base.text = "source"
			cb := &asyncDesktopClipboard{testClipboard: base, writePending: true, state: platform.DesktopStatus{Wayland: true, Ready: true}}
			a.cb = cb
			a.conf.AutoPaste = true
			g := pending(a)
			a.handleResult(result{generation: g, original: "source", translated: "translation", fromSelection: true, window: 100})
			if a.pendingResult == nil || !a.busy() || len(a.hist.Items()) != 0 || len(tray.notes) != 0 {
				t.Fatal("pending must not report completion")
			}
			if abort {
				a.abortTranslation()
			}
			cb.writePending = false
			a.tickClipboard()
			if *pastes != 0 || a.pendingResult != nil {
				t.Fatal("Wayland must never auto-paste")
			}
			if abort {
				if base.text != "source" || len(a.hist.Items()) != 0 {
					t.Fatal("stale generation wrote clipboard")
				}
			} else if base.text != "translation" || len(a.hist.Items()) != 1 {
				t.Fatal("completion was not applied once")
			}
			a.tickClipboard()
			if len(a.hist.Items()) > 1 {
				t.Fatal("duplicate history")
			}
		})
	}
}
func TestDesktopPendingResultTimeoutKeepsHistory(t *testing.T) {
	a, tray, base, _, _ := uxApp(t)
	base.text = "source"
	a.cb = &asyncDesktopClipboard{testClipboard: base, writePending: true}
	now := time.Now()
	a.now = func() time.Time { return now }
	a.handleResult(result{generation: pending(a), original: "source", translated: "translation"})
	now = now.Add(9 * time.Second)
	a.tickClipboard()
	if a.pendingResult != nil || len(a.hist.Items()) != 1 || base.text != "source" || !strings.Contains(tray.lastNote(), "timed out") {
		t.Fatal("timeout should keep result without claiming success")
	}
}
func TestDesktopAsyncHistoryAndAbort(t *testing.T) {
	a, tray, base, _, _ := uxApp(t)
	cb := &asyncDesktopClipboard{testClipboard: base, writePending: true}
	a.cb = cb
	a.hist.Add(historyEntry{Source: "source", Translated: "translation"})
	a.copyHistoryText(0, "bilingual")
	if a.pendingHistory == nil || len(tray.notes) != 0 {
		t.Fatal("pending history must not notify success")
	}
	cb.writePending = false
	a.tickClipboard()
	if a.pendingHistory != nil || base.text != "source\ntranslation" || !strings.Contains(tray.lastNote(), "Copied") {
		t.Fatal("history write did not complete")
	}
}
func TestDesktopClipboardReadCancellation(t *testing.T) {
	a, _, base, _, _ := uxApp(t)
	cb := &asyncDesktopClipboard{testClipboard: base, readPending: true}
	a.cb = cb
	a.conf.Profiles[0].Input = config.InputClipboard
	a.startTranslation(0)
	if a.pendingRead == nil || !a.busy() {
		t.Fatal("clipboard profile must wait without translating")
	}
	a.abortTranslation()
	cb.readPending = false
	a.tickClipboard()
	if a.pendingRead != nil || a.translating {
		t.Fatal("aborted read started translation")
	}
}
func TestDesktopStatusGuideAndSettings(t *testing.T) {
	a, tray, base, _, _ := uxApp(t)
	cb := &asyncDesktopClipboard{testClipboard: base, state: platform.DesktopStatus{Wayland: true, Retry: true, Message: "Permission refused"}}
	a.cb = cb
	a.tickDesktop()
	st := a.settingsState()
	if !st.Wayland || st.DesktopStatus != "Permission refused" || st.Guide.Active || !strings.Contains(tray.status, "Permission refused") {
		t.Fatal("permission state is missing from UI")
	}
	a.handleCommand(cmdRetryDesktop)
	if cb.retries != 1 {
		t.Fatal("retry command did not reach session")
	}
}
