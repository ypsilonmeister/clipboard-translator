package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectDirection(t *testing.T) {
	cases := []struct {
		input, source, target string
		ok                    bool
	}{
		{"明日の会議は何時？", "ja", "en", true},
		{"漢字", "ja", "en", true},
		{"Hello, world!", "en", "ja", true},
		{"ありがとう and thanks", "ja", "en", true},
		{"https://example.com/日本語", "", "", false},
		{"12345", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		got, ok := directionFor(defaultLanguages, tc.input)
		if ok != tc.ok || got.Source != tc.source || got.Target != tc.target {
			t.Errorf("%q: got %+v, %v", tc.input, got, ok)
		}
	}
	if shouldTranslate("日本語をたくさん", 2, defaultLanguages) {
		t.Fatal("max chars ignored")
	}
}
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
			got, err := translate(context.Background(), srv.URL, tc.source, translationDirection{tc.sourceLang, tc.targetLang}, 128)
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
			got, err := translate(context.Background(), srv.URL, "こんにちは", translationDirection{"ja", "en"}, 1)
			if tc.truncated {
				if !errors.Is(err, errTruncated) || got != "" {
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

type testClipboard struct {
	text   string
	writes int
	err    error
}

func (c *testClipboard) Read() (string, error) { return c.text, nil }
func (c *testClipboard) Write(s string) error  { c.text = s; c.writes++; return nil }
func (c *testClipboard) Close() error          { return nil }
func (c *testClipboard) ReplaceIfUnchanged(expected, translated string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if c.text != expected {
		return false, nil
	}
	c.text = translated
	c.writes++
	return true, nil
}

func TestApplyTranslationPreservesClipboard(t *testing.T) {
	cases := []struct {
		name, clipboard, original, translated, want string
		wantApplied                                 bool
		wantWrites                                  int
	}{
		{"same captured text after metadata update", "こんにちは", "こんにちは", "Hello", "Hello", true, 1},
		{"different clipboard text copied during translation", "new user copy", "こんにちは", "Hello", "new user copy", false, 0},
		{"model returns unchanged text", "こんにちは", "こんにちは", "こんにちは", "こんにちは", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &testClipboard{text: tc.clipboard}
			applied, err := applyTranslation(c, tc.original, tc.translated)
			if err != nil || applied != tc.wantApplied || c.text != tc.want || c.writes != tc.wantWrites {
				t.Fatalf("applied=%v err=%v clipboard=%q writes=%d", applied, err, c.text, c.writes)
			}
		})
	}
}
