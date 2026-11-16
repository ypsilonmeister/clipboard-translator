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
		// Latin text quoting a little of the other script is read as Latin (#187).
		{[]string{"ja", "en"}, "Hi Tanaka-san (田中さん), could you review the attached proposal by Friday? Thanks!", "en", "name in ja", "ja", true},
		{[]string{"ja", "en"}, "The word 侘寂 has no direct English equivalent.", "en", "quoted word", "ja", true},
		{[]string{"en", "zh-hans"}, "Our Shanghai (上海) team will join the call at 3 pm.", "en", "han name", "zh-hans", true},
		{[]string{"en", "ko"}, "Samsung (삼성) announced a new phone today.", "en", "hangul name", "ko", true},
		{[]string{"ru", "en"}, "Привет, this is a test of the new system", "en", "one cyrillic word", "ru", true},
		{[]string{"ja", "en"}, "Hi Tanaka-san (田中太郎さん), could you review the attached proposal by Friday? Thanks!", "en", "six-char name", "ja", true},
		// The other script stays the skeleton: unchanged results.
		{[]string{"ja", "en"}, "このAPIはJSONを返します", "ja", "particles", "en", true},
		{[]string{"ja", "en"}, "Docker Composeを使う", "ja", "attached particle", "en", true},
		{[]string{"ja", "en"}, "APIのURL", "ja", "short tech", "en", true},
		{[]string{"ja", "en"}, "Kubernetes clusterをdeployする", "ja", "tech verbs", "en", true},
		{[]string{"ja", "en"}, "Visual Studio Code で TypeScript を debug する", "ja", "spaced tech", "en", true},
		{[]string{"ja", "en"}, "次のエラーが出ます: java.lang.NullPointerException at com.example.foo.Bar.baz(Bar.java:42)", "ja", "code fragment", "en", true},
		{[]string{"ja", "en"}, "I love 寿司", "ja", "short and ambiguous", "en", true},
		{[]string{"en", "ko"}, "삼성 Galaxy S24 출시", "ko", "hangul product", "en", true},
		{[]string{"en", "ko"}, "API를 사용합니다", "ko", "hangul particle", "en", true},
		{[]string{"en", "th"}, "สวัสดีครับ ผมใช้ iPhone 15 Pro Max มาสองปีแล้ว", "th", "thai product", "en", true},
		{[]string{"ru", "en"}, "Это тест of the system", "ru", "cyrillic skeleton", "en", true},
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
