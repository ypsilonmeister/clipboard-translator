package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

// uxApp is an app wired for translation-flow tests: a fake clipboard, a fake
// foreground window and a recording paste function.
func uxApp(t *testing.T) (a *app, tray *fakeTray, cb *testClipboard, pastes *int, fg *uintptr) {
	t.Helper()
	a, tray, _ = newTestApp(t)
	cb = &testClipboard{}
	a.cb = cb
	win := uintptr(100)
	fg = &win
	a.foreground = func() uintptr { return *fg }
	n := 0
	pastes = &n
	a.paste = func(uintptr) error { n++; return nil }
	return
}

// pending puts a translation "in flight" the way startTranslation does.
func pending(a *app) uint64 {
	a.generation++
	a.translating = true
	a.cancelCurrent = func() {}
	return a.generation
}

func TestHandleResultCopiesAndRemembers(t *testing.T) {
	a, tray, cb, pastes, _ := uxApp(t)
	cb.text = "こんにちは"
	g := pending(a)
	a.handleResult(result{original: "こんにちは", translated: "Hello", generation: g, direction: languages.Direction{Source: "ja", Target: "en"}, window: 100, fromSelection: true})
	if cb.text != "Hello" {
		t.Fatalf("clipboard = %q", cb.text)
	}
	if items := a.hist.Items(); len(items) != 1 || items[0].Translated != "Hello" {
		t.Fatalf("history = %+v", items)
	}
	if a.translating {
		t.Fatal("translating must be cleared")
	}
	if *pastes != 0 {
		t.Fatal("auto-paste is off by default and must not paste")
	}
	if !strings.Contains(tray.status, "translation ready") {
		t.Fatalf("status = %q", tray.status)
	}
}

func TestHandleResultAutoPaste(t *testing.T) {
	a, tray, cb, pastes, fg := uxApp(t)
	a.conf.AutoPaste = true

	// Same window, fresh selection: paste.
	cb.text = "a"
	g := pending(a)
	a.handleResult(result{original: "a", translated: "A", generation: g, window: 100, fromSelection: true})
	if *pastes != 1 || !strings.Contains(tray.status, "pasted") {
		t.Fatalf("pastes=%d status=%q", *pastes, tray.status)
	}

	// Focus moved while the model worked: do not paste into the wrong window.
	cb.text = "b"
	g = pending(a)
	*fg = 200
	a.handleResult(result{original: "b", translated: "B", generation: g, window: 100, fromSelection: true})
	if *pastes != 1 {
		t.Fatalf("must not paste into another window, pastes=%d", *pastes)
	}
	if !strings.Contains(tray.lastNote(), "active window changed") || cb.text != "B" {
		t.Fatalf("note=%q clipboard=%q", tray.lastNote(), cb.text)
	}

	// Only the old clipboard was translated (no selection): never paste.
	*fg = 100
	cb.text = "c"
	g = pending(a)
	a.handleResult(result{original: "c", translated: "C", generation: g, window: 100, fromSelection: false})
	if *pastes != 1 {
		t.Fatalf("must not paste when nothing was selected, pastes=%d", *pastes)
	}

	// A paste that fails is reported, and the translation stays in the clipboard.
	a.paste = func(uintptr) error { return errors.New("keys held") }
	cb.text = "d"
	g = pending(a)
	tray.status = "Clipboard Translator - translating... (1s)"
	a.handleResult(result{original: "d", translated: "D", generation: g, window: 100, fromSelection: true})
	if !strings.Contains(tray.lastNote(), "could not be pasted") || cb.text != "D" {
		t.Fatalf("note=%q clipboard=%q", tray.lastNote(), cb.text)
	}
	wantStatus := "Clipboard Translator - translation ready (" + pasteShortcut() + ")"
	if tray.status != wantStatus {
		t.Fatalf("failed auto-paste status = %q, want %q", tray.status, wantStatus)
	}
}

func TestHandleResultTruncatedKeepsClipboard(t *testing.T) {
	a, tray, cb, pastes, _ := uxApp(t)
	a.conf.AutoPaste = true
	cb.text = "こんにちは"
	g := pending(a)
	a.handleResult(result{original: "こんにちは", generation: g, window: 100, fromSelection: true, err: errTruncated})
	if cb.text != "こんにちは" || cb.writes != 0 || *pastes != 0 || len(a.hist.Items()) != 0 {
		t.Fatalf("clipboard=%q writes=%d pastes=%d history=%d", cb.text, cb.writes, *pastes, len(a.hist.Items()))
	}
	if !strings.Contains(tray.lastNote(), "max_tokens") {
		t.Fatalf("the notification must say what to change, note=%q", tray.lastNote())
	}
}

func TestHandleResultIgnoresStaleAndFailed(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.text = "x"
	old := pending(a)
	g := pending(a) // a newer request replaced it
	a.handleResult(result{original: "x", translated: "X", generation: old})
	if cb.text != "x" || !a.translating {
		t.Fatal("a stale result must change nothing")
	}
	a.handleResult(result{original: "x", generation: g, err: errors.New("boom")})
	if cb.text != "x" || a.translating || len(a.hist.Items()) != 0 {
		t.Fatal("a failed translation must not touch the clipboard or the history")
	}
	if !strings.Contains(tray.lastNote(), "translator.log") { // fakeTray records the balloon text only
		t.Fatalf("a failure must be reported, note=%q", tray.lastNote())
	}
	n := len(tray.notes)
	g = pending(a)
	a.handleResult(result{original: "x", generation: g, err: context.Canceled})
	if len(tray.notes) != n {
		t.Fatal("a cancelled request must not show an error")
	}
	// The user copied something else meanwhile: keep it, remember nothing.
	cb.text = "typed meanwhile"
	g = pending(a)
	a.handleResult(result{original: "x", translated: "X", generation: g})
	if cb.text != "typed meanwhile" || len(a.hist.Items()) != 0 {
		t.Fatal("a result for an outdated clipboard must be discarded")
	}
}

func TestCancelFromMenu(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	cancelled := false
	g := pending(a)
	a.cancelCurrent = func() { cancelled = true }
	a.readyTip = "ready"

	if items := a.buildMenu(); items[2].ID != cmdCancel {
		t.Fatalf("while translating, Cancel must follow Settings and Quick start, got %+v", items[2])
	}
	a.handleCommand(cmdCancel)
	if !cancelled || a.translating || a.generation == g || tray.status != "Clipboard Translator - translation cancelled" {
		t.Fatalf("cancelled=%v translating=%v generation=%d (was %d) status=%q", cancelled, a.translating, a.generation, g, tray.status)
	}
	a.now = func() time.Time { return time.Now().Add(9 * time.Second) }
	a.tickTransientStatus()
	if tray.status != "ready" {
		t.Fatalf("status must reset to readyTip after 8s, got %q", tray.status)
	}
	for _, it := range a.buildMenu() {
		if it.ID == cmdCancel {
			t.Fatal("when idle there is nothing to cancel")
		}
	}
	// The cancelled request's late result is ignored.
	a.handleResult(result{original: "x", translated: "X", generation: g})
	if len(a.hist.Items()) != 0 {
		t.Fatal("a result of a cancelled request must be ignored")
	}
}

func TestElapsedSecondsInTooltip(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	pending(a)
	a.translateStart = time.Now().Add(-3 * time.Second)
	a.lastTranslSec = 0
	a.tickTranslationStatus()
	if !strings.Contains(tray.status, "(3s)") {
		t.Fatalf("status = %q", tray.status)
	}
	tray.status = "untouched"
	a.tickTranslationStatus() // same second: no needless tooltip update
	if tray.status != "untouched" {
		t.Fatal("the tooltip must only change when the second changes")
	}
	a.translating = false
	a.translateStart = time.Now().Add(-10 * time.Second)
	a.tickTranslationStatus()
	if tray.status != "untouched" {
		t.Fatal("nothing to show when idle")
	}
}

func TestSettingsTogglesAreSaved(t *testing.T) {
	for _, field := range []string{"auto_paste", "notify_on_complete"} {
		t.Run(field, func(t *testing.T) {
			a, _, _, _, _ := uxApp(t)
			for _, enabled := range []bool{true, false} {
				saveSettingsField(t, a, field, enabled)
				st := a.settingsState()
				c, err := loadConfig(a.dir)
				if err != nil {
					t.Fatal(err)
				}
				got, saved := st.AutoPaste, c.AutoPaste
				if field == "notify_on_complete" {
					got, saved = st.NotifyOnComplete, c.NotifyOnComplete
				}
				if got != enabled || saved != enabled {
					t.Fatalf("enabled=%v state=%v saved=%v", enabled, got, saved)
				}
			}
		})
	}
}

func TestHandleResultNotifyOnComplete(t *testing.T) {
	a, tray, cb, pastes, fg := uxApp(t)
	a.conf.NotifyOnComplete = true

	wantTitle, wantBody := completionNotification()
	if wantTitle == "" || wantBody == "" {
		t.Fatal("completion notification must not be empty")
	}

	assertGenericNotification := func(t *testing.T, source, translated string) {
		t.Helper()
		if tray.lastTitle() != wantTitle {
			t.Fatalf("title = %q, want %q", tray.lastTitle(), wantTitle)
		}
		if tray.lastNote() != wantBody {
			t.Fatalf("note = %q, want %q", tray.lastNote(), wantBody)
		}
		for _, s := range []string{tray.lastTitle(), tray.lastNote()} {
			if strings.Contains(s, source) {
				t.Fatalf("notification %q must not contain source text %q", s, source)
			}
			if strings.Contains(s, translated) {
				t.Fatalf("notification %q must not contain translated text %q", s, translated)
			}
			if strings.Contains(s, "Japanese") || strings.Contains(s, "English") || strings.Contains(s, "→") {
				t.Fatalf("notification %q must not leak language names or direction", s)
			}
		}
	}

	// 1. auto-paste off, translation finishes: generic completion notification shown.
	cb.text = "こんにちは"
	g := pending(a)
	a.handleResult(result{
		original:      "こんにちは",
		translated:    "Hello world",
		generation:    g,
		direction:     languages.Direction{Source: "ja", Target: "en"},
		window:        100,
		fromSelection: true,
	})
	if *pastes != 0 {
		t.Fatal("auto-paste is off; must not paste")
	}
	assertGenericNotification(t, "こんにちは", "Hello world")

	// 2. target-only profile direction (Source empty): generic completion notification shown.
	cb.text = "Hello"
	g = pending(a)
	a.handleResult(result{
		original:      "Hello",
		translated:    "こんにちは",
		generation:    g,
		direction:     languages.Direction{Target: "ja"},
		window:        100,
		fromSelection: true,
	})
	assertGenericNotification(t, "Hello", "こんにちは")

	// 3. auto-paste on and pasted successfully (action == pasteNow): no completion notification.
	a.conf.AutoPaste = true
	*fg = 100
	tray.notes = nil
	tray.titles = nil
	cb.text = "apple"
	g = pending(a)
	a.handleResult(result{
		original:      "apple",
		translated:    "りんご",
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
	})
	if *pastes != 1 {
		t.Fatalf("pastes = %d, want 1", *pastes)
	}
	for _, title := range tray.titles {
		if title == wantTitle {
			t.Fatalf("pasteNow must not show completion notification, got title %q", title)
		}
	}
	for _, note := range tray.notes {
		if strings.Contains(note, "apple") || strings.Contains(note, "りんご") {
			t.Fatalf("notification note %q must not contain source or translated text", note)
		}
	}

	// 4. auto-paste on but window changed (action == pasteWindowChanged): exactly one notification (no double notification).
	*fg = 200
	cb.text = "orange"
	tray.notes = nil
	tray.titles = nil
	g = pending(a)
	a.handleResult(result{
		original:      "orange",
		translated:    "みかん",
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
	})
	if len(tray.notes) != 1 {
		t.Fatalf("pasteWindowChanged must show exactly 1 notification, got %d: %v", len(tray.notes), tray.notes)
	}
	if !strings.Contains(tray.lastNote(), "active window changed") {
		t.Fatalf("note = %q, want active window changed", tray.lastNote())
	}
	if tray.lastTitle() == wantTitle {
		t.Fatalf("generic completion notification must not fire in pasteWindowChanged")
	}

	// 5. clipboard text changed during translation: result discarded notification shown, but no completion notification.
	tray.notes = nil
	tray.titles = nil
	cb.text = "user copied something else"
	g = pending(a)
	a.handleResult(result{
		original:      "grape",
		translated:    "ぶどう",
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
	})
	if len(tray.notes) != 1 {
		t.Fatalf("discarded outcome must show exactly 1 notification, got %d: %v", len(tray.notes), tray.notes)
	}
	if !strings.Contains(tray.lastNote(), "clipboard changed while translating") {
		t.Fatalf("note = %q, want clipboard changed notification", tray.lastNote())
	}
	if tray.lastTitle() == wantTitle {
		t.Fatalf("generic completion notification must not fire on discarded result")
	}
	if cb.text != "user copied something else" {
		t.Fatalf("clipboard must be kept intact, got %q", cb.text)
	}
}

func TestHistoryMenuCopiesAndClears(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	find := func(label string) []MenuItem {
		for _, it := range a.buildMenu() {
			if strings.HasPrefix(it.Label, label) {
				return it.Sub
			}
		}
		return nil
	}
	if sub := find("Recent translations"); len(sub) != 1 || !sub[0].Disabled {
		t.Fatalf("an empty history shows one disabled placeholder, got %+v", sub)
	}
	a.hist.Add(entry("first", "erste"))
	a.hist.Add(entry("second & more", "zweite"))
	sub := find("Recent translations")
	if len(sub) != 4 || sub[0].ID != cmdHistoryBase || sub[1].ID != cmdHistoryBase+1 || sub[3].ID != cmdHistoryClear {
		t.Fatalf("history submenu = %+v", sub)
	}
	if !strings.HasPrefix(sub[0].Label, "second & more") {
		t.Fatalf("newest first, got %q", sub[0].Label)
	}

	a.handleCommand(cmdHistoryBase + 1) // the older entry
	if cb.text != "erste" || !strings.Contains(tray.lastNote(), "Copied") {
		t.Fatalf("clipboard=%q note=%q", cb.text, tray.lastNote())
	}
	a.handleCommand(cmdHistoryBase + 99) // a stale index must do nothing
	if cb.text != "erste" {
		t.Fatal("a stale history index must be ignored")
	}
	a.handleCommand(cmdHistoryClear)
	if len(a.hist.Items()) != 0 {
		t.Fatal("history not cleared")
	}
}

func TestSkipReasonClassification(t *testing.T) {
	pairProfile := Profile{Languages: []string{"ja", "en"}}
	targetProfile := Profile{Target: "ko"}

	cases := []struct {
		name     string
		p        Profile
		input    string
		maxChars int
		want     skipReason
	}{
		{"empty string", pairProfile, "", 100, skipEmpty},
		{"whitespace only", pairProfile, "   \n\t  ", 100, skipEmpty},
		{"too long pair", pairProfile, "こんにちは", 2, skipTooLong},
		{"too long target", targetProfile, strings.Repeat("a", 101), 100, skipTooLong},
		{"url https", pairProfile, "https://example.com/test", 100, skipURL},
		{"url http", targetProfile, "http://example.com", 100, skipURL},
		{"digits and symbols only for pair", pairProfile, "12345 !?", 100, skipNoTranslatableText},
		{"digits and symbols only for target", targetProfile, "12345 !?", 100, skipNoTranslatableText},
		{"unsupported script for pair", pairProfile, "Привет мир", 100, skipNoTranslatableText},
		{"valid ja in pair", pairProfile, "こんにちは", 100, skipNone},
		{"valid en in pair", pairProfile, "Hello world", 100, skipNone},
		{"valid text for target", targetProfile, "Guten Tag", 100, skipNone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.p.skipReason(tc.input, tc.maxChars)
			if got != tc.want {
				t.Fatalf("skipReason(%q) = %v, want %v", tc.input, got, tc.want)
			}
			_, ok := tc.p.direction(tc.input, tc.maxChars)
			if ok != (tc.want == skipNone) {
				t.Fatalf("direction ok=%v, want %v", ok, tc.want == skipNone)
			}
		})
	}
}

func TestSkipMessageText(t *testing.T) {
	for _, tc := range []struct {
		reason skipReason
		count  int
		limit  int
		label  string
		want   string
	}{
		{
			reason: skipEmpty,
			count:  0,
			limit:  4000,
			label:  "Japanese ↔ English",
			want:   "Nothing to translate: no text was selected or copied.",
		},
		{
			reason: skipTooLong,
			count:  4213,
			limit:  4000,
			label:  "Japanese ↔ English",
			want:   "Nothing was translated: the selection is 4213 characters, over the 4000-character limit. Select less text or raise the limit in Settings (Longest text).",
		},
		{
			reason: skipURL,
			count:  25,
			limit:  4000,
			label:  "Japanese ↔ English",
			want:   "Nothing was translated: the selection is just a URL.",
		},
		{
			reason: skipNoTranslatableText,
			count:  10,
			limit:  4000,
			label:  "Japanese ↔ English",
			want:   "Nothing was translated: this hotkey translates Japanese ↔ English.",
		},
		{
			reason: skipNoTranslatableText,
			count:  10,
			limit:  4000,
			label:  "to Korean",
			want:   "Nothing was translated: this hotkey translates to Korean.",
		},
	} {
		msg := skipMessage(tc.reason, tc.count, tc.limit, tc.label)
		if msg != tc.want {
			t.Errorf("skipMessage(%v) = %q, want %q", tc.reason, msg, tc.want)
		}
	}
}

func TestStartTranslationTooLongNotificationAndThrottle(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.conf.MaxChars = 10
	longText := "123456789012" // 12 chars > 10 limit
	a.capture = func(Clipboard, uintptr) (string, bool, error) {
		return longText, true, nil
	}
	currentTime := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return currentTime }

	// 1. First too-long request: notification shown, error flag set, limit and count included.
	a.startTranslation(0)
	if len(tray.notes) != 1 {
		t.Fatalf("first too-long press must show 1 notification, got %d", len(tray.notes))
	}
	if !tray.lastError() {
		t.Fatal("too-long notification must have isError = true")
	}
	note := tray.lastNote()
	if !strings.Contains(note, "12") || !strings.Contains(note, "10") {
		t.Fatalf("notification must contain count 12 and limit 10, got %q", note)
	}
	if strings.Contains(note, longText) {
		t.Fatalf("notification must not contain source text, got %q", note)
	}
	if tray.status != "Clipboard Translator - text is too long" {
		t.Fatalf("status = %q, want 'Clipboard Translator - text is too long'", tray.status)
	}

	// 2. Second too-long request within throttle window (e.g. 5 seconds later):
	// status still updated, but NO new notification.
	currentTime = currentTime.Add(5 * time.Second)
	tray.status = "untouched"
	a.startTranslation(0)
	if len(tray.notes) != 1 {
		t.Fatalf("throttled too-long press must not send another notification, got %d", len(tray.notes))
	}
	if tray.status != "Clipboard Translator - text is too long" {
		t.Fatalf("status must still be updated while throttled, got %q", tray.status)
	}

	// 3. Third request after 16 seconds (outside 15s throttle window): new notification.
	currentTime = currentTime.Add(11 * time.Second) // 5 + 11 = 16 seconds after first
	a.startTranslation(0)
	if len(tray.notes) != 2 {
		t.Fatalf("too-long request after throttle window must notify, got %d notes", len(tray.notes))
	}
}

func TestStartTranslationRoutineSkips(t *testing.T) {
	for _, tc := range []struct {
		name       string
		text       string
		wantStatus string
	}{
		{"empty selection", "", "Clipboard Translator - nothing to translate (empty selection)"},
		{"whitespace selection", "   ", "Clipboard Translator - nothing to translate (empty selection)"},
		{"url only", "https://example.com/foo", "Clipboard Translator - nothing to translate (URL only)"},
		{"digits/symbols only", "12345", "Clipboard Translator - nothing to translate (not Japanese ↔ English)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tray, _, _, _ := uxApp(t)
			a.capture = func(Clipboard, uintptr) (string, bool, error) {
				return tc.text, true, nil
			}
			a.startTranslation(0)
			if len(tray.notes) != 0 {
				t.Fatalf("routine skip must not notify, got %v", tray.notes)
			}
			if tray.status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", tray.status, tc.wantStatus)
			}
		})
	}
}

func TestTransientStatusReset(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.readyTip = "Clipboard Translator - ready"
	currentTime := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return currentTime }

	a.setTransientStatus("Clipboard Translator - nothing to translate (empty selection)")
	if tray.status != "Clipboard Translator - nothing to translate (empty selection)" {
		t.Fatalf("initial transient status = %q", tray.status)
	}

	// 4 seconds later: still within 8s timeout, not reset yet.
	currentTime = currentTime.Add(4 * time.Second)
	a.tickTransientStatus()
	if tray.status != "Clipboard Translator - nothing to translate (empty selection)" {
		t.Fatalf("status after 4s = %q, should not reset yet", tray.status)
	}

	// 8.1 seconds later: expired, resets to readyTip.
	currentTime = currentTime.Add(4100 * time.Millisecond)
	a.tickTransientStatus()
	if tray.status != a.readyTip {
		t.Fatalf("status after 8.1s = %q, want %q", tray.status, a.readyTip)
	}

	// If translating, transient status must not overwrite translating status.
	a.setTransientStatus("Clipboard Translator - nothing to translate (empty selection)")
	a.translating = true
	currentTime = currentTime.Add(10 * time.Second)
	a.tickTransientStatus()
	if tray.status != "Clipboard Translator - nothing to translate (empty selection)" {
		t.Fatalf("tickTransientStatus must not touch status while translating, got %q", tray.status)
	}
}

func TestHandleResultUnchangedOutcome(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.text = "Hello world"
	g := pending(a)
	a.handleResult(result{
		original:   "Hello world",
		translated: "Hello world",
		generation: g,
	})
	if len(tray.notes) != 0 {
		t.Fatalf("unchanged translation must not notify, got %v", tray.notes)
	}
	wantStatus := "Clipboard Translator - translation is the same as the source; the clipboard still holds the source"
	if tray.status != wantStatus {
		t.Fatalf("status = %q, want %q", tray.status, wantStatus)
	}
	if cb.text != "Hello world" {
		t.Fatalf("clipboard changed: %q", cb.text)
	}
	if len(a.hist.Items()) != 0 {
		t.Fatal("unchanged translation must not be added to history")
	}
}

func TestHandleResultDiscardedOutcome(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.text = "brand new clipboard text"
	g := pending(a)
	a.handleResult(result{
		original:   "old selection",
		translated: "old translation",
		generation: g,
	})
	if len(tray.notes) != 1 {
		t.Fatalf("discarded result must show 1 notification, got %d", len(tray.notes))
	}
	if tray.lastError() {
		t.Fatal("discarded notification must be non-error (isError = false)")
	}
	note := tray.lastNote()
	if !strings.Contains(note, "clipboard changed while translating") {
		t.Fatalf("note = %q, want clipboard changed message", note)
	}
	if cb.text != "brand new clipboard text" {
		t.Fatalf("new clipboard must be preserved, got %q", cb.text)
	}
	if len(a.hist.Items()) != 0 {
		t.Fatal("discarded translation must not be added to history")
	}
}

func TestHandleResultClipboardWriteError(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.err = errors.New("clipboard locked by another process")
	cb.text = "source"
	g := pending(a)
	a.handleResult(result{
		original:   "source",
		translated: "translated",
		generation: g,
	})
	if len(tray.notes) != 1 {
		t.Fatalf("write error must show 1 notification, got %d", len(tray.notes))
	}
	if !tray.lastError() {
		t.Fatal("write error notification must be an error (isError = true)")
	}
	if !strings.Contains(tray.lastNote(), "clipboard locked") {
		t.Fatalf("notification must contain the error, got %q", tray.lastNote())
	}
}

func TestHandleResultStaleGenerationProducesNothing(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.text = "source"
	tray.status = "idle status"
	a.generation = 5
	a.handleResult(result{
		original:   "source",
		translated: "translated",
		generation: 4, // stale
	})
	if len(tray.notes) != 0 {
		t.Fatalf("stale generation must not notify, got %v", tray.notes)
	}
	if tray.status != "idle status" {
		t.Fatalf("stale generation must not change status, got %q", tray.status)
	}
	if cb.text != "source" {
		t.Fatalf("stale generation must not touch clipboard, got %q", cb.text)
	}
}

func TestOutcomeMessagesDoNotLeakText(t *testing.T) {
	source := "SuperSecretSourceText12345"
	translated := "SuperSecretTranslatedText67890"

	messages := []string{
		skipMessage(skipEmpty, 0, 4000, "Japanese ↔ English"),
		skipMessage(skipTooLong, 5000, 4000, "Japanese ↔ English"),
		skipMessage(skipURL, 20, 4000, "Japanese ↔ English"),
		skipMessage(skipNoTranslatableText, 10, 4000, "Japanese ↔ English"),
		skipStatus(skipEmpty, "Japanese ↔ English"),
		skipStatus(skipTooLong, "Japanese ↔ English"),
		skipStatus(skipURL, "Japanese ↔ English"),
		skipStatus(skipNoTranslatableText, "Japanese ↔ English"),
		"Clipboard Translator - translation cancelled",
		"Clipboard Translator - translation is the same as the source; the clipboard still holds the source",
		"The clipboard changed while translating, so the result was dropped and the new clipboard text was kept.",
		"The translation was cut off at the token limit, so no translation was put on the clipboard. Select less text, or raise max_tokens / context_tokens in config.json.",
	}

	for _, msg := range messages {
		if strings.Contains(msg, source) {
			t.Fatalf("message %q must not leak source %q", msg, source)
		}
		if strings.Contains(msg, translated) {
			t.Fatalf("message %q must not leak translated %q", msg, translated)
		}
	}
}

func TestHandleResultAutoPasteFailureLeavesReadyStatus(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.conf.AutoPaste = true
	a.paste = func(uintptr) error { return errors.New("cannot paste into window") }
	cb.text = "source"
	tray.status = "Clipboard Translator - translating... (3s)"
	g := pending(a)
	a.handleResult(result{
		original:      "source",
		translated:    "translation",
		generation:    g,
		window:        100,
		fromSelection: true,
	})
	wantStatus := "Clipboard Translator - translation ready (" + pasteShortcut() + ")"
	if tray.status != wantStatus {
		t.Fatalf("status = %q, want %q", tray.status, wantStatus)
	}
	if strings.Contains(tray.status, "translating") {
		t.Fatalf("failed auto-paste must not leave status on translating, got %q", tray.status)
	}
	if cb.text != "translation" {
		t.Fatalf("translation must stay on clipboard, got %q", cb.text)
	}
}

func TestHandleResultCanceledResetsStatus(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	tray.status = "Clipboard Translator - translating... (2s)"
	a.readyTip = "Clipboard Translator (ready)"
	g := pending(a)
	a.handleResult(result{
		err:        context.Canceled,
		generation: g,
	})
	if tray.status != a.readyTip {
		t.Fatalf("canceled result with matching generation must reset status to %q, got %q", a.readyTip, tray.status)
	}
	if len(tray.notes) != 0 {
		t.Fatalf("canceled result must not show notification, got %v", tray.notes)
	}
}

func TestStartTranslationSupersedingDoesNotShowCancelled(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	cb.text = "first selection"
	g := pending(a)
	tray.statuses = nil
	tray.status = "Clipboard Translator - translating... (1s)"

	var statusDuringCapture string
	a.capture = func(cb Clipboard, win uintptr) (string, bool, error) {
		statusDuringCapture = tray.status
		return "second text to translate", true, nil
	}
	a.conf.Profiles = []Profile{{Languages: []string{"ja", "en"}}}
	a.startTranslation(0)

	if statusDuringCapture == "Clipboard Translator - translation cancelled" {
		t.Fatalf("superseding translation must not show cancelled status during capture, got %q", statusDuringCapture)
	}
	for _, s := range tray.statuses {
		if strings.Contains(s, "cancelled") {
			t.Fatalf("tray status must never contain 'cancelled' when superseding, got %v", tray.statuses)
		}
	}
	if a.generation == g {
		t.Fatal("generation must advance when superseded")
	}
}

func TestTransientResetDoesNotOverwriteServerRestartStatus(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.readyTip = "Clipboard Translator (ready)"
	currentTime := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return currentTime }

	// 1. User cancels or triggers a transient status.
	a.setTransientStatus("Clipboard Translator - translation cancelled")

	// 2. Server restart path sets status.
	a.cancelTransientStatus()
	serverStatus := "Clipboard Translator - server stopped, restarting..."
	tray.SetStatus(serverStatus)

	// 3. Time advances past the 8-second mark.
	currentTime = currentTime.Add(10 * time.Second)
	a.tickTransientStatus()

	// 4. Status must NOT be overwritten by readyTip.
	if tray.status != serverStatus {
		t.Fatalf("server restart status was overwritten by transient reset: got %q, want %q", tray.status, serverStatus)
	}
}

func TestCaptureFailureDisarmsTransientTimer(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.readyTip = "Clipboard Translator (ready)"
	currentTime := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return currentTime }

	// Set a transient status
	a.setTransientStatus("Clipboard Translator - text is too long")

	// Set capture to fail
	a.capture = func(cb Clipboard, win uintptr) (string, bool, error) {
		return "", false, errors.New("cannot capture focus changed")
	}
	a.conf.Profiles = []Profile{{Languages: []string{"ja", "en"}}}
	a.startTranslation(0)

	if tray.status != a.readyTip {
		t.Fatalf("status after capture failure = %q, want %q", tray.status, a.readyTip)
	}
	if !a.statusResetAt.IsZero() {
		t.Fatalf("statusResetAt must be zero (timer disarmed), got %v", a.statusResetAt)
	}

	// Change status to something else and advance time past 8s; verify it is not clobbered
	tray.SetStatus("Clipboard Translator - server stopped, restarting...")
	currentTime = currentTime.Add(10 * time.Second)
	a.tickTransientStatus()
	if tray.status != "Clipboard Translator - server stopped, restarting..." {
		t.Fatalf("transient timer fired after capture failure: status = %q", tray.status)
	}
}

func TestHandleResultBilingualOutput(t *testing.T) {
	a, tray, cb, pastes, _ := uxApp(t)
	a.conf.NotifyOnComplete = true

	// Original text with trailing CRLF
	original := "Hello world\r\n"
	translated := "こんにちは世界"
	cb.text = original
	g := pending(a)

	a.handleResult(result{
		original:      original,
		translated:    translated,
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		output:        OutputBilingual,
	})

	wantClipboard := "Hello world\nこんにちは世界"
	if cb.text != wantClipboard {
		t.Fatalf("clipboard = %q, want %q", cb.text, wantClipboard)
	}

	// History must store Source and Translated separately (plain translation, not bilingual)
	items := a.hist.Items()
	if len(items) != 1 {
		t.Fatalf("history length = %d, want 1", len(items))
	}
	if items[0].Source != original || items[0].Translated != translated {
		t.Fatalf("history entry = %+v, want Source=%q Translated=%q", items[0], original, translated)
	}

	if a.translating {
		t.Fatal("translating flag must be cleared")
	}
	if *pastes != 0 {
		t.Fatal("auto-paste is off and must not paste")
	}

	// Notifications must stay free of source and translated text
	for _, text := range []string{tray.lastTitle(), tray.lastNote(), tray.status} {
		if strings.Contains(text, "Hello") {
			t.Errorf("text %q contains source text", text)
		}
		if strings.Contains(text, "こんにちは") {
			t.Errorf("text %q contains translated text", text)
		}
	}
}

func TestHandleResultBilingualReplaceIfUnchanged(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	original := "Hello"
	translated := "こんにちは"

	// User changed clipboard text while translation was running
	cb.text = "user copied new text"
	g := pending(a)

	a.handleResult(result{
		original:      original,
		translated:    translated,
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		output:        OutputBilingual,
	})

	if cb.text != "user copied new text" {
		t.Fatalf("clipboard was modified: %q", cb.text)
	}
	if !strings.Contains(tray.lastNote(), "clipboard changed while translating") {
		t.Fatalf("notification should mention clipboard changed: %q", tray.lastNote())
	}
	for _, text := range []string{tray.lastTitle(), tray.lastNote()} {
		if strings.Contains(text, original) || strings.Contains(text, translated) {
			t.Errorf("notification %q leaked source or translated text", text)
		}
	}
}

func TestHandleResultWhitespaceOnlyRewriteReplaces(t *testing.T) {
	a, _, cb, _, _ := uxApp(t)
	original := "Hello\r\n"
	translated := "こんにちは"

	// Clipboard was rewritten by another app to normalize CRLF to LF and trim trailing space
	cb.text = "Hello\n  "
	g := pending(a)

	a.handleResult(result{
		original:      original,
		translated:    translated,
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
	})

	if cb.text != translated {
		t.Fatalf("whitespace-only rewrite should be replaced with translation, got %q", cb.text)
	}
}

func TestHandleResultRealChangeDiscardsWithDiagnostics(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	original := "Hello"
	translated := "こんにちは"

	// User changed clipboard text to something different
	cb.text = "Hello world"
	g := pending(a)

	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOutput)

	capturedAt := a.timeNow().Add(-120 * time.Millisecond)

	a.handleResult(result{
		original:      original,
		translated:    translated,
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		capturedAt:    capturedAt,
	})

	if cb.text != "Hello world" {
		t.Fatalf("clipboard was modified: %q", cb.text)
	}
	if !strings.Contains(tray.lastNote(), "clipboard changed while translating") {
		t.Fatalf("notification should mention clipboard changed: %q", tray.lastNote())
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "clipboard text changed during translation; result discarded") {
		t.Fatalf("missing discard log line in: %q", logOutput)
	}
	if !strings.Contains(logOutput, "clipboard change diagnostics:") {
		t.Fatalf("missing diagnostics line in: %q", logOutput)
	}
	if !strings.Contains(logOutput, "expected_len=5") || !strings.Contains(logOutput, "current_len=11") ||
		!strings.Contains(logOutput, "first_diff=5") || !strings.Contains(logOutput, "ws_only=false") ||
		!strings.Contains(logOutput, "prefix=true") {
		t.Fatalf("unexpected diagnostics line content: %q", logOutput)
	}
	if strings.Contains(logOutput, original) || strings.Contains(logOutput, cb.text) || strings.Contains(logOutput, translated) {
		t.Fatalf("diagnostics log leaked clipboard text: %q", logOutput)
	}
}

func TestHandleResultBilingualTranslationUnchanged(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	original := "Same text"
	cb.text = original
	g := pending(a)

	a.handleResult(result{
		original:      original,
		translated:    original, // model returned unchanged text
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		output:        OutputBilingual,
	})

	if cb.text != original {
		t.Fatalf("clipboard changed when translation was identical: %q", cb.text)
	}
	if !strings.Contains(tray.status, "translation is the same as the source") {
		t.Fatalf("status should note translation is unchanged: %q", tray.status)
	}
}

func TestHandleResultBilingualTargetOnly(t *testing.T) {
	a, _, cb, _, _ := uxApp(t)
	original := "Good morning\n"
	translated := "좋은 아침"
	cb.text = original
	g := pending(a)

	a.handleResult(result{
		original:      original,
		translated:    translated,
		generation:    g,
		direction:     languages.Direction{Target: "ko"},
		window:        100,
		fromSelection: true,
		output:        OutputBilingual,
	})

	want := "Good morning\n좋은 아침"
	if cb.text != want {
		t.Fatalf("target-only bilingual clipboard = %q, want %q", cb.text, want)
	}
}

func TestHandleResultMultipleProfilesDifferentModes(t *testing.T) {
	a, _, cb, _, _ := uxApp(t)

	// Result from profile 0 (replace mode)
	cb.text = "Hello\r\n"
	g := pending(a)
	a.handleResult(result{
		original:      "Hello\r\n",
		translated:    "こんにちは",
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		output:        OutputReplace,
	})
	if cb.text != "こんにちは" {
		t.Fatalf("replace mode clipboard = %q, want %q", cb.text, "こんにちは")
	}

	// Result from profile 1 (bilingual mode)
	cb.text = "Good night\r\n"
	g = pending(a)
	a.handleResult(result{
		original:      "Good night\r\n",
		translated:    "おやすみ",
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		output:        OutputBilingual,
	})
	wantBilingual := "Good night\nおやすみ"
	if cb.text != wantBilingual {
		t.Fatalf("bilingual mode clipboard = %q, want %q", cb.text, wantBilingual)
	}
}

func TestHandleResultBilingualAutoPaste(t *testing.T) {
	a, tray, cb, pastes, _ := uxApp(t)
	a.conf.AutoPaste = true

	original := "Select and translate\r\n"
	translated := "選択して翻訳"
	cb.text = original
	g := pending(a)

	a.handleResult(result{
		original:      original,
		translated:    translated,
		generation:    g,
		direction:     languages.Direction{Source: "en", Target: "ja"},
		window:        100,
		fromSelection: true,
		output:        OutputBilingual,
	})

	wantClipboard := "Select and translate\n選択して翻訳"
	if cb.text != wantClipboard {
		t.Fatalf("clipboard = %q, want %q", cb.text, wantClipboard)
	}
	if *pastes != 1 {
		t.Fatalf("auto-paste should paste under matching window, pastes = %d", *pastes)
	}
	if !strings.Contains(tray.status, "translation pasted") {
		t.Fatalf("status = %q, want translation pasted", tray.status)
	}
	for _, text := range []string{tray.lastTitle(), tray.lastNote(), tray.status} {
		if strings.Contains(text, "Select") || strings.Contains(text, "選択") {
			t.Errorf("status/note %q leaked source or translated text", text)
		}
	}
}
