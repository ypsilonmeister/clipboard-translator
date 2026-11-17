package settings

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTranslationAPIConnectionInfoAction(t *testing.T) {
	s := &Server{ask: func(req Request) Reply {
		if req.Action != "translate_api_info" || req.Save != nil {
			t.Fatalf("request=%+v", req)
		}
		return Reply{State: State{TranslateAPIToken: "existing"}}
	}}
	w := httptest.NewRecorder()
	s.handleAction("translate_api_info", "")(w, httptest.NewRequest("POST", "/api/translate-api/info", strings.NewReader(`{}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"translate_api_token":"existing"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestTranslationAPIRotateActionRoute(t *testing.T) {
	var calls atomic.Int32
	s, err := Start(func(req Request) Reply {
		calls.Add(1)
		if req.Action != "translate_api_regenerate" || req.Save != nil {
			t.Errorf("request=%+v", req)
		}
		return Reply{State: State{TranslateAPIToken: "regenerated"}}
	}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, authorized := range []bool{false, true} {
		r, err := http.NewRequest("POST", "http://"+s.host+"/api/translate-api/regenerate", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://"+s.host)
		if authorized {
			r.Header.Set(TokenHeader, s.token)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !authorized && (res.StatusCode != 403 || calls.Load() != 0) {
			t.Fatal("unguarded rotation route")
		}
		if authorized && (res.StatusCode != 200 || calls.Load() != 1 || !strings.Contains(string(body), `"translate_api_token":"regenerated"`)) {
			t.Fatalf("rotation route: %d %s", res.StatusCode, body)
		}
	}
}

func TestTranslateAPIStatePreservation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for settings state check")
	}
	cmd := exec.Command(node, "-e", `
const fs = require('fs'), assert = require('node:assert/strict');
const js = fs.readFileSync('web/app.js', 'utf8');

const elements = {};
function getEl(id) {
  if (!elements[id]) {
    elements[id] = { id, disabled: false, checked: false, value: '', textContent: '', addEventListener(event, fn) { this['on' + event] = fn; }, select() {} };
  }
  return elements[id];
}
const $ = getEl;

let state = { translate_api_enabled: false, translate_api_token: '', translate_api_available: true, translate_api_url: '' };
let apiResponses = [];
const api = async (url, params) => apiResponses.shift();

const vm = require('vm');
const sandbox = {
  $,
  get state() { return state; },
  set state(v) { state = v; },
  api,
  navigator: { clipboard: { writeText: async () => {} } },
  console,
};
vm.createContext(sandbox);

const renderIdx = js.indexOf('function renderTranslateAPI(');
const renderEnd = js.indexOf('\n  }', renderIdx) + 4;
vm.runInContext(js.slice(renderIdx, renderEnd), sandbox);

const initIdx = js.indexOf("$('translate-api-enabled').addEventListener('change'");
const initEnd = js.indexOf("$('guide').addEventListener('toggle'", initIdx);
vm.runInContext('(async () => {\n' + js.slice(initIdx, initEnd) + '\n})()', sandbox);

(async () => {
  sandbox.renderTranslateAPI(state);
  assert.equal(getEl('translate-api-enabled').checked, false);

  // 1. Successful enable updates state
  apiResponses.push({ ok: true, data: { state: { translate_api_enabled: true, translate_api_token: 'tok123', translate_api_available: true, translate_api_url: 'http://localhost:18121/translate' } } });
  await getEl('translate-api-enabled').onchange({ target: { checked: true } });

  assert.equal(state.translate_api_enabled, true);
  assert.equal(state.translate_api_token, 'tok123');
  assert.equal(getEl('translate-api-enabled').checked, true);
  assert.equal(getEl('translate-api-token').value, 'tok123');

  // 2. Failed toggle reverts to current state (true), not page-load state (false)
  apiResponses.push({ ok: false, data: { error: 'Server error' } });
  await getEl('translate-api-enabled').onchange({ target: { checked: false } });

  assert.equal(getEl('translate-api-enabled').checked, true);
  assert.equal(getEl('translate-api-token').value, 'tok123');

  // 3. Info/copy updates state.translate_api_token
  apiResponses.push({ ok: true, data: { state: { translate_api_enabled: true, translate_api_token: 'tok456', translate_api_available: true, translate_api_url: 'http://localhost:18121/translate' } } });
  await getEl('translate-api-copy').onclick();

  assert.equal(state.translate_api_token, 'tok456');
  assert.equal(getEl('translate-api-token').value, 'tok456');
})();
`)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("translate API state preservation test: %v\n%s", err, out)
	}
}
