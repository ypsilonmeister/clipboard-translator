package platform

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The full origin x kind x status x availability x marker matrix. The
// expectations are the maintainer decision on Issue #67 spelled out
// directly, not derived from decideNotice.
func TestDecideNoticeMatrix(t *testing.T) {
	statuses := []AuthStatus{AuthUnavailable, AuthUnknown, AuthNotDetermined, AuthDenied, AuthAuthorized, AuthProvisional}
	// "authorized" and "denied" were written by development builds of this
	// branch; like any non-empty value they mean the request was used.
	markers := []string{"", "asked", "authorized", "denied", "maybe"}
	kinds := []NoticeKind{NoticeNotify, NoticeStatusOnly}
	origins := []NoticeOrigin{OriginHotkey, OriginOther}
	rows, requests := 0, 0
	for _, status := range statuses {
		for _, available := range []bool{true, false} {
			for _, stored := range markers {
				for _, kind := range kinds {
					for _, origin := range origins {
						o := NoticeOptions{Kind: kind, Origin: origin, ID: NoticeIDHotkey}
						wantPost := available && kind == NoticeNotify && (status == AuthAuthorized || status == AuthProvisional)
						wantReq := available && kind == NoticeNotify && origin == OriginHotkey && status == AuthNotDetermined && stored == ""
						wantStored := stored
						if wantReq {
							wantStored = "asked"
						}
						post, req, got := decideNotice(status, available, stored, o)
						rows++
						if req {
							requests++
						}
						if post != wantPost || req != wantReq || got != wantStored {
							t.Errorf("status=%v available=%v stored=%q kind=%v origin=%v: got post=%v request=%v stored=%q, want %v %v %q",
								status, available, stored, kind, origin, post, req, got, wantPost, wantReq, wantStored)
						}
					}
				}
			}
		}
	}
	if rows != 6*2*5*2*2 || requests != 1 {
		t.Fatalf("matrix rows=%d, combinations that ask=%d (want 240 and exactly 1)", rows, requests)
	}
}

// fakeBridge is a native notification center whose callbacks the test
// completes by hand, in any order. status is the cached snapshot (what
// Status returns); system is what macOS would answer to a read now.
type fakeBridge struct {
	available bool
	status    AuthStatus
	system    AuthStatus
	hasSystem bool
	inFlight  bool
	requests  int
	refreshes int
	failRead  bool
	queued    []fakeRead
	applied   int64
	tickets   int64
	posts     []string
	sounds    []bool
	failures  int
	last      string
}

type fakeRead struct {
	ticket int64
	value  AuthStatus
}

func (b *fakeBridge) Available() bool    { return b.available }
func (b *fakeBridge) Status() AuthStatus { return b.status }
func (b *fakeBridge) Refresh() int64 {
	b.refreshes++
	if b.failRead {
		b.failures++
		b.last = "refresh: NSInternalInconsistencyException 0"
		return 0
	}
	b.tickets++
	v := b.status
	if b.hasSystem {
		v = b.system
	}
	b.queued = append(b.queued, fakeRead{b.tickets, v})
	return b.tickets
}
func (b *fakeBridge) Applied() int64 { return b.applied }
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

// answer completes the pending request as the user's choice would, with the
// answer already in the snapshot (the native side clears the in-flight bit
// only after its refresh).
func (b *fakeBridge) answer(s AuthStatus) { b.inFlight = false; b.status = s; b.setSystem(s) }

// setSystem changes the permission outside the app (System Settings, a reset).
func (b *fakeBridge) setSystem(s AuthStatus) { b.system, b.hasSystem = s, true }

// land delivers the oldest outstanding read; a late answer to an older
// ticket never overwrites a newer one, as in notify_darwin.m.
func (b *fakeBridge) land() {
	if len(b.queued) == 0 {
		return
	}
	r := b.queued[0]
	b.queued = b.queued[1:]
	if r.ticket > b.applied {
		b.applied, b.status = r.ticket, r.value
	}
}

// settle delivers every outstanding read, including the ones sync starts
// in response, as the system would over a few pumps.
func settle(n *notifier, b *fakeBridge) {
	for i := 0; i < 10; i++ {
		n.sync()
		if len(b.queued) == 0 {
			return
		}
		for len(b.queued) > 0 {
			b.land()
		}
	}
}

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

// last is what a restarted app would read from config.json.
func (m *memory) last() string {
	if len(m.saved) == 0 {
		return ""
	}
	return m.saved[len(m.saved)-1]
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestNotifier(b *fakeBridge, stored string, m *memory) (*notifier, *[]string) {
	n, logs, _ := newClockedNotifier(b, stored, m)
	return n, logs
}

func newClockedNotifier(b *fakeBridge, stored string, m *memory) (*notifier, *[]string, *clock) {
	var logs []string
	c := &clock{t: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)}
	n := &notifier{bridge: b, lastStatus: AuthUnknown, now: c.now,
		logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	n.setMemory(stored, m.save)
	return n, &logs, c
}

func count(logs []string, sub string) int {
	k := 0
	for _, l := range logs {
		if strings.Contains(l, sub) {
			k++
		}
	}
	return k
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
	// The marker is not replaced by the observed grant (Codex review P1).
	if n.stored != "asked" || strings.Join(m.saved, ",") != "asked" {
		t.Fatalf("marker after grant = %q (saved %v), want asked only", n.stored, m.saved)
	}
	n.notice("Clipboard Translator", "failed", true, hotkeyNotice)
	if len(b.posts) != 1 || b.posts[0] != "hotkey-outcome|failed" || !b.sounds[0] {
		t.Fatalf("after the grant notices are posted: %v %v", b.posts, b.sounds)
	}
}

// Codex review P1: grant -> reset (while running and across restarts) must
// not re-arm the automatic request. Exactly one automatic request, ever.
func TestNotifierAutomaticRequestOnceAcrossGrantResetRestart(t *testing.T) {
	m := &memory{}
	automatic := 0
	hotkeys := func(n *notifier, b *fakeBridge) {
		before := b.requests
		for i := 0; i < 3; i++ {
			n.notice("t", "too long", true, hotkeyNotice)
			n.sync()
		}
		automatic += b.requests - before
	}
	// Run 1: the one automatic request, granted.
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	n, _ := newTestNotifier(b, "", m)
	hotkeys(n, b)
	b.answer(AuthAuthorized)
	n.sync()
	hotkeys(n, b)
	// macOS forgets the answer while the app runs (update, re-sign).
	b.setSystem(AuthNotDetermined)
	b.status = AuthNotDetermined
	n.sync()
	hotkeys(n, b)
	// Run 2: restart with what config.json holds; still reset.
	b2 := &fakeBridge{available: true, status: AuthNotDetermined}
	n2, _ := newTestNotifier(b2, m.last(), m)
	hotkeys(n2, b2)
	// The user asks explicitly and grants again; then another reset and restart.
	n2.explicitAction()
	settle(n2, b2)
	if b2.requests != 1 {
		t.Fatalf("the explicit item still asks while not determined: requests=%d", b2.requests)
	}
	b2.answer(AuthAuthorized)
	n2.sync()
	hotkeys(n2, b2)
	b3 := &fakeBridge{available: true, status: AuthNotDetermined}
	n3, _ := newTestNotifier(b3, m.last(), m)
	hotkeys(n3, b3)
	if automatic != 1 {
		t.Fatalf("automatic requests across grant/reset/restart = %d, want exactly 1", automatic)
	}
	if strings.Join(m.saved, ",") != "asked" {
		t.Fatalf("the marker is written once and never changes: %v", m.saved)
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
	if b.requests != 1 || len(b.posts) != 0 || n.stored != "asked" {
		t.Fatalf("after a denial: requests=%d posts=%v stored=%q", b.requests, b.posts, n.stored)
	}
	if k := count(*logs, "not allowed"); k != 1 {
		t.Fatalf("the skip diagnostic is logged once per run, got %d: %v", k, *logs)
	}
	// After a restart and even a reset by an update, no automatic request.
	b2 := &fakeBridge{available: true, status: AuthNotDetermined}
	opened := 0
	n2, _ := newTestNotifier(b2, m.last(), m)
	n2.openSettings = func() { opened++ }
	n2.notice("t", "x", true, hotkeyNotice)
	if b2.requests != 0 {
		t.Fatalf("restart after denial asked again")
	}
	// The explicit item still asks while the system says not determined...
	n2.explicitAction()
	settle(n2, b2)
	if opened != 0 || b2.requests != 1 {
		t.Fatalf("explicit item with not determined: opened=%d requests=%d", opened, b2.requests)
	}
	// ...and opens System Settings once denied.
	b2.answer(AuthDenied)
	n2.explicitAction()
	settle(n2, b2)
	if opened != 1 || b2.requests != 1 {
		t.Fatalf("explicit item with denied must open System Settings: opened=%d requests=%d", opened, b2.requests)
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

// Codex review P2: a change made in System Settings while the app runs is
// picked up from the Settings page path (and from a skipped notice) without
// a tray menu or a relaunch; reads coalesce.
func TestNotifierExternalEnableAndRevoke(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthDenied}
	m := &memory{}
	n, _, c := newClockedNotifier(b, "asked", m)
	b.setSystem(AuthAuthorized) // turned on in System Settings
	if s := n.settingsStatus(); s != AuthDenied || !n.pending() || b.refreshes != 1 {
		t.Fatalf("first page read: %v pending=%v refreshes=%d (want the cached denied and one read started)", s, n.pending(), b.refreshes)
	}
	n.settingsStatus()
	n.settingsStatus()
	if b.refreshes != 1 {
		t.Fatalf("page reads while one is in flight must coalesce: refreshes=%d", b.refreshes)
	}
	b.land()
	n.sync()
	if !n.pending() || b.refreshes != 2 {
		// The coalesced requests are one more read after the first.
		t.Fatalf("after the first answer: pending=%v refreshes=%d", n.pending(), b.refreshes)
	}
	b.land()
	n.sync()
	if s := n.settingsStatus(); s != AuthAuthorized || n.pending() || b.refreshes != 2 {
		t.Fatalf("fresh read: %v pending=%v refreshes=%d", s, n.pending(), b.refreshes)
	}
	n.notice("t", "a", false, NoticeOptions{})
	if strings.Join(b.posts, ",") != "misc|a" {
		t.Fatalf("posts after the external enable: %v", b.posts)
	}
	// Revoked in System Settings: the page sees it after the fresh window.
	b.setSystem(AuthDenied)
	c.advance(3 * time.Second)
	n.settingsStatus()
	b.land()
	n.sync()
	if s := n.settingsStatus(); s != AuthDenied {
		t.Fatalf("after the external revoke the page shows %v", s)
	}
	n.notice("t", "b", false, NoticeOptions{})
	// Enabled again; nobody opens the page, but the next skipped notice
	// reads the status again so the one after it is posted.
	b.setSystem(AuthAuthorized)
	c.advance(3 * time.Second)
	n.notice("t", "c", false, NoticeOptions{})
	b.land()
	n.sync()
	n.notice("t", "d", false, NoticeOptions{})
	if strings.Join(b.posts, ",") != "misc|a,misc|d" || b.requests != 0 {
		t.Fatalf("posts=%v requests=%d", b.posts, b.requests)
	}
	if len(m.saved) != 0 {
		t.Fatalf("observed status changes are not written to the marker: %v", m.saved)
	}
}

// Codex review P2: the tray item must not act on a snapshot older than the
// click. Here the automatic prompt was just answered (allow) but the cached
// status still says not determined; the item opens System Settings instead of
// requesting again, and back-to-back clicks act once.
func TestNotifierExplicitActionWaitsForFreshStatus(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	opened := 0
	n, _, c := newClockedNotifier(b, "", &memory{})
	n.openSettings = func() { opened++ }
	n.notice("t", "x", true, hotkeyNotice) // the automatic request
	// A page read started before the user answered.
	n.settingsStatus()
	b.inFlight = false
	b.setSystem(AuthAuthorized) // answered; the snapshot is behind
	n.explicitAction()
	n.explicitAction() // back-to-back
	if b.requests != 1 || opened != 0 {
		t.Fatalf("acted on the stale snapshot: requests=%d opened=%d", b.requests, opened)
	}
	if lines := n.menuLines(5); !lines[0].Disabled {
		t.Fatalf("the item waits for its answer: %+v", lines)
	}
	b.land() // the read that started before the click: not fresh enough
	n.sync()
	if opened != 0 || b.requests != 1 {
		t.Fatalf("acted on a read older than the click: requests=%d opened=%d", b.requests, opened)
	}
	b.land() // the read started after the click
	n.sync()
	n.explicitAction() // a third click lands after the action: a new one
	n.sync()
	if opened != 1 || b.requests != 1 {
		t.Fatalf("fresh answer allowed: requests=%d opened=%d", b.requests, opened)
	}
	b.land()
	n.sync()
	if opened != 2 || b.requests != 1 {
		t.Fatalf("the click after the action acts again: requests=%d opened=%d", b.requests, opened)
	}
	// No answer arrives: the item acts on the cached status after actionWait.
	b.setSystem(AuthDenied)
	n.explicitAction()
	c.advance(actionWait)
	n.sync()
	if opened != 3 {
		t.Fatalf("no answer within %v: opened=%d", actionWait, opened)
	}
}

// While the prompt is open the fresh status is still not determined; the
// item must not stack a second request.
func TestNotifierExplicitActionWhilePromptOpen(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthNotDetermined}
	n, logs := newTestNotifier(b, "", &memory{})
	n.notice("t", "x", true, hotkeyNotice)
	n.explicitAction()
	settle(n, b)
	if b.requests != 1 || count(*logs, "already open") != 1 {
		t.Fatalf("requests=%d logs=%v", b.requests, *logs)
	}
}

// Codex review P2 (refresh failure loop): a status read that keeps failing is
// retried with backoff a bounded number of times, then the app falls back to
// the log and tooltip quietly, whatever the pump rate.
func TestNotifierRefreshFailuresAreBounded(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthAuthorized, failRead: true}
	n, logs, c := newClockedNotifier(b, "asked", &memory{})
	n.notice("t", "a", false, NoticeOptions{})
	b.failures, b.last = 1, "post: UNErrorDomain 1" // a refused post asks for a read
	for i := 0; i < 2000; i++ {                     // 200 s of 100 ms pumps
		n.sync()
		n.settingsStatus()
		c.advance(100 * time.Millisecond)
	}
	if want := 1 + len(refreshBackoff); b.refreshes != want {
		t.Fatalf("reads = %d, want %d (first try + retries)", b.refreshes, want)
	}
	if k := count(*logs, "could not be read"); k != 1 {
		t.Fatalf("give-up line logged %d times: %v", k, *logs)
	}
	if k := count(*logs, "operation(s) failed"); k > 2+len(refreshBackoff) { // the post + each read
		t.Fatalf("failure lines are bounded, got %d", k)
	}
	n.notice("t", "b", true, hotkeyNotice)
	n.explicitAction()
	if strings.Join(b.posts, ",") != "misc|a" || b.requests != 0 || n.settingsStatus() != AuthUnavailable || b.refreshes != 1+len(refreshBackoff) {
		t.Fatalf("after giving up: posts=%v requests=%d status=%v reads=%d", b.posts, b.requests, n.status(), b.refreshes)
	}
	if lines := n.menuLines(5); len(lines) != 1 || !lines[0].Disabled {
		t.Fatalf("menu after giving up: %+v", lines)
	}
}

// A read whose answer never arrives counts as failed; a read that succeeds
// resets the retry budget.
func TestNotifierRefreshTimeoutAndRecovery(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthDenied}
	n, _, c := newClockedNotifier(b, "asked", &memory{})
	n.settingsStatus()
	c.advance(refreshTimeout)
	n.sync()
	if !n.retryAt.After(c.now()) || n.failedReads != 1 || n.pending() {
		t.Fatalf("a read without an answer must count as failed: %+v", n)
	}
	c.advance(refreshBackoff[0])
	n.sync() // the retry
	b.land()
	b.land()
	n.sync()
	if n.failedReads != 0 || n.broken || n.pending() {
		t.Fatalf("a good read resets the budget: failed=%d broken=%v", n.failedReads, n.broken)
	}
}

func TestNotifierUnavailableAndStatusOnly(t *testing.T) {
	b := &fakeBridge{available: false}
	n, _ := newTestNotifier(b, "", &memory{})
	n.notice("t", "x", true, hotkeyNotice)
	n.sync()
	n.settingsStatus()
	n.explicitAction()
	n.sync()
	if b.requests != 0 || len(b.posts) != 0 || b.refreshes != 0 {
		t.Fatalf("an unavailable center must never be touched: %+v", b)
	}
	if lines := n.menuLines(5); len(lines) != 1 || !lines[0].Disabled {
		t.Fatalf("unavailable menu: %+v", lines)
	}
	b2 := &fakeBridge{available: true, status: AuthAuthorized}
	n2, logs := newTestNotifier(b2, "asked", &memory{})
	n2.notice("t", "Copied.", false, NoticeOptions{Kind: NoticeStatusOnly})
	if len(b2.posts) != 0 || strings.Contains(strings.Join(*logs, "\n"), "not allowed") || b2.refreshes != 0 {
		t.Fatalf("status-only notice posted, logged a skip or read the status: %v %v %d", b2.posts, *logs, b2.refreshes)
	}
}

func TestNotifierLogsAsyncFailures(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthAuthorized}
	n, logs := newTestNotifier(b, "asked", &memory{})
	b.failures, b.last = 2, "post: UNErrorDomain 1"
	n.sync()
	n.sync()
	if got := count(*logs, "UNErrorDomain 1"); got != 1 || b.refreshes != 1 {
		t.Fatalf("failures are logged once per change and refresh the status: %d %d %v", got, b.refreshes, *logs)
	}
}

func TestNotifierMenuLines(t *testing.T) {
	b := &fakeBridge{available: true, status: AuthDenied}
	n, _ := newTestNotifier(b, "", &memory{})
	lines := n.menuLines(5)
	if len(lines) != 2 || lines[0].ID != 5 || lines[0].Label != "Notifications..." || lines[0].Disabled || lines[1].Checked {
		t.Fatalf("denied menu: %+v", lines)
	}
	b.status = AuthAuthorized
	if lines := n.menuLines(5); !lines[1].Checked {
		t.Fatalf("allowed menu: %+v", lines)
	}
}

// page is the Settings page's side of the permission hint, as app.js does
// it: on focus one state request that rechecks the permission, then while
// the state says pending one more state request a second later, at most 5
// times per focus. The hint is derived from the status string alone.
// Between page requests the app pumps (sync) and the system answers at most
// one outstanding read per second (a slow center).
func pageFocus(n *notifier, b *fakeBridge, c *clock) (hint string, requests int) {
	n.recheck()
	s := n.settingsStatus()
	pending := n.pending()
	for k := 0; pending && k < 5; k++ {
		c.advance(time.Second)
		b.land()
		n.sync()
		s, pending = n.settingsStatus(), n.pending()
		requests++
	}
	return s.String(), requests
}

// Codex re-review finding 1: the user changes the permission in System
// Settings and returns to the page inside statusFreshFor of the last read.
// The focus request must not reuse that read: the hint converges with no
// further user input, whether or not an older read is still in flight.
func TestNotifierFocusInsideFreshWindowConverges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to AuthStatus
		inFlight bool // a read started before the change is still outstanding
	}{
		{"enable", AuthDenied, AuthAuthorized, false},
		{"revoke", AuthAuthorized, AuthDenied, false},
		{"enable, older read in flight", AuthDenied, AuthAuthorized, true},
		{"revoke, older read in flight", AuthAuthorized, AuthDenied, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBridge{available: true, status: tc.from}
			n, _, c := newClockedNotifier(b, "asked", &memory{})
			n.settingsStatus() // the page was open; its read landed
			b.land()
			n.sync()
			if tc.inFlight {
				c.advance(statusFreshFor) // a later state poll starts another read
				n.settingsStatus()
				if !n.pending() {
					t.Fatal("setup: no read in flight")
				}
			}
			c.advance(statusFreshFor / 4)
			b.setSystem(tc.to) // toggled in System Settings
			c.advance(statusFreshFor / 4)
			if !tc.inFlight && c.now().Sub(n.freshAt) >= statusFreshFor {
				t.Fatal("setup: the focus must come inside the freshness window")
			}
			hint, requests := pageFocus(n, b, c)
			if hint != tc.to.String() {
				t.Fatalf("hint after focus = %q, want %q (page requests %d, reads %d)", hint, tc.to.String(), requests, b.refreshes)
			}
			if n.pending() || requests > 3 {
				t.Fatalf("converged late or still pending: requests=%d pending=%v", requests, n.pending())
			}
			// Further state polls inside the window reuse the fresh read.
			before := b.refreshes
			n.settingsStatus()
			if b.refreshes != before {
				t.Fatalf("a plain state request inside the window read again")
			}
		})
	}
}

// Codex re-review finding 2: the cached status says not determined but the
// system already has an answer, and the post-click read is slow or never
// lands. The item must never request authorization from that snapshot; on
// timeout it opens System Settings.
func TestNotifierExplicitActionStaleUndeterminedTimeout(t *testing.T) {
	for _, system := range []AuthStatus{AuthAuthorized, AuthDenied} {
		t.Run("delayed reply/"+system.String(), func(t *testing.T) {
			b := &fakeBridge{available: true, status: AuthNotDetermined}
			opened := 0
			n, logs, c := newClockedNotifier(b, "asked", &memory{})
			n.openSettings = func() { opened++ }
			b.setSystem(system)
			n.explicitAction()
			c.advance(actionWait)
			n.sync()
			if b.requests != 0 || opened != 1 || count(*logs, "could not be read in time") != 1 {
				t.Fatalf("timeout with a stale not-determined: requests=%d opened=%d logs=%v", b.requests, opened, *logs)
			}
			b.land() // the reply arrives late
			n.sync()
			if b.requests != 0 || opened != 1 || n.status() != system {
				t.Fatalf("late reply: requests=%d opened=%d status=%v", b.requests, opened, n.status())
			}
			n.explicitAction() // a new click acts on a read that lands in time
			b.land()
			n.sync()
			if b.requests != 0 || opened != 2 {
				t.Fatalf("click after the late reply: requests=%d opened=%d", b.requests, opened)
			}
		})
	}
	t.Run("missing reply, repeated clicks", func(t *testing.T) {
		b := &fakeBridge{available: true, status: AuthNotDetermined}
		opened := 0
		n, _, c := newClockedNotifier(b, "asked", &memory{})
		n.openSettings = func() { opened++ }
		b.setSystem(AuthDenied)
		for i := 1; i <= 6; i++ { // no read ever lands; clicks keep coming
			n.explicitAction()
			n.explicitAction() // back-to-back: the same request
			for k := 0; k < int(actionWait/(100*time.Millisecond)); k++ {
				c.advance(100 * time.Millisecond)
				n.sync()
			}
			if b.requests != 0 || opened != i {
				t.Fatalf("click %d with no reply: requests=%d opened=%d", i, b.requests, opened)
			}
		}
		if b.refreshes > 1+len(refreshBackoff) {
			t.Fatalf("reads stay bounded without replies: %d", b.refreshes)
		}
	})
	t.Run("genuinely not determined still asks", func(t *testing.T) {
		b := &fakeBridge{available: true, status: AuthNotDetermined}
		opened := 0
		n, _, c := newClockedNotifier(b, "", &memory{})
		n.openSettings = func() { opened++ }
		n.explicitAction()
		c.advance(actionWait / 2)
		b.land() // a slow but timely reply: still not determined
		n.sync()
		if b.requests != 1 || opened != 0 {
			t.Fatalf("a landed post-click read of not determined asks: requests=%d opened=%d", b.requests, opened)
		}
	})
}
