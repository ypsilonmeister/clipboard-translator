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
		{[]string{"ru", "en"}, "The Russian word тоска has no exact equivalent in English.", "en", "one cyrillic word", "ru", true},
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
		// More Latin skeletons, in other scripts and positions.
		{[]string{"ja", "en"}, "Our office has moved. The new address is 〒100-0001 東京都千代田区千代田1-1 and the phone number is unchanged. Please update your records.", "en", "address in english", "ja", true},
		{[]string{"ja", "en"}, "1. The word 侘寂 has no direct equivalent in other languages.", "en", "list number first", "ja", true},
		{[]string{"de", "ja"}, "Das Wort 侘寂 hat keine direkte deutsche Entsprechung und ist schwer zu erklären.", "de", "german", "ja", true},
		{[]string{"en", "th"}, "The dish called ต้มยำ is a hot and sour soup from Thailand.", "en", "thai word", "th", true},
		{[]string{"en", "ar"}, "The Arabic greeting مرحبا is used in many countries across the region.", "en", "arabic word", "ar", true},
		{[]string{"en", "hi"}, "The Hindi word जुगाड़ describes a clever and frugal fix.", "en", "hindi word", "hi", true},
		// Names, lists and commands are not a Latin sentence: unchanged results.
		{[]string{"ja", "en"}, "React Native と Expo で iOS app を build", "ja", "spaced particles", "en", true},
		{[]string{"ja", "en"}, "Amazon Web Services Elastic Compute Cloud とは", "ja", "long product name", "en", true},
		{[]string{"ja", "en"}, "対応 OS: Windows 11, macOS Sonoma, Ubuntu 24.04 LTS, Red Hat Enterprise Linux", "ja", "product list", "en", true},
		{[]string{"ja", "en"}, "npm run build して npm run test する", "ja", "commands with suru", "en", true},
		{[]string{"ja", "en"}, "git push origin main する", "ja", "command with suru", "en", true},
		{[]string{"ja", "en"}, "all code review comments を resolve した", "ja", "lowercase terms", "en", true},
		{[]string{"ja", "en"}, "今日は\u3000meeting\u3000が\u3000three\u3000times\u3000ある\u3000and\u3000it\u3000is\u3000tiring", "ja", "ideographic spaces", "en", true},
		{[]string{"ja", "en"}, "ｺﾝﾋﾟｭｰﾀ を restart して log file を確認", "ja", "half-width kana", "en", true},
		{[]string{"ja", "en"}, "田中さん、please review this document by Friday", "ja", "too few prose words", "en", true},
		{[]string{"en", "ko"}, "React Native, Expo, TypeScript, Next.js 사용", "ko", "hangul product list", "en", true},
		{[]string{"en", "zh-hans"}, "支持 Windows 11, macOS Sonoma, Ubuntu 24.04 LTS 和 Red Hat Enterprise Linux", "zh-hans", "han product list", "en", true},
		{[]string{"en", "zh-hans"}, "运行 npm install lodash express", "zh-hans", "han verb and command", "en", true},
		{[]string{"en", "ru"}, "Установите Visual Studio Code и GitHub Copilot extension", "ru", "cyrillic product names", "en", true},
		{[]string{"en", "ru"}, "Запусти git push origin main", "ru", "cyrillic verb and command", "en", true},
		{[]string{"en", "ar"}, "حمّل Visual Studio Code و GitHub Desktop", "ar", "arabic product names", "en", true},
		{[]string{"en", "hi"}, "Visual Studio Code में GitHub Copilot install करें", "hi", "hindi product names", "en", true},
		// Text that opens or ends in the other script: unchanged results.
		{[]string{"ru", "en"}, "Привет, this is a test of the new system", "ru", "opens in cyrillic", "en", true},
		{[]string{"en", "ru"}, "Запусти npm run build and then npm run test", "ru", "cyrillic verb and long command", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает", "ru", "command, ends in cyrillic", "en", true},
		{[]string{"en", "zh-hans"}, "试试 pip install requests numpy pandas", "zh-hans", "han verb and long command", "en", true},
		{[]string{"en", "zh-hant"}, "執行 docker compose up and then check the logs", "zh-hant", "han verb and long command", "en", true},
		{[]string{"ja", "en"}, "実行: npm run build and then npm run test", "ja", "kanji label and command", "en", true},
		{[]string{"ja", "en"}, "以下を翻訳: The quick brown fox jumps over the lazy dog", "ja", "opens in japanese", "en", true},
		{[]string{"ja", "en"}, "田中さん, could you review the attached proposal by Friday?", "ja", "opens with a name", "en", true},
		{[]string{"ja", "en"}, "Please forward the attached file to 田中さん", "ja", "ends with a name", "en", true},
		{[]string{"en", "ko"}, "먼저 npm run build and then npm run test", "ko", "hangul lead and command", "en", true},
		{[]string{"en", "ko"}, "npm run build 후에 npm run test 실행", "ko", "command, ends in hangul", "en", true},
		{[]string{"en", "th"}, "รัน npm run build and then npm run test", "th", "thai verb and command", "en", true},
		{[]string{"en", "ar"}, "شغّل npm run build and then npm run test", "ar", "arabic verb and command", "en", true},
		{[]string{"en", "hi"}, "पहले npm run build and then npm run test", "hi", "hindi lead and command", "en", true},
		{[]string{"ja", "en"}, "コーヒー", "ja", "katakana", "en", true},
		// A flag, URL or path at either end is not a word and leaves the
		// boundary where the last real word put it (#208).
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает --verbose", "ru", "trailing flag", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает https://example.com", "ru", "trailing url", "en", true},
		{[]string{"en", "ru"}, "--verbose Запусти npm run build and then npm run test", "ru", "leading flag", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает -v", "ru", "trailing short flag", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает (https://example.com)", "ru", "trailing url in parens", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает src/main.go", "ru", "trailing path", "en", true},
		{[]string{"en", "ru"}, "The Russian word тоска has no exact equivalent in English. --verbose", "en", "english then flag", "ru", true},
		{[]string{"en", "ru"}, "The Russian word тоска has no exact equivalent in English. См. https://example.com", "ru", "english then cyrillic then url", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает （--verbose）", "ru", "flag in fullwidth parens", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает [--verbose]", "ru", "flag in brackets", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает \"--verbose\"", "ru", "flag in quotes", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает —verbose", "ru", "em dash flag", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает –verbose", "ru", "en dash flag", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает --config=file.json", "ru", "flag with value", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает C:\\build", "ru", "windows path", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает /usr/bin", "ru", "absolute path", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает www.example.com", "ru", "scheme-less url", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает example.com", "ru", "bare domain", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает support@example.com", "ru", "email", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает @username", "ru", "mention", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает #golang", "ru", "hashtag", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает v1.2.3", "ru", "version", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает oauth2", "ru", "letter-digit identifier", "en", true},
		// Words that merely contain a slash, hyphen, apostrophe, dot or digit
		// are still words and mark the boundary.
		{[]string{"en", "ru"}, "you learned how to write тоска and/or", "en", "and/or at the end", "ru", true},
		{[]string{"en", "ru"}, "and/or тоска is what you learned how to write", "en", "and/or at the start", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска either/or", "en", "either/or", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска w/o", "en", "w/o", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска state-of-the-art", "en", "hyphenated word", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска don't", "en", "apostrophe word", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска e.g.", "en", "abbreviation", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска by the 2nd", "en", "ordinal", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска in the 90s", "en", "decade", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска covid19", "ru", "word taken for an identifier: safe side", "en", true},
		// Review of #212, round 2: units and sizes are identifiers, ordinals and
		// the like are words; hyphenated slash prose and dotted names are words.
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 2b", "ru", "model size", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 4k", "ru", "lowercase resolution", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 2fa", "ru", "2fa", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 1gb", "ru", "size unit", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 10ms", "ru", "time unit", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает main.go", "ru", "file name", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает en.wikipedia.org", "ru", "three-label domain", "en", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 2nd", "en", "ordinal 2nd", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 1st", "en", "ordinal 1st", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 90s", "en", "decade at the end", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска in 3D", "en", "3D", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 4K", "en", "4K", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 5pm", "en", "time of day", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 10x", "en", "multiplier", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска client-side/server-side", "en", "hyphenated slash prose", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска front-end/back-end", "en", "hyphenated slash prose 2", "ru", true},
		{[]string{"en", "ru"}, "client-side/server-side тоска is what you learned how to write", "en", "hyphenated slash prose first", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска node.js", "en", "dotted product name", "ru", true},
		{[]string{"en", "ru"}, "node.js тоска is what you learned how to write", "en", "dotted product name first", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска op.cit.", "en", "dotted abbreviation", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска a.k.a.", "en", "one-letter labels", "ru", true},
		// Review of #212, round 3: units, sizes and seconds are identifiers,
		// decades and dimensions are words; one-letter labels and more
		// extensions and top-level domains are file names and domains.
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 10s", "ru", "seconds", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 5s", "ru", "seconds 5s", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 100s", "ru", "100s", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 70B", "ru", "model size 70B", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 7B", "ru", "model size 7B", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 1G", "ru", "memory size 1G", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 2G", "ru", "memory size 2G", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает 1M", "ru", "memory size 1M", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает a.go", "ru", "one-letter file name", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает x.py", "ru", "one-letter file name py", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает x.com", "ru", "one-letter domain", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает t.co", "ru", "one-letter domain co", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает main.c", "ru", "one-letter extension", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает main.cpp", "ru", "cpp", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает test.cc", "ru", "cc", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает index.php", "ru", "php", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает data.sql", "ru", "sql", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает app.rb", "ru", "rb", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает main.swift", "ru", "swift", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает Main.kt", "ru", "kt", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает Main.java", "ru", "java", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает app.wasm", "ru", "wasm", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает example.xyz", "ru", "xyz", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает example.online", "ru", "online", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает example.tech", "ru", "tech", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает example.me", "ru", "me", "en", true},
		{[]string{"en", "ru"}, "npm run build and then npm run test не работает example.us", "ru", "us", "en", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 3d", "en", "lowercase 3d", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 2d", "en", "lowercase 2d", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 20s", "en", "decade 20s", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 1990s", "en", "decade 1990s", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска 2010s", "en", "decade 2010s", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска U.S.A.", "en", "one-letter labels, uppercase", "ru", true},
		{[]string{"en", "ru"}, "you learned how to write тоска Ph.D.", "en", "Ph.D.", "ru", true},
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
		{[]string{"ja", "ko"}, "ー", "ja", "long vowel mark is kana", "ko", true},
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
