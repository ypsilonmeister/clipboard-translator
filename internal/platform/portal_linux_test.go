package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakePortalWire struct {
	events     []portalEvent
	registered string
	requests   []struct{ iface, token, session string }
	closes     int
}

func (w *fakePortalWire) Register(id string) { w.registered = id }
func (w *fakePortalWire) CreateSession(iface, token, session string) {
	w.requests = append(w.requests, struct{ iface, token, session string }{iface, token, session})
}
func (w *fakePortalWire) Poll() (portalEvent, bool) {
	if len(w.events) == 0 {
		return portalEvent{}, false
	}
	e := w.events[0]
	w.events = w.events[1:]
	return e, true
}
func (w *fakePortalWire) Close() { w.closes++ }
func portalFixture() (*portalClient, *fakePortalWire, *time.Time) {
	now := time.Now()
	w := &fakePortalWire{}
	p := startPortalClient(w, waylandAppID, func() time.Time { return now })
	return p, w, &now
}
func TestPortalRegistrationAndSessionLifecycle(t *testing.T) {
	p, w, _ := portalFixture()
	if w.registered != waylandAppID || len(w.requests) != 0 {
		t.Fatal("must register before any portal request")
	}
	w.events = append(w.events, portalEvent{kind: "registered"})
	p.Step()
	if len(w.requests) != 2 || p.phase != "creating" {
		t.Fatalf("requests=%+v phase=%s", w.requests, p.phase)
	}
	tokens := map[string]bool{}
	for i := len(w.requests) - 1; i >= 0; i-- {
		q := w.requests[i]
		if tokens[q.token] || tokens[q.session] || q.token == q.session {
			t.Fatal("tokens must be unique")
		}
		tokens[q.token] = true
		tokens[q.session] = true
		w.events = append(w.events, portalEvent{kind: "response", token: q.token, session: "/org/freedesktop/portal/desktop/session/test/" + q.session})
	}
	p.Step()
	if p.phase != "ready" || len(p.sessions) != 2 {
		t.Fatalf("phase=%s sessions=%v", p.phase, p.sessions)
	}
	w.events = append(w.events, portalEvent{kind: "closed", session: p.sessions[portalShortcuts]})
	p.Step()
	if p.phase != "failed" || len(p.sessions) != 0 || w.closes != 1 {
		t.Fatal("revocation must discard both sessions and disconnect")
	}
	p.Close()
	p.Close()
	if w.closes != 1 {
		t.Fatal("close must be idempotent after failure")
	}
}
func TestPortalFailureAndCancellation(t *testing.T) {
	for _, kind := range []string{"registration", "denied", "malformed", "lost", "early-close", "timeout", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			p, w, now := portalFixture()
			if kind != "registration" {
				w.events = append(w.events, portalEvent{kind: "registered"})
				p.Step()
			}
			switch kind {
			case "registration", "lost":
				w.events = append(w.events, portalEvent{kind: "failed"})
			case "denied":
				w.events = append(w.events, portalEvent{kind: "response", token: w.requests[0].token, code: 1})
			case "malformed":
				w.events = append(w.events, portalEvent{kind: "response", token: w.requests[0].token, session: "bad"})
			case "early-close":
				w.events = append(w.events, portalEvent{kind: "closed", session: "/org/freedesktop/portal/desktop/session/test/pending"})
			case "timeout":
				*now = now.Add(16 * time.Second)
			case "cancel":
				p.Close()
			}
			p.Step()
			if w.closes != 1 || len(p.sessions) != 0 || (kind != "cancel" && (p.phase != "failed" || p.err == nil)) {
				t.Fatalf("phase=%s closes=%d err=%v", p.phase, w.closes, p.err)
			}
			p.Close()
			if w.closes != 1 {
				t.Fatal("double disconnect")
			}
		})
	}
}
func TestPortalIgnoresUnknownAndDuplicateReplies(t *testing.T) {
	p, w, _ := portalFixture()
	w.events = []portalEvent{{kind: "registered"}, {kind: "registered"}, {kind: "response", token: "unknown", code: 2}}
	p.Step()
	if p.phase != "creating" || len(w.requests) != 2 || len(p.pending) != 2 {
		t.Fatal("unrelated replies changed state")
	}
	p.Close()
	w.events = append(w.events, portalEvent{kind: "registered"})
	p.Step()
	if len(w.requests) != 2 {
		t.Fatal("closed client restarted")
	}
}

func TestPortalDesktopEntryTracksExecutable(t *testing.T) {
	home := t.TempDir()
	exe := "/tmp/with space/a\"$%/translator"
	if err := installPortalDesktopEntry(home, exe); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "applications", waylandAppID+".desktop")
	expected, err := linuxDesktopEntry(exe)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != expected+"NoDisplay=true\n" {
		t.Fatalf("desktop entry=%s", got)
	}
	if err := installPortalDesktopEntry(home, "/moved/translator"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(file)
	if !strings.Contains(string(got), "/moved/translator") {
		t.Fatal("Exec path was not updated")
	}
	if err := installPortalDesktopEntry("relative", exe); err == nil {
		t.Fatal("relative data home accepted")
	}
}
func TestPortalPrivateTokenStore(t *testing.T) {
	home := t.TempDir()
	s, err := newPortalTokenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if token, err := s.Load(); err != nil || token != "" {
		t.Fatal("missing token must be allowed")
	}
	for _, token := range []string{"first-private-token", "rotated-private-token"} {
		if err := s.Save(token); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load()
		if err != nil || got != token {
			t.Fatal("token round trip failed")
		}
	}
	file := filepath.Join(s.dir, "restore-token")
	st, _ := os.Stat(file)
	if st.Mode().Perm() != 0600 {
		t.Fatal("token permissions")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("public token accepted")
	}
	if err := s.Save("replacement"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(""); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, "external")
	if err := os.WriteFile(external, []byte("do-not-read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, file); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("symlink token accepted")
	}
	if err := s.Save("private"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(external)
	if string(b) != "do-not-read" {
		t.Fatal("external file overwritten")
	}
	if err := s.Save(strings.Repeat("x", 4097)); err == nil {
		t.Fatal("oversized token accepted")
	}
	if err := os.Chmod(s.dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := newPortalTokenStore(home); err == nil {
		t.Fatal("public state folder accepted")
	}
}
