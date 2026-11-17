package settings

import (
	"io"
	"net/http"
	"net/http/httptest"
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
