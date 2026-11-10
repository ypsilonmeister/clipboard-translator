package platform

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type remoteCall struct {
	kind, session, token, value string
	sym                         int
	serial                      uint32
	flag                        bool
	data                        []byte
}
type fakeRemoteWire struct {
	fakePortalWire
	calls                                   []remoteCall
	cancels, closedSessions, closedRequests []string
}

func (w *fakeRemoteWire) CloseSession(s string) { w.closedSessions = append(w.closedSessions, s) }
func (w *fakeRemoteWire) CloseRequest(s string) { w.closedRequests = append(w.closedRequests, s) }
func (w *fakeRemoteWire) SelectDevices(s, t, v string) {
	w.calls = append(w.calls, remoteCall{kind: "select", session: s, token: t, value: v})
}
func (w *fakeRemoteWire) RequestClipboard(s, t string) {
	w.calls = append(w.calls, remoteCall{kind: "clipboard", session: s, token: t})
}
func (w *fakeRemoteWire) StartRemote(s, t string) {
	w.calls = append(w.calls, remoteCall{kind: "start", session: s, token: t})
}
func (w *fakeRemoteWire) ReadSelection(s, t, m string) {
	w.calls = append(w.calls, remoteCall{kind: "read", session: s, token: t, value: m})
}
func (w *fakeRemoteWire) SetSelection(s, t string) {
	w.calls = append(w.calls, remoteCall{kind: "set", session: s, token: t})
}
func (w *fakeRemoteWire) WriteSelection(s, t string, n uint32, b []byte) {
	w.calls = append(w.calls, remoteCall{kind: "write", session: s, token: t, serial: n, data: append([]byte(nil), b...)})
}
func (w *fakeRemoteWire) WriteDone(s string, n uint32, ok bool) {
	w.calls = append(w.calls, remoteCall{kind: "done", session: s, serial: n, flag: ok})
}
func (w *fakeRemoteWire) CancelIO(t string) { w.cancels = append(w.cancels, t) }
func (w *fakeRemoteWire) Key(s, t string, k int, down bool) {
	w.calls = append(w.calls, remoteCall{kind: "key", session: s, token: t, sym: k, flag: down})
}
func remoteFixture(t *testing.T) (*portalRemote, *fakeRemoteWire, *time.Time) {
	t.Helper()
	now := time.Now()
	w := &fakeRemoteWire{}
	p := startPortalClient(w, waylandAppID, func() time.Time { return now })
	p.phase = "ready"
	p.sessions[portalShortcuts] = "/org/freedesktop/portal/desktop/session/test/shortcuts"
	p.sessions[portalRemoteDesktop] = "/org/freedesktop/portal/desktop/session/test/remote"
	store, err := newPortalTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.tokens = store
	return newPortalRemote(p, w), w, &now
}
func authorizeRemote(t *testing.T, r *portalRemote, w *fakeRemoteWire) {
	t.Helper()
	r.Authorize()
	r.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
	r.Step()
	w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
	r.Step()
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending, devices: 1, clipboard: true, restore: "new-private-token"})
	r.Step()
	if r.phase != "ready" || r.err != nil {
		t.Fatalf("authorization failed: phase=%s err=%v", r.phase, r.err)
	}
}
func TestPortalRemoteAuthorizationAndTokenRotation(t *testing.T) {
	r, w, _ := remoteFixture(t)
	if err := r.client.tokens.Save("old-private-token"); err != nil {
		t.Fatal(err)
	}
	r.Authorize()
	r.Step()
	if len(w.calls) != 1 || w.calls[0].kind != "select" || w.calls[0].value != "old-private-token" {
		t.Fatal("restore token was not supplied to SelectDevices")
	}
	if token, err := r.client.tokens.Load(); err != nil || token != "" {
		t.Fatal("single-use token was not consumed")
	}
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
	r.Step()
	if w.calls[1].kind != "clipboard" {
		t.Fatal("clipboard must be requested before Start")
	}
	w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
	r.Step()
	if w.calls[2].kind != "start" {
		t.Fatal("Start must follow clipboard request acknowledgement")
	}
	w.events = append(w.events, portalEvent{kind: "response", token: r.pending, devices: 1, clipboard: true, restore: "rotated-private-token"})
	r.Step()
	if token, err := r.client.tokens.Load(); err != nil || token != "rotated-private-token" {
		t.Fatal("new token was not persisted")
	}
	r.Close()
	if token, _ := r.client.tokens.Load(); token != "rotated-private-token" {
		t.Fatal("normal shutdown must retain restore token")
	}
}
func TestPortalRemoteDenialAndRevocationKeepShortcuts(t *testing.T) {
	for _, phase := range []string{"select", "clipboard", "start", "missing-keyboard", "revoked", "timeout"} {
		t.Run(phase, func(t *testing.T) {
			r, w, now := remoteFixture(t)
			r.Authorize()
			r.Step()
			if phase != "select" && phase != "timeout" {
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending})
				r.Step()
			}
			if phase != "select" && phase != "clipboard" && phase != "timeout" {
				w.events = append(w.events, portalEvent{kind: "method", token: r.pending})
				r.Step()
			}
			switch phase {
			case "revoked":
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending, devices: 1, clipboard: true, restore: "credential"})
				r.Step()
				w.events = append(w.events, portalEvent{kind: "closed", session: r.session})
			case "missing-keyboard":
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending, clipboard: true})
			case "timeout":
				*now = now.Add(4 * time.Minute)
			default:
				w.events = append(w.events, portalEvent{kind: "response", token: r.pending, code: 1, restore: "must-not-leak"})
			}
			r.Step()
			if r.phase != "failed" || r.err == nil || strings.Contains(r.err.Error(), "must-not-leak") {
				t.Fatal("failure handling leaked or accepted denied permissions")
			}
			if r.client.phase != "ready" || r.client.sessions[portalShortcuts] == "" || w.closes != 0 {
				t.Fatal("denying remote access must not close shortcuts")
			}
			if _, err := r.clip.Read(); !errors.Is(err, errPortalNotAuthorized) {
				t.Fatal("unauthorized clipboard read allowed")
			}
			r.Authorize()
			r.Step()
			if r.operation != "create" || len(w.requests) != 1 {
				t.Fatal("retry must create a new remote session")
			}
		})
	}
}
func primeClipboard(t *testing.T, r *portalRemote, w *fakeRemoteWire, text string) {
	t.Helper()
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME})
	if _, err := r.clip.Read(); !errors.Is(err, errPortalPending) {
		t.Fatal("read should be asynchronous")
	}
	w.events = append(w.events, portalEvent{kind: "read", token: r.clip.read.token, data: []byte(text)})
	got, err := r.clip.Read()
	if err != nil || got != text {
		t.Fatal("clipboard read did not complete")
	}
}
func TestPortalClipboardReadInvalidation(t *testing.T) {
	r, w, _ := remoteFixture(t)
	authorizeRemote(t, r, w)
	primeClipboard(t, r, w, "first")
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME})
	r.clip.Read()
	old := r.clip.read.token
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME}, portalEvent{kind: "read", token: old, data: []byte("stale")})
	if _, err := r.clip.Read(); !errors.Is(err, errPortalPending) {
		t.Fatal("changed owner must invalidate in-flight reads")
	}
	if len(w.cancels) == 0 || w.cancels[len(w.cancels)-1] != old {
		t.Fatal("old pipe was not canceled")
	}
	w.events = append(w.events, portalEvent{kind: "read", token: r.clip.read.token, data: []byte("current")})
	if text, err := r.clip.Read(); err != nil || text != "current" {
		t.Fatal("stale transfer was used")
	}
}
func TestPortalClipboardCompareAndWrite(t *testing.T) {
	for _, order := range []string{"ack-first", "owner-first"} {
		t.Run(order, func(t *testing.T) {
			r, w, _ := remoteFixture(t)
			authorizeRemote(t, r, w)
			primeClipboard(t, r, w, "original\r\n")
			if ok, err := r.clip.ReplaceIfUnchanged("different", "bad"); ok || err != nil {
				t.Fatal("new clipboard must be preserved")
			}
			if ok, err := r.clip.ReplaceIfUnchanged("original", "translation 日本語"); ok || !errors.Is(err, errPortalPending) {
				t.Fatal("unacknowledged write cannot report success")
			}
			ack := portalEvent{kind: "method", token: r.clip.write.token}
			owner := portalEvent{kind: "selection-owner", session: r.session, owner: true, mime: portalTextMIME}
			if order == "ack-first" {
				w.events = append(w.events, ack, owner)
			} else {
				w.events = append(w.events, owner, ack)
			}
			if ok, err := r.clip.ReplaceIfUnchanged("original", "translation 日本語"); !ok || err != nil {
				t.Fatal("confirmed write did not complete")
			}
			w.events = append(w.events, portalEvent{kind: "selection-transfer", session: r.session, mime: portalTextMIME, serial: 7})
			r.Step()
			transfer := w.calls[len(w.calls)-1]
			if transfer.kind != "write" || string(transfer.data) != "translation 日本語" {
				t.Fatal("UTF-8 transfer payload mismatch")
			}
			w.events = append(w.events, portalEvent{kind: "written", token: transfer.token})
			r.Step()
			done := w.calls[len(w.calls)-1]
			if done.kind != "done" || done.serial != 7 || !done.flag {
				t.Fatal("transfer completion was not reported")
			}
		})
	}
}
func TestPortalClipboardNewCopyDuringWrite(t *testing.T) {
	r, w, _ := remoteFixture(t)
	authorizeRemote(t, r, w)
	primeClipboard(t, r, w, "source")
	r.clip.ReplaceIfUnchanged("source", "translation")
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, owner: true, mime: portalTextMIME}, portalEvent{kind: "method", token: r.clip.write.token}, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME})
	before := len(w.calls)
	if ok, err := r.clip.ReplaceIfUnchanged("source", "translation"); ok || err != nil {
		t.Fatal("new copy after write must not be overwritten on retry")
	}
	if len(w.calls) != before || r.clip.owner {
		t.Fatal("retry queued a second SetSelection")
	}
	w.events = append(w.events, portalEvent{kind: "selection-transfer", session: r.session, mime: portalTextMIME, serial: 8})
	r.Step()
	last := w.calls[len(w.calls)-1]
	if last.kind != "done" || last.flag {
		t.Fatal("lost ownership must reject old data requests")
	}
}
func TestPortalClipboardErrorsAndLimits(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	if err := r.clip.Write(strings.Repeat("x", portalTextLimit+1)); err == nil || errors.Is(err, errPortalPending) {
		t.Fatal("oversized write accepted")
	}
	if err := r.clip.Write("bad\x00text"); err == nil || errors.Is(err, errPortalPending) {
		t.Fatal("NUL write accepted")
	}
	r.clip.Write("pending")
	*now = now.Add(6 * time.Second)
	if err := r.clip.Write("pending"); err == nil || errors.Is(err, errPortalPending) {
		t.Fatal("write timeout missing")
	}
	primeClipboard(t, r, w, "good")
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME})
	r.clip.Read()
	w.events = append(w.events, portalEvent{kind: "read", token: r.clip.read.token, data: []byte{0xff}})
	if _, err := r.clip.Read(); err == nil || errors.Is(err, errPortalPending) {
		t.Fatal("invalid UTF-8 accepted")
	}
}
func finishRemoteKeys(t *testing.T, r *portalRemote, w *fakeRemoteWire, k *portalKeys) {
	t.Helper()
	for i := 0; i < 8 && !k.done; i++ {
		for token := range r.keys {
			w.events = append(w.events, portalEvent{kind: "method", token: token})
		}
		r.Step()
	}
	if !k.done || k.err != nil {
		t.Fatal("key sequence did not finish")
	}
}
func TestPortalKeyboardSequencesAndCleanup(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	start := len(w.calls)
	k := r.beginKeys('c')
	finishRemoteKeys(t, r, w, k)
	want := []portalKey{{0xffe3, true}, {'c', true}, {'c', false}, {0xffe3, false}}
	for i, e := range want {
		call := w.calls[start+i]
		if call.sym != e.sym || call.flag != e.down {
			t.Fatal("keyboard sequence is not keysym Ctrl+C")
		}
	}
	k = r.beginKeys('v')
	*now = now.Add(6 * time.Second)
	r.Step()
	if !k.done || k.err == nil {
		t.Fatal("keyboard timeout ignored")
	}
	n := len(w.calls)
	if w.calls[n-1].flag || w.calls[n-2].flag {
		t.Fatal("timed-out keys must be released")
	}
}
func TestPortalCaptureNeverAutoPastes(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	primeClipboard(t, r, w, "old clipboard")
	task := beginPortalCapture(r, func() bool { return true }).(*portalCapture)
	task.Step()
	*now = now.Add(160 * time.Millisecond)
	task.Step()
	finishRemoteKeys(t, r, w, task.keys)
	task.Step()
	w.events = append(w.events, portalEvent{kind: "selection-owner", session: r.session, mime: portalTextMIME})
	task.Step()
	*now = now.Add(100 * time.Millisecond)
	task.Step()
	if r.clip.read == nil {
		t.Fatal("capture did not request a fresh selection")
	}
	w.events = append(w.events, portalEvent{kind: "read", token: r.clip.read.token, data: []byte("selected text")})
	done, text, fresh, err := task.Step()
	if !done || text != "selected text" || fresh || err != nil {
		t.Fatal("Wayland selection capture must never authorize auto-paste")
	}
}
func TestPortalCaptureHeldModifiersAndCopyFailure(t *testing.T) {
	r, w, now := remoteFixture(t)
	authorizeRemote(t, r, w)
	task := beginPortalCapture(r, func() bool { return false })
	before := len(w.calls)
	*now = now.Add(2 * time.Second)
	if done, _, _, err := task.Step(); !done || !errors.Is(err, ErrCaptureModifiersHeld) {
		t.Fatal("held modifiers must abort")
	}
	if len(w.calls) != before {
		t.Fatal("held modifiers must not read or copy")
	}
	primeClipboard(t, r, w, "old")
	copy := beginPortalCapture(r, func() bool { return true }).(*portalCapture)
	copy.Step()
	*now = now.Add(160 * time.Millisecond)
	copy.Step()
	for token := range r.keys {
		w.events = append(w.events, portalEvent{kind: "failed", token: token})
	}
	if done, text, _, err := copy.Step(); !done || text != "" || err == nil {
		t.Fatal("failed copy must not translate stale clipboard text")
	}
}

func TestPortalKeyboardRejectsOverlappingSequences(t *testing.T) {
	r, w, _ := remoteFixture(t)
	authorizeRemote(t, r, w)
	first := r.beginKeys('c')
	calls := len(w.calls)
	second := r.beginKeys('v')
	if !second.done || second.err == nil || len(w.calls) != calls {
		t.Fatal("overlapping virtual modifier sequences must be rejected")
	}
	finishRemoteKeys(t, r, w, first)
}
