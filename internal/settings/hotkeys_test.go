package settings

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHotkeysUnavailableRendering(t *testing.T) {
	html, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// Native fieldset disabling covers every profile input and the Add button,
	// including controls subsequently rebuilt by renderHotkeys.
	start := strings.Index(string(html), `<fieldset id="hotkeys-controls"`)
	if start < 0 {
		t.Fatal("missing Hotkeys fieldset")
	}
	end := strings.Index(string(html)[start:], "</fieldset>")
	if end < 0 {
		t.Fatal("unclosed Hotkeys fieldset")
	}
	section := string(html)[start : start+end]
	for _, id := range []string{`id="profiles"`, `id="add"`} {
		if !strings.Contains(section, id) {
			t.Fatalf("%s outside disabled fieldset", id)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for page rendering check")
	}
	cmd := exec.Command(node, "-e", `
const fs = require('fs');
const assert = require('node:assert/strict');
const js = fs.readFileSync('web/app.js', 'utf8');
const start = js.indexOf('function renderBusy()');
const end = js.indexOf('// The quick start card:', start);
assert(start >= 0 && end > start);
const elements = {};
const $ = id => elements[id] ||= { disabled: false, hidden: true, textContent: '' };
let state;
let closed = false, saving = false, lastServerNotice = '';
const appBusy = () => false;
const showNotice = () => {};
eval(js.slice(start, end));
const note = 'Not available on ChromeOS: hotkeys cannot be triggered from Chrome or other host apps.';
for (const unavailable of [false, true, false]) {
  state = unavailable ? { hotkeys_unavailable: note } : {};
  renderBusy();
  assert.equal($('hotkeys-controls').disabled, unavailable);
  assert.equal($('hotkeys-unavailable').hidden, !unavailable);
  assert.equal($('hotkeys-unavailable').textContent, unavailable ? note : '');
}
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderBusy: %v\n%s", err, out)
	}
}
