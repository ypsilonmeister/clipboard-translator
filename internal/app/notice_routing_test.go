package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/update"
)

// Representative rows of the macOS notice taxonomy (Issue #67). Only the macOS
// tray reads the classification; these tests keep it from drifting.

var (
	hotkeyOutcome = platform.NoticeOptions{Origin: platform.OriginHotkey, ID: platform.NoticeIDHotkey}
	statusOnly    = platform.NoticeOptions{Kind: platform.NoticeStatusOnly}
)

// plainTray has only the Tray methods, like the Windows and Linux trays.
type plainTray struct {
	fakeTray
	plain []string
}

func (p *plainTray) Notify(title, msg string, isError bool) { p.plain = append(p.plain, msg) }

func TestNoticeWithoutNoticeTrayIsPlainNotify(t *testing.T) {
	tray := &plainTray{}
	var tr platform.Tray = struct{ platform.Tray }{tray} // hides NotifyWith
	if _, ok := tr.(platform.NoticeTray); ok {
		t.Fatal("test setup: the wrapped tray must not be a NoticeTray")
	}
	a := &app{tray: tr}
	a.notify("Clipboard Translator", "Copied.", false, statusOnly)
	a.notify("Clipboard Translator", "failed", true, hotkeyOutcome)
	if strings.Join(tray.plain, ",") != "Copied.,failed" {
		t.Fatalf("Windows/Linux must get every notice as plain Notify, got %v", tray.plain)
	}
	if s, pending := a.notificationStatus(); s != "n/a" || pending {
		t.Fatalf("notification status without a NotificationTray = %q %v", s, pending)
	}
}

func TestNoticeHotkeyVersusMenuOrigin(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.conf.MaxChars = 3
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "too long text", true, nil }
	a.startTranslation(0)
	if len(tray.opts) != 1 || tray.lastOpts() != hotkeyOutcome {
		t.Fatalf("a hotkey skip must be a hotkey outcome, got %+v", tray.opts)
	}
	// The tray's Translate clipboard runs the same pipeline but is a menu click:
	// it must never count as a hotkey outcome (no automatic permission request).
	a.lastSkipReason = config.SkipNone // past the 15 s throttle
	cb.text = "also too long"
	a.startClipboardTranslation()
	if len(tray.opts) != 2 || tray.lastOpts().Origin != platform.OriginOther || tray.lastOpts().ID != platform.NoticeIDHotkey {
		t.Fatalf("Translate clipboard skip: %+v", tray.opts)
	}
}

func TestNoticeCompletionCarriesTheJobOrigin(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	a.conf.NotifyOnComplete = true
	a.conf.Profiles = []config.Profile{{Hotkey: "Ctrl+Alt+T", Action: config.ActionTidy, Rules: []string{"width"}}}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "ＡＢＣ", true, nil }
	cb.text = "ＡＢＣ"
	a.startTranslation(0)
	want := platform.NoticeOptions{Origin: platform.OriginHotkey, ID: platform.NoticeIDCompletion}
	if len(tray.opts) != 1 || tray.lastOpts() != want {
		t.Fatalf("completion notice of a hotkey press: %+v (notes %v)", tray.opts, tray.notes)
	}
	cb.text = "ＤＥＦ"
	a.startClipboardTranslation()
	if len(tray.opts) != 2 || tray.lastOpts().Origin != platform.OriginOther || tray.lastOpts().ID != platform.NoticeIDCompletion {
		t.Fatalf("completion notice of Translate clipboard: %+v", tray.opts)
	}
	// A failure result keeps the origin of its job too.
	g := pending(a)
	a.handleResult(result{generation: g, err: errors.New("boom"), origin: platform.OriginHotkey})
	if tray.lastOpts() != hotkeyOutcome || tray.lastTitle() != "Translation failed" {
		t.Fatalf("translation failure: %+v %q", tray.lastOpts(), tray.lastTitle())
	}
}

func TestNoticeStatusOnlyRows(t *testing.T) {
	a, tray, _, _, _ := uxApp(t)
	a.finishHistoryCopy("Hello")
	if !strings.HasPrefix(tray.lastNote(), "Copied.") || tray.lastOpts().Kind != platform.NoticeStatusOnly {
		t.Fatalf("history copy: %q %+v", tray.lastNote(), tray.lastOpts())
	}
	// Extra hotkeys that only hit this build's limit repeat at every launch.
	a.conf.Profiles = []config.Profile{{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}}, {Hotkey: "Ctrl+Alt+J", Languages: []string{"ja", "en"}}}
	a.hotkeyLimit = 1
	a.registerStartupHotkey()
	a.noteExtraFailures()
	if tray.lastOpts() != (platform.NoticeOptions{Kind: platform.NoticeStatusOnly, ID: platform.NoticeIDStartup}) {
		t.Fatalf("limit-only line: %+v %q", tray.lastOpts(), tray.lastNote())
	}
	// A real conflict is a notification.
	a.hotkeyLimit = 5
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
		if spec == "Ctrl+Alt+J" {
			return nil, errors.New("already used")
		}
		return fakeHotkey{spec}, nil
	}
	a.registerStartupHotkey()
	a.noteExtraFailures()
	if tray.lastOpts() != (platform.NoticeOptions{ID: platform.NoticeIDStartup}) {
		t.Fatalf("conflict line: %+v %q", tray.lastOpts(), tray.lastNote())
	}
}

func TestNoticeUpdateResults(t *testing.T) {
	a, tray, _ := newTestApp(t)
	old := version
	version = "0.5.1"
	defer func() { version = old }()
	a.onUpdateResult(updateResult{info: update.ReleaseInfo{Tag: "v0.6.0"}})
	a.onUpdateResult(updateResult{err: errors.New("offline")})
	for i, o := range tray.opts {
		if o != (platform.NoticeOptions{ID: platform.NoticeIDUpdate}) {
			t.Fatalf("update notice %d (%q): %+v", i, tray.notes[i], o)
		}
	}
	if len(tray.opts) != 2 {
		t.Fatalf("notes: %v", tray.notes)
	}
}

func TestNoticeServerRestartSharesOneID(t *testing.T) {
	a, tray, errs := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.sup.Stop()
	if !a.restartServer() {
		t.Fatalf("restart failed: %v", *errs)
	}
	if len(tray.opts) != 2 {
		t.Fatalf("notes: %v", tray.notes)
	}
	for i, o := range tray.opts {
		if o != (platform.NoticeOptions{ID: platform.NoticeIDServer}) {
			t.Fatalf("server notice %d (%q): %+v", i, tray.notes[i], o)
		}
	}
}

// An engine switch started from the tray has nothing else to report it; one
// started on the Settings page is reported by the page (settingsNotice).
// Success is status only either way (maintainer decision on Issue #67).
func TestNoticeEngineSwitchTrayVersusSettings(t *testing.T) {
	a, tray, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	a.switchBackend("cuda")
	if !strings.HasPrefix(tray.lastNote(), "Translation engine:") || tray.lastOpts() != wantSwitchSuccess {
		t.Fatalf("tray switch success: %q %+v", tray.lastNote(), tray.lastOpts())
	}
	a.switchBackend("vulkan") // cannot start; cuda stays
	if !strings.Contains(tray.lastNote(), "keeping the previous engine") || tray.lastOpts() != (platform.NoticeOptions{ID: platform.NoticeIDEngine}) {
		t.Fatalf("tray switch failure: %q %+v", tray.lastNote(), tray.lastOpts())
	}
	reply := make(chan settings.Reply, 2)
	a.startEngineSwitch(settings.Request{Action: "engine", Arg: "vulkan", Reply: reply})
	if !strings.Contains(tray.lastNote(), "keeping the previous engine") || tray.lastOpts().Kind != platform.NoticeStatusOnly {
		t.Fatalf("Settings switch failure: %q %+v", tray.lastNote(), tray.lastOpts())
	}
	if a.settingsNotice == "" {
		t.Fatal("the page must report the failure itself")
	}
	if a.fromSettings {
		t.Fatal("fromSettings must be cleared after the switch")
	}
}

// wantSwitchSuccess is written out here, not taken from engineSuccess, so a
// change of that production value fails the tests (maintainer decision on
// Issue #67, Q2: switch success is status only).
var wantSwitchSuccess = platform.NoticeOptions{Kind: platform.NoticeStatusOnly, ID: platform.NoticeIDEngine}

func TestNoticeModelSwitchSuccessIsStatusOnly(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[1])
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := a.switchModel(models.Catalog[1]); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tray.lastNote(), "Model: ") || tray.lastOpts() != wantSwitchSuccess {
		t.Fatalf("model switch success: %q %+v", tray.lastNote(), tray.lastOpts())
	}
}

// The translation worker goroutine, not the test, supplies the origin of a
// failed result (app.go, the result sent on a.results): a hotkey job's
// failure is a hotkey outcome, a Translate clipboard job's is not.
func TestNoticeAsyncFailureOriginFromWorker(t *testing.T) {
	a, tray, cb, _, _ := uxApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal model error", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	a.endpoint = srv.URL
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "Hello", true, nil }
	cb.text = "Hello"
	wait := func() result {
		t.Helper()
		select {
		case res := <-a.results:
			return res
		case <-time.After(10 * time.Second):
			t.Fatal("no result from the worker")
		}
		return result{}
	}
	a.startTranslation(0)
	res := wait()
	if res.err == nil || res.origin != platform.OriginHotkey {
		t.Fatalf("hotkey job result: err=%v origin=%v", res.err, res.origin)
	}
	a.handleResult(res)
	if tray.lastTitle() != "Translation failed" || tray.lastOpts() != (platform.NoticeOptions{Origin: platform.OriginHotkey, ID: platform.NoticeIDHotkey}) {
		t.Fatalf("hotkey job failure notice: %q %+v", tray.lastTitle(), tray.lastOpts())
	}
	cb.text = "Hello again"
	a.startClipboardTranslation()
	res = wait()
	if res.err == nil || res.origin != platform.OriginOther {
		t.Fatalf("Translate clipboard job result: err=%v origin=%v", res.err, res.origin)
	}
	a.handleResult(res)
	if tray.lastTitle() != "Translation failed" || tray.lastOpts() != (platform.NoticeOptions{Origin: platform.OriginOther, ID: platform.NoticeIDHotkey}) {
		t.Fatalf("Translate clipboard failure notice: %q %+v", tray.lastTitle(), tray.lastOpts())
	}
}

// fakeNotificationTray stands in for the macOS tray's permission side.
type fakeNotificationTray struct {
	fakeTray
	status  platform.AuthStatus
	pending bool
	reads   int
	stored  string
	save    func(string) error
}

func (f *fakeNotificationTray) SetNotificationMemory(stored string, save func(string) error) {
	f.stored, f.save = stored, save
}
func (f *fakeNotificationTray) NotificationStatus() platform.AuthStatus {
	f.reads++
	return f.status
}
func (f *fakeNotificationTray) NotificationStatusPending() bool { return f.pending }

func TestNotificationMemoryAndSettingsState(t *testing.T) {
	a, _, _ := newTestApp(t)
	if got := a.settingsState().Notifications; got != "n/a" {
		t.Fatalf("Windows/Linux state = %q, want n/a", got)
	}
	nt := &fakeNotificationTray{status: platform.AuthDenied}
	a.tray = nt
	a.conf.MacNotifications = "asked"
	a.setupNotificationMemory()
	if nt.stored != "asked" || nt.save == nil {
		t.Fatalf("memory handed to the tray: %q", nt.stored)
	}
	if err := nt.save("denied"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(a.dir)
	if err != nil || c.MacNotifications != "denied" || a.conf.MacNotifications != "denied" {
		t.Fatalf("saved %q / in memory %q, %v", c.MacNotifications, a.conf.MacNotifications, err)
	}
	if got := a.settingsState().Notifications; got != "denied" {
		t.Fatalf("state = %q, want denied", got)
	}
	nt.status, nt.pending = platform.AuthAuthorized, true
	if st := a.settingsState(); st.Notifications != "allowed" || !st.NotificationsPending {
		t.Fatalf("state = %q pending=%v, want allowed and pending", st.Notifications, st.NotificationsPending)
	}
	if nt.reads == 0 {
		t.Fatal("the Settings state must go through NotificationStatus (which refreshes on macOS)")
	}
}

func TestSettingsPageNotificationHint(t *testing.T) {
	b, err := os.ReadFile("../settings/web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	html, err := os.ReadFile("../settings/web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `id="notifications-denied" class="hint" hidden`) {
		t.Error("the page needs the hidden denied hint under the completion checkbox")
	}
	if !strings.Contains(js, "notificationHint(state.notifications)") {
		t.Error("the page must derive the hint from state.notifications")
	}
	// A permission read in flight (it may have changed in System Settings):
	// the page asks once more, a bounded number of times.
	if !strings.Contains(js, "if (state.notifications_pending) recheckNotifications();") || !strings.Contains(js, "notifyRechecks >= 5") {
		t.Error("the page must read the state again, a bounded number of times, while notifications_pending")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; the string checks above ran")
	}
	cmd := exec.Command(nodePath, "-e", `
const fs = require('fs');
const js = fs.readFileSync(process.argv[1], 'utf8');
const match = js.match(/function notificationHint\([^)]*\)\s*\{[^}]*\}/);
if (!match) throw new Error('notificationHint not found in app.js');
const fn = new Function('return ' + match[0])();
if (!fn('denied').includes('System Settings > Notifications')) throw new Error('denied must show the hint');
for (const s of ['n/a', 'allowed', 'not_asked', 'unknown', 'unavailable', undefined]) {
  if (fn(s) !== '') throw new Error('no hint for ' + s);
}
`, filepath.Join("..", "settings", "web", "app.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node check failed: %v\n%s", err, out)
	}
}

// The readable menu line for an inactive hotkey is derived from the current
// registration and disappears once registration succeeds (Issue #67, Q4).
func TestHotkeyProblemMenuLine(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.hotkeyProblemMenu = true
	taken := true
	a.newHotkey = func(spec string) (platform.Hotkey, error) {
		if taken {
			return nil, errors.New("already used")
		}
		return fakeHotkey{spec}, nil
	}
	a.registerStartupHotkey()
	items := a.buildMenu()
	if !items[0].Disabled || !strings.Contains(items[0].Label, "Ctrl+Alt+E is not active") {
		t.Fatalf("first menu line = %+v", items[0])
	}
	taken = false
	a.registerStartupHotkey()
	for _, it := range a.buildMenu() {
		if strings.Contains(it.Label, "is not active") {
			t.Fatalf("the line must go once the hotkey works: %+v", it)
		}
	}
	a.hotkeyProblemMenu = false // Windows and Linux menus are unchanged
	taken = true
	a.registerStartupHotkey()
	if items := a.buildMenu(); items[0].ID != cmdSettings {
		t.Fatalf("without hotkeyProblemMenu the menu starts with Settings: %+v", items[0])
	}
}
