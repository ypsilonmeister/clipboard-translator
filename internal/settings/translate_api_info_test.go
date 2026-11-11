package settings

import (
	"net/http/httptest"
	"strings"
	"testing"
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
