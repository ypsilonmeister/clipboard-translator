package llamaserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

func TestTranslatePayloadBothWays(t *testing.T) {
	cases := []struct{ source, sourceLang, targetLang, answer string }{
		{"こんにちは", "ja", "en", "Hello."},
		{"Hello.", "en", "ja", "こんにちは。"},
	}
	for _, tc := range cases {
		t.Run(tc.sourceLang+tc.targetLang, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("path=%s", r.URL.Path)
				}
				var m map[string]any
				if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
					t.Error(err)
				}
				opts := m["chat_template_kwargs"].(map[string]any)
				if opts["source_lang"] != tc.sourceLang || opts["target_lang"] != tc.targetLang {
					t.Errorf("opts=%v", opts)
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": tc.answer}}}})
			}))
			defer srv.Close()
			got, err := Translate(context.Background(), srv.URL, "", tc.source, languages.Direction{Source: tc.sourceLang, Target: tc.targetLang}, 128)
			if err != nil || got != tc.answer {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}

func TestTranslateFinishReason(t *testing.T) {
	cases := []struct {
		name      string
		choice    map[string]any
		want      string
		truncated bool
	}{
		{"stop", map[string]any{"message": map[string]any{"content": "Hello."}, "finish_reason": "stop"}, "Hello.", false},
		{"missing", map[string]any{"message": map[string]any{"content": "Hello."}}, "Hello.", false},
		{"null", map[string]any{"message": map[string]any{"content": "Hello."}, "finish_reason": nil}, "Hello.", false},
		{"length", map[string]any{"message": map[string]any{"content": "incomplete translation"}, "finish_reason": "length"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{tc.choice}})
			}))
			defer srv.Close()
			got, err := Translate(context.Background(), srv.URL, "", "こんにちは", languages.Direction{Source: "ja", Target: "en"}, 1)
			if tc.truncated {
				if !errors.Is(err, ErrTruncated) || got != "" {
					t.Fatalf("got=%q err=%v, want errTruncated", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}
