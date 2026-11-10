package app

import (
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
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
		got, ok := languages.DirectionFor(languages.Default, tc.input)
		if ok != tc.ok || got.Source != tc.source || got.Target != tc.target {
			t.Errorf("%q: got %+v, %v", tc.input, got, ok)
		}
	}
	if _, ok := (config.Profile{Languages: languages.Default}).Direction("日本語をたくさん", 2); ok {
		t.Fatal("max chars ignored")
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
	if !platform.SameClipboardText(c.text, expected) {
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

func TestFormatBilingual(t *testing.T) {
	cases := []struct {
		name, source, translated, want string
	}{
		{"plain", "Hello", "こんにちは", "Hello\nこんにちは"},
		{"trailing CRLF", "Hello\r\n", "こんにちは", "Hello\nこんにちは"},
		{"trailing LF", "Hello\n", "こんにちは", "Hello\nこんにちは"},
		{"trailing CR", "Hello\r", "こんにちは", "Hello\nこんにちは"},
		{"multiple trailing newlines", "Hello\r\n\r\n\n", "こんにちは", "Hello\nこんにちは"},
		{"multiline text", "Line 1\r\nLine 2\r\n", "行1\n行2", "Line 1\r\nLine 2\n行1\n行2"},
		{"preserves trailing spaces before newline", "Hello   \r\n", "こんにちは", "Hello   \nこんにちは"},
		{"empty source with newlines", "\r\n", "こんにちは", "\nこんにちは"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatBilingual(tc.source, tc.translated); got != tc.want {
				t.Fatalf("formatBilingual(%q, %q) = %q, want %q", tc.source, tc.translated, got, tc.want)
			}
		})
	}
}
