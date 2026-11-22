package platform

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDecideNotice(t *testing.T) {
	hotkey := NoticeOptions{Origin: OriginHotkey, ID: NoticeIDHotkey}
	other := NoticeOptions{ID: NoticeIDStartup}
	statusOnly := NoticeOptions{Kind: NoticeStatusOnly, Origin: OriginHotkey}
	for _, tc := range []struct {
		name      string
		status    AuthStatus
		available bool
		stored    string
		o         NoticeOptions
		post, req bool
		newStored string
	}{
		{"never asked, hotkey: the one automatic request", AuthNotDetermined, true, "", hotkey, false, true, "asked"},
		{"never asked, startup: no request", AuthNotDetermined, true, "", other, false, false, ""},
		{"already asked (ignored prompt): never again", AuthNotDetermined, true, "asked", hotkey, false, false, "asked"},
		{"authorized, then reset by an update: asks once more", AuthNotDetermined, true, "authorized", hotkey, false, true, "asked"},
		{"authorized-then-reset, background event: no request", AuthNotDetermined, true, "authorized", other, false, false, "authorized"},
		{"denied before, reset: never asked automatically", AuthNotDetermined, true, "denied", hotkey, false, false, "denied"},
		{"unknown stored value: conservative", AuthNotDetermined, true, "maybe", hotkey, false, false, "maybe"},
		{"allowed, hotkey: post", AuthAuthorized, true, "authorized", hotkey, true, false, "authorized"},
		{"allowed, startup: post", AuthAuthorized, true, "", other, true, false, ""},
		{"provisional counts as allowed", AuthProvisional, true, "asked", other, true, false, "asked"},
		{"denied, hotkey: skip, no request", AuthDenied, true, "denied", hotkey, false, false, "denied"},
		{"denied while stored is empty: still no request", AuthDenied, true, "", hotkey, false, false, ""},
		{"status not read yet: skip, no request", AuthUnknown, true, "", hotkey, false, false, ""},
		{"unavailable (unbundled): nothing", AuthUnavailable, false, "", hotkey, false, false, ""},
		{"unavailable even if a status is cached", AuthAuthorized, false, "", hotkey, false, false, ""},
		{"status only, allowed: no post", AuthAuthorized, true, "authorized", statusOnly, false, false, "authorized"},
		{"status only, never asked: no request", AuthNotDetermined, true, "", statusOnly, false, false, ""},
	} {
		post, req, stored := decideNotice(tc.status, tc.available, tc.stored, tc.o)
		if post != tc.post || req != tc.req || stored != tc.newStored {
			t.Errorf("%s: got post=%v request=%v stored=%q, want %v %v %q", tc.name, post, req, stored, tc.post, tc.req, tc.newStored)
		}
	}
}

func TestStoredForStatus(t *testing.T) {
	for _, tc := range []struct {
		status       AuthStatus
		stored, want string
	}{
		{AuthAuthorized, "asked", "authorized"},
		{AuthProvisional, "", "authorized"},
		{AuthDenied, "asked", "denied"},
		{AuthDenied, "authorized", "denied"},
		{AuthNotDetermined, "asked", "asked"},
		{AuthNotDetermined, "authorized", "authorized"},
		{AuthUnknown, "", ""},
	} {
		if got := storedForStatus(tc.status, tc.stored); got != tc.want {
			t.Errorf("storedForStatus(%v, %q) = %q, want %q", tc.status, tc.stored, got, tc.want)
		}
	}
}

// fakeBridge is a native notification center whose callbacks the test
// completes by hand, in any order.
type fakeBridge struct {
	available bool
	status    AuthStatus
	inFlight  bool
	requests  int
	refreshes int
	posts     []string
	sounds    []bool
	failures  int
	last      string
}

func (b *fakeBridge) Available() bool    { return b.available }
func (b *fakeBridge) Status() AuthStatus { return b.status }
func (b *fakeBridge) Refresh()           { b.refreshes++ }
func (b *fakeBridge) Request() bool {
	if b.inFlight {
		return false
	}
	b.inFlight = true
	b.requests++
	return true
}
func (b *fakeBridge) Post(id, title, body string, sound bool) {
	b.posts = append(b.posts, id+"|"+body)
	b.sounds = append(b.sounds, sound)
}
func (b *fakeBridge) Failures() (int, string) { return b.failures, b.last }

// answer completes the pending request as the user's choice would.
func (b *fakeBridge) answer(s AuthStatus) { b.inFlight = false; b.status = s }

type memory struct {
	saved []string
	err   error
}

func (m *memory) save(v string) error {
	if m.err != nil {
		return m.err
	}
	m.saved = append(m.saved, v)
	return nil
}

func newTestNotifier(b *fakeBridge, stored string, m *memory) (*notifier, *[]string) {
	var logs []string
	n := &notifier{bridge: b, lastStatus: AuthUnknown, logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	n.setMemory(stored, m.save)
	return n, &logs
}

var hotkeyNotice = NoticeOptions{Origin: OriginHotkey, ID: NoticeIDHotkey}

func TestNotifierGrantFlowDoesNotReplay(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	m := &memory{}
	n, _ := newTestNotifier(b, "", m)
	n.notice("Clipboard Translator", "startup notice", true, NoticeOptions{ID: NoticeIDStartup})
	if b.requests != 0 || len(b.posts) != 0 {
		t.Fatalf("a startup notice must neither ask nor post: %d %v", b.requests, b.posts)
	}
	n.notice("Clipboard Translator", "too long", true, hotkeyNotice)
	if b.requests != 1 || strings.Join(m.saved, ",") != "asked" {
		t.Fatalf("first hotkey notice: requests=%d saved=%v", b.requests, m.saved)
	}
	n.notice("Clipboard Translator", "too long again", true, hotkeyNotice) // prompt still open
	if b.requests != 1 {
		t.Fatalf("a second hotkey notice while the prompt is open must not ask again")
	}
	b.answer(AuthAuthorized)
	n.sync()
	if len(b.posts) != 0 {
		t.Fatalf("the triggering notice must not be replayed after the grant: %v", b.posts)
	}
	if n.stored != "authorized" || m.saved[len(m.saved)-1] != "authorized" {
		t.Fatalf("stored after grant = %q (%v)", n.stored, m.saved)
	}
	n.notice("Clipboard Translator", "failed", true, hotkeyNotice)
	if len(b.posts) != 1 || b.posts[0] != "hotkey-outcome|failed" || !b.sounds[0] {
		t.Fatalf("after the grant notices are posted: %v %v", b.posts, b.sounds)
	}
}

func TestNotifierDeniedNeverAsksAgain(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	m := &memory{}
	n, logs := newTestNotifier(b, "", m)
	n.notice("t", "one", true, hotkeyNotice)
	b.answer(AuthDenied)
	n.sync()
	for i := 0; i < 3; i++ {
		n.notice("t", "more", true, hotkeyNotice)
	}
	if b.requests != 1 || len(b.posts) != 0 || n.stored != "denied" {
		t.Fatalf("after a denial: requests=%d posts=%v stored=%q", b.requests, b.posts, n.stored)
	}
	skips := 0
	for _, l := range *logs {
		if strings.Contains(l, "not allowed") {
			skips++
		}
	}
	if skips != 1 {
		t.Fatalf("the skip diagnostic is logged once per run, got %d: %v", skips, *logs)
	}
	// After a restart (new notifier, same stored value) and even a reset by an
	// update, a denial is never followed by an automatic request.
	b2 := &fakeBridge{available: true, status: AuthNotDetermined}
	n2, _ := newTestNotifier(b2, n.stored, m)
	n2.notice("t", "x", true, hotkeyNotice)
	if b2.requests != 0 {
		t.Fatalf("restart after denial asked again")
	}
	// The explicit item still asks while the system says not determined...
	if open := n2.explicitAction(); open || b2.requests != 1 {
		t.Fatalf("explicit item with not determined: open=%v requests=%d", open, b2.requests)
	}
	// ...and opens System Settings once denied.
	b2.answer(AuthDenied)
	if !n2.explicitAction() || b2.requests != 1 {
		t.Fatalf("explicit item with denied must open System Settings")
	}
}

func TestNotifierIgnoredPromptAcrossRestart(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	m := &memory{}
	n, _ := newTestNotifier(b, "", m)
	n.notice("t", "x", true, hotkeyNotice)
	// The user ignores the prompt; the app restarts with the stored value.
	n2, _ := newTestNotifier(&fakeBridge{available: true, status: AuthNotDetermined}, n.stored, m)
	n2.notice("t", "x", true, hotkeyNotice)
	if n2.bridge.(*fakeBridge).requests != 0 {
		t.Fatalf("an ignored prompt must not come back automatically")
	}
}

func TestNotifierFailedWriteSuppressesRequest(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	n, logs := newTestNotifier(b, "", &memory{err: errors.New("read-only")})
	n.notice("t", "x", true, hotkeyNotice)
	if b.requests != 0 {
		t.Fatalf("without a durable 'asked' the automatic request must not happen")
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "read-only") {
		t.Fatalf("the write failure is logged: %v", *logs)
	}
}

func TestNotifierRevokedWhileRunning(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthAuthorized}
	m := &memory{}
	n, _ := newTestNotifier(b, "authorized", m)
	n.notice("t", "a", false, NoticeOptions{})
	b.status = AuthDenied // turned off in System Settings
	n.sync()
	n.notice("t", "b", false, NoticeOptions{})
	b.status = AuthAuthorized // and on again
	n.sync()
	n.notice("t", "c", false, NoticeOptions{})
	if strings.Join(b.posts, ",") != "misc|a,misc|c" || b.requests != 0 {
		t.Fatalf("posts=%v requests=%d", b.posts, b.requests)
	}
	if strings.Join(m.saved, ",") != "denied,authorized" {
		t.Fatalf("saved=%v", m.saved)
	}
}

func TestNotifierUnavailableAndStatusOnly(t *testing.T) {
	b := &fakeBridge{available: false}
	n, _ := newTestNotifier(b, "", &memory{})
	n.notice("t", "x", true, hotkeyNotice)
	n.sync()
	if b.requests != 0 || len(b.posts) != 0 || b.refreshes != 0 {
		t.Fatalf("an unavailable center must never be touched: %+v", b)
	}
	if lines := n.menuLines(5); len(lines) != 1 || !lines[0].Disabled {
		t.Fatalf("unavailable menu: %+v", lines)
	}
	if n.explicitAction() {
		t.Fatalf("unavailable: nothing to open")
	}
	b2 := &fakeBridge{available: true, status: AuthAuthorized}
	n2, logs := newTestNotifier(b2, "authorized", &memory{})
	n2.notice("t", "Copied.", false, NoticeOptions{Kind: NoticeStatusOnly})
	if len(b2.posts) != 0 || strings.Contains(strings.Join(*logs, "\n"), "not allowed") {
		t.Fatalf("status-only notice posted or logged a skip: %v %v", b2.posts, *logs)
	}
}

func TestNotifierLogsAsyncFailures(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthAuthorized}
	n, logs := newTestNotifier(b, "authorized", &memory{})
	b.failures, b.last = 2, "post: UNErrorDomain 1"
	n.sync()
	n.sync()
	got := 0
	for _, l := range *logs {
		if strings.Contains(l, "UNErrorDomain 1") {
			got++
		}
	}
	if got != 1 || b.refreshes != 1 {
		t.Fatalf("failures are logged once per change and refresh the status: %d %d %v", got, b.refreshes, *logs)
	}
}

func TestNotifierMenuLines(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthDenied}
	n, _ := newTestNotifier(b, "", &memory{})
	lines := n.menuLines(5)
	if len(lines) != 2 || lines[0].ID != 5 || lines[0].Label != "Notifications..." || lines[1].Checked {
		t.Fatalf("denied menu: %+v", lines)
	}
	b.status = AuthAuthorized
	if lines := n.menuLines(5); !lines[1].Checked {
		t.Fatalf("allowed menu: %+v", lines)
	}
}
