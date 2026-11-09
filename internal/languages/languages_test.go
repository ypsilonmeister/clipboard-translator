package languages

import (
	"strings"
	"testing"
)

func TestDirectionForPairs(t *testing.T) {
	cases := []struct {
		pair          []string
		input         string
		source, label string // label is only for failure messages
		target        string
		ok            bool
	}{
		// ja/en keeps the old rules, in either list order.
		{[]string{"ja", "en"}, "漢字", "ja", "kanji only", "en", true},
		{[]string{"en", "ja"}, "漢字", "ja", "kanji only, reversed", "en", true},
		{[]string{"en", "ja"}, "Hello", "en", "latin, reversed", "ja", true},
		{[]string{"ja", "en"}, "ありがとう and thanks", "ja", "mixed", "en", true},
		// Latin <-> other script.
		{[]string{"en", "ko"}, "안녕하세요", "ko", "hangul", "en", true},
		{[]string{"en", "ko"}, "Hello", "en", "latin", "ko", true},
		{[]string{"ru", "en"}, "Привет, world", "ru", "cyrillic wins", "en", true},
		{[]string{"en", "th"}, "สวัสดี", "th", "thai", "en", true},
		{[]string{"en", "zh-hans"}, "你好", "zh-hans", "han", "en", true},
		{[]string{"zh-hant", "en"}, "Hello", "en", "latin", "zh-hant", true},
		// Two non-Latin scripts.
		{[]string{"ja", "ko"}, "こんにちは", "ja", "kana", "ko", true},
		{[]string{"ja", "ko"}, "안녕하세요", "ko", "hangul", "ja", true},
		{[]string{"ja", "ko"}, "漢字", "ja", "han only means ja", "ko", true},
		{[]string{"ja", "zh-hans"}, "こんにちは世界", "ja", "kana beside han", "zh-hans", true},
		{[]string{"zh-hans", "ja"}, "こんにちは世界", "ja", "kana beside han, reversed", "zh-hans", true},
		{[]string{"ja", "zh-hans"}, "你好世界", "zh-hans", "han only is chinese here", "ja", true},
		// Not translated.
		{[]string{"ja", "ko"}, "Hello", "", "latin is neither", "", false},
		{[]string{"ja", "en"}, "https://example.com/日本語", "", "url", "", false},
		{[]string{"ja", "en"}, "12345", "", "digits", "", false},
		{nil, "Hello", "en", "missing pair uses ja/en", "ja", true},
	}
	for _, tc := range cases {
		got, ok := DirectionFor(tc.pair, tc.input)
		if ok != tc.ok || got.Source != tc.source || got.Target != tc.target {
			t.Errorf("%s %v %q: got %+v, %v; want %s->%s, %v", tc.label, tc.pair, tc.input, got, ok, tc.source, tc.target, tc.ok)
		}
	}
}

func TestValidateLanguages(t *testing.T) {
	good := [][]string{{"ja", "en"}, {"EN", " ko "}, {"zh-hant", "ja"}, {"en", "ru"}}
	for _, g := range good {
		got, err := Validate(g)
		if err != nil {
			t.Errorf("%v must be accepted: %v", g, err)
			continue
		}
		for _, c := range got {
			if c != strings.ToLower(strings.TrimSpace(c)) {
				t.Errorf("%v: %q was not normalized", g, c)
			}
		}
	}
	bad := []struct {
		in   []string
		want string
	}{
		{nil, "exactly two"},
		{[]string{"ja"}, "exactly two"},
		{[]string{"ja", "en", "ko"}, "exactly two"},
		{[]string{"ja", "xx"}, `"xx"`},
		{[]string{"ja", "zh"}, "zh-hans or zh-hant"},
		{[]string{"en", "en"}, "different"},
		{[]string{"en", "de"}, "same script"},
		{[]string{"zh-hans", "zh-hant"}, "same script"},
		{[]string{"ru", "bg"}, "same script"},
	}
	for _, tc := range bad {
		if _, err := Validate(tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want an error containing %q, got %v", tc.in, tc.want, err)
		}
	}
}

func TestEveryLanguageHasAScriptTable(t *testing.T) {
	if len(languages) != 53 {
		t.Errorf("the model card lists 53 languages, the table has %d", len(languages))
	}
	for code, l := range languages {
		if code != strings.ToLower(code) {
			t.Errorf("%s: codes must be lowercase", code)
		}
		if len(scriptTables[l.script]) == 0 {
			t.Errorf("%s: no unicode table for script %q", code, l.script)
		}
	}
}
