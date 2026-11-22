package platform

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Settings page's permission hint, end to end: the production app.js
// focus handler and recheck timers run in Node on a virtual clock, and each
// state request they make is answered by the real notifier (as
// app_settings.go does: recheck on ?recheck=notifications, then the status
// and the pending flag). The system answers each status read after a
// scripted delay; between requests the app pumps (sync) every 50 ms.

// pageHarness is the page side. It prints one JSON line per state request
// ({"t": ms, "path": ...}) and reads the reply line ({"notifications",
// "notifications_pending"}). Focus events come from the scenario. At the
// end it prints {"done": true, "hint": ..., "polls": ...}.
const pageHarness = `
const fs = require('fs');
// The anchors below use LF; a Windows checkout (text=auto) has CRLF.
const js = fs.readFileSync(process.argv[1], 'utf8').replace(/\r\n/g, '\n');
const scenario = JSON.parse(process.argv[2]);
function slice(from, to, keepEnd = true) {
  const s = js.indexOf(from), e = js.indexOf(to, s);
  if (s < 0 || e < 0) throw new Error('app.js changed: ' + from);
  return js.slice(s, keepEnd ? e + to.length : e) + '\n';
}
// The parts of app.js that drive the hint.
const hintCode = slice('  function notificationHint(', '\n  }\n');
const recheckCode = slice('  const NOTIFY_FAST_RECHECKS', '\n  function isSaveNeeded(', false);
const focusCode = slice("    window.addEventListener('focus', ", '\n    });\n');
for (const line of ["if (state.notifications_pending) recheckNotifications();",
                    "const text = notificationHint(state.notifications);"]) {
  if (!js.includes(line)) throw new Error('app.js mergeState changed: ' + line);
}

// Virtual timers.
let now = 0, seq = 0;
const timers = [];
function setTimeout(fn, ms) { const id = ++seq; timers.push({ id, at: now + ms, fn }); return id; }
function clearTimeout(id) { const i = timers.findIndex((x) => x.id === id); if (i >= 0) timers.splice(i, 1); }

// Synchronous exchange with the Go side.
let inbuf = '';
function readLine() {
  const b = Buffer.alloc(4096);
  for (;;) {
    const i = inbuf.indexOf('\n');
    if (i >= 0) { const l = inbuf.slice(0, i); inbuf = inbuf.slice(i + 1); return l; }
    const n = fs.readSync(0, b, 0, b.length, null);
    if (n === 0) throw new Error('go side closed');
    inbuf += b.toString('utf8', 0, n);
  }
}
let polls = 0;
async function api(path) {
  polls++;
  fs.writeSync(1, JSON.stringify({ t: now, path }) + '\n');
  return { ok: true, status: 200, data: { state: JSON.parse(readLine()) } };
}

let closed = false, saving = false, acting = false, hint = '';
let state = null;
function mergeState(st) {
  state = st;
  const text = notificationHint(state.notifications);
  hint = text;
  if (state.notifications_pending) recheckNotifications();
}
const handlers = {};
const window = { addEventListener(ev, fn) { handlers[ev] = fn; } };
eval(hintCode + recheckCode + focusCode);

(async () => {
  const focuses = scenario.focus.slice();
  const flush = () => new Promise((r) => setImmediate(r));
  for (;;) {
    timers.sort((a, b) => a.at - b.at || a.id - b.id);
    const nextTimer = timers.length ? timers[0].at : Infinity;
    const nextFocus = focuses.length ? focuses[0] : Infinity;
    if (nextTimer === Infinity && nextFocus === Infinity) break;
    if (nextFocus <= nextTimer) {
      now = focuses.shift();
      await handlers.focus();
    } else {
      const t = timers.shift();
      now = t.at;
      await t.fn();
    }
    await flush();
  }
  fs.writeSync(1, JSON.stringify({ done: true, t: now, hint, polls, notifications: state && state.notifications }) + '\n');
})().catch((e) => { fs.writeSync(2, String(e && e.stack || e) + '\n'); process.exit(1); });
`

// timedBridge answers each status read after delays[i] (i: the read's index
// from 0; past the end, defaultDelay; a negative delay never answers).
type timedBridge struct {
	*fakeBridge
	c            *clock
	delays       []time.Duration
	defaultDelay time.Duration
	due          []time.Time // of b.queued, in order
}

func (b *timedBridge) Refresh() int64 {
	t := b.fakeBridge.Refresh()
	if t == 0 {
		return 0
	}
	d := b.defaultDelay
	if i := int(t - 1); i < len(b.delays) {
		d = b.delays[i]
	}
	at := b.c.now().Add(d)
	if d < 0 {
		at = time.Time{} // never
	}
	b.due = append(b.due, at)
	return t
}

// deliver lands the reads that are due, in order. A read that never answers
// stays at the head only until a later one is due (the native side answers
// each ticket independently).
func (b *timedBridge) deliver() {
	for i := 0; i < len(b.queued); {
		if !b.due[i].IsZero() && !b.c.now().Before(b.due[i]) {
			r := b.queued[i]
			b.queued = append(b.queued[:i], b.queued[i+1:]...)
			b.due = append(b.due[:i], b.due[i+1:]...)
			if r.ticket > b.applied {
				b.applied, b.status = r.ticket, r.value
			}
			continue
		}
		i++
	}
}

type pageStep struct {
	at time.Duration // since the start of the scenario
	do func()
}

type pageResult struct {
	Done          bool   `json:"done"`
	T             int64  `json:"t"`
	Hint          string `json:"hint"`
	Polls         int    `json:"polls"`
	Notifications string `json:"notifications"`
}

// runPage plays focus events (ms since start) through app.js against n.
// steps change the system at given times. It returns the page's last view.
func runPage(t *testing.T, n *notifier, b *timedBridge, c *clock, start time.Time, focus []int64, steps []pageStep) pageResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for the Settings page check")
	}
	scenario, _ := json.Marshal(map[string]any{"focus": focus})
	cmd := exec.Command(node, "-e", pageHarness, "../settings/web/app.js", string(scenario))
	in, _ := cmd.StdinPipe()
	outPipe, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	advanceTo := func(ms int64) {
		target := start.Add(time.Duration(ms) * time.Millisecond)
		for c.now().Before(target) {
			step := 50 * time.Millisecond
			if d := target.Sub(c.now()); d < step {
				step = d
			}
			c.advance(step)
			for len(steps) > 0 && !c.now().Before(start.Add(steps[0].at)) {
				steps[0].do()
				steps = steps[1:]
			}
			b.deliver()
			n.sync()
		}
	}
	var res pageResult
	sc := bufio.NewScanner(outPipe)
	for sc.Scan() {
		var req struct {
			Done bool   `json:"done"`
			T    int64  `json:"t"`
			Path string `json:"path"`
		}
		line := sc.Bytes()
		if err := json.Unmarshal(line, &req); err != nil {
			t.Fatalf("page said %q: %v", line, err)
		}
		if req.Done {
			_ = json.Unmarshal(line, &res)
			break
		}
		advanceTo(req.T)
		if strings.Contains(req.Path, "recheck=notifications") {
			n.recheck()
		}
		s := n.settingsStatus()
		reply, _ := json.Marshal(map[string]any{"notifications": s.String(), "notifications_pending": n.pending()})
		if _, err := fmt.Fprintf(in, "%s\n", reply); err != nil {
			t.Fatal(err)
		}
	}
	_ = in.Close()
	_, _ = io.Copy(io.Discard, outPipe)
	if err := cmd.Wait(); err != nil || !res.Done {
		t.Fatalf("page harness: %v\n%s", err, stderr.String())
	}
	return res
}

// Re-review 2 finding 1: a recent cached answer, an older read still out,
// and the permission toggled in System Settings with focus (and refocus)
// inside the freshness window. The focus read queues behind the older read
// and both replies are slow but within refreshTimeout; the page must still
// end on the new answer with no further user action.
func TestSettingsPageFocusSlowCoalescedReplies(t *testing.T) {
	sec := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	type toggle struct {
		at time.Duration
		to AuthStatus
	}
	for _, tc := range []struct {
		name    string
		from    AuthStatus
		delays  []time.Duration // per read, in order
		toggles []toggle
		focus   []int64 // ms
		want    AuthStatus
	}{
		{
			// The reviewer's timings: focus at 2 s; the older read answers at
			// 5.1 s (3.1 s after focus), the focus read 2.1 s later.
			name: "enable, slow older read and slow focus read",
			from: AuthDenied, delays: []time.Duration{sec(0.3), sec(4.8), sec(2.1)},
			toggles: []toggle{{sec(1.8), AuthAuthorized}}, focus: []int64{2000}, want: AuthAuthorized,
		},
		{
			name: "revoke, slow older read and slow focus read",
			from: AuthAuthorized, delays: []time.Duration{sec(0.3), sec(4.8), sec(2.1)},
			toggles: []toggle{{sec(1.8), AuthDenied}}, focus: []int64{2000}, want: AuthDenied,
		},
		{
			// Toggle, focus, toggle back and refocus, all inside the window,
			// with every read near the timeout.
			name: "toggle and refocus inside the window",
			from: AuthDenied, delays: []time.Duration{sec(0.3), sec(4.9), sec(4.9), sec(4.9), sec(4.9)},
			toggles: []toggle{{sec(1.8), AuthAuthorized}, {sec(2.5), AuthDenied}, {sec(2.9), AuthAuthorized}},
			focus:   []int64{2000, 3000}, want: AuthAuthorized,
		},
		{
			// The longest the app still recovers from (about recheckBound):
			// the older read answers just in time, the focus read and two
			// retries time out, the last retry answers just in time.
			name: "reads time out until the last retry",
			from: AuthDenied, delays: []time.Duration{sec(0.3), sec(4.9), -1, -1, -1, sec(4.9)},
			toggles: []toggle{{sec(1.8), AuthAuthorized}}, focus: []int64{2000}, want: AuthAuthorized,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeBridge{available: true, status: tc.from}
			n, _, c := newClockedNotifier(fb, "asked", &memory{})
			b := &timedBridge{fakeBridge: fb, c: c, delays: tc.delays, defaultDelay: sec(0.3)}
			n.bridge = b
			// Setup at 0.5 s: a state request starts read 1 and an earlier
			// focus asks for another while it is out; read 1 lands at 0.8 s
			// and read 2 (seeing the old permission) starts then, so at the
			// focus at 2 s the cached answer is fresh and read 2 is out.
			start := c.now()
			c.advance(sec(0.5))
			n.settingsStatus()
			n.recheck()
			steps := []pageStep{}
			for _, tg := range tc.toggles {
				to := tg.to
				steps = append(steps, pageStep{tg.at, func() { fb.setSystem(to) }})
			}
			res := runPage(t, n, b, c, start, tc.focus, steps)
			if fb.refreshes < 2 || n.freshAt.IsZero() {
				t.Fatalf("setup: reads=%d", fb.refreshes)
			}
			if res.Notifications != tc.want.String() {
				t.Fatalf("the page ends on %q, want %q (polls %d, last at %d ms, reads %d, app now %v, pending %v)",
					res.Notifications, tc.want.String(), res.Polls, res.T, fb.refreshes, n.status(), n.pending())
			}
			wantHint := ""
			if tc.want == AuthDenied {
				wantHint = "macOS notifications are off"
			}
			if (wantHint == "") != (res.Hint == "") || !strings.Contains(res.Hint, wantHint) {
				t.Fatalf("hint %q for %v", res.Hint, tc.want)
			}
			if fb.refreshes > 1+len(tc.delays)+len(refreshBackoff) {
				t.Fatalf("reads stay bounded: %d", fb.refreshes)
			}
		})
	}
}

// The page polls at least as long as the app may report a focus recheck
// as pending; recheckBound is what the app guarantees.
func TestSettingsPagePollsLongerThanRecheckBound(t *testing.T) {
	if got := recheckBound(); got != 61*time.Second {
		t.Fatalf("recheckBound = %v; check the poll cap in app.js and update this test", got)
	}
	js, err := os.ReadFile("../settings/web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	constant := func(name string) int {
		m := regexp.MustCompile(`const ` + name + ` = (\d+);`).FindSubmatch(js)
		if m == nil {
			t.Fatalf("app.js has no %s", name)
		}
		v, _ := strconv.Atoi(string(m[1]))
		return v
	}
	fast, max := constant("NOTIFY_FAST_RECHECKS"), constant("NOTIFY_MAX_RECHECKS")
	if !strings.Contains(string(js), "const delay = notifyRechecks < NOTIFY_FAST_RECHECKS ? 1000 : 5000;") {
		t.Fatal("app.js changed its recheck delays; update this test")
	}
	window := time.Duration(fast)*time.Second + time.Duration(max-fast)*5*time.Second
	if window <= recheckBound() {
		t.Fatalf("the page stops polling after %v, before the app's bound %v", window, recheckBound())
	}
}
