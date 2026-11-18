package textstruct

import "testing"

func TestTidyRules(t *testing.T) {
	cases := []struct {
		name, rule, input, want string
	}{
		{"newlines/prose", "newlines", "This is a long line from a PDF that wraps in the middle of\na sentence and should be joined.", "This is a long line from a PDF that wraps in the middle of a sentence and should be joined."},
		{"newlines/Japanese", "newlines", "本研究では、小型エッジデバイス上で高速かつ高精度に\n動作する翻訳モデルの実現を目的としています。", "本研究では、小型エッジデバイス上で高速かつ高精度に動作する翻訳モデルの実現を目的としています。"},
		{"newlines/list", "newlines", "- first item\n- second item\n1. third item", "- first item\n- second item\n1. third item"},
		{"newlines/address", "newlines", "Taro Yamada\n123 Main Street\nTokyo 100-0001", "Taro Yamada\n123 Main Street\nTokyo 100-0001"},
		{"width/alphanumerics", "width", "ＡＢＣａｂｃ０１２３", "ABCabc0123"},
		{"width/kana", "width", "ｶﾞｯﾂﾎﾟｰｽﾞ ｳﾞｧｲｵﾘﾝ ﾜﾞｦﾞ", "ガッツポーズ ヴァイオリン ヷヺ"},
		{"width/all kana", "width", "ｦｧｨｩｪｫｬｭｮｯｰｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ", "ヲァィゥェォャュョッーアイウエオカキクケコサシスセソタチツテトナニヌネノハヒフヘホマミムメモヤユヨラリルレロワン"},
		{"width/marks", "width", "ｶﾟ ﾞ ﾟ", "カ゜ ゛ ゜"},
		{"width/symbols", "width", "① ㍍ ＋　「｡､･」", "① ㍍ ＋　「｡､･」"},
		{"width/list", "width", "1. ASCII\n- カタカナ", "1. ASCII\n- カタカナ"},
		{"width/address", "width", "123 Main St.\nTokyo 100-0001", "123 Main St.\nTokyo 100-0001"},
		{"punct/halfwidth", "punct", "日本語､句読点｡", "日本語、句読点。"},
		{"punct/numeric separators", "punct", "３．１４ １，０００ 3．14", "３．１４ １，０００ 3．14"},
		{"punct/dotted address", "punct", "example．com", "example．com"},
		{"punct/Japanese", "punct", "日本語，句読点．", "日本語、句読点。"},
		{"punct/ASCII", "punct", "Version 1.2, 3.14.\n123 Main St., Tokyo", "Version 1.2, 3.14.\n123 Main St., Tokyo"},
		{"punct/list", "punct", "1. first\n2. second", "1. first\n2. second"},
		{"spaces/prose", "spaces", "a  b   c \nnext  line   \r\n", "a b c\nnext line\r\n"},
		{"spaces/trailing tab and wide space", "spaces", "hello \t　\n    \n", "hello\n\n"},
		{"spaces/two trailing", "spaces", "hello  ", "hello"},
		{"spaces/indent", "spaces", "  hello  world ", "  hello world"},
		{"spaces/list", "spaces", "- first  item  \n  * second   item", "- first  item  \n  * second   item"},
		{"spaces/address", "spaces", "Taro Yamada\n123 Main Street\nTokyo 100-0001", "Taro Yamada\n123 Main Street\nTokyo 100-0001"},
		{"spaces/table", "spaces", "Name\tCount  \n| Name  | Count |", "Name\tCount  \n| Name  | Count |"},
		{"quotes/smart", "quotes", "“Hello”, ‘world’, don’t.", "\"Hello\", 'world', don't."},
		{"quotes/other", "quotes", "「引用」 『保持』 5′ 6″", "「引用」 『保持』 5′ 6″"},
		{"quotes/list", "quotes", "1. one\n- two", "1. one\n- two"},
		{"quotes/address", "quotes", "123 Main St.\nTokyo", "123 Main St.\nTokyo"},
		{"bullets/markers", "bullets", "・ one\n* two\n  • three\n    ・ nested\n- four", "- one\n- two\n  - three\n    - nested\n- four"},
		{"bullets/literal", "bullets", "A・B\n*word*\n-1\n***\n---\n* * *", "A・B\n*word*\n-1\n***\n---\n* * *"},
		{"bullets/ordered and tasks", "bullets", "1. one\n(a) two\n* [x] task\n- [ ] todo", "1. one\n(a) two\n* [x] task\n- [ ] todo"},
		{"bullets/address", "bullets", "123-4 Main Street\nTokyo", "123-4 Main Street\nTokyo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tidy(tc.input, []string{tc.rule}, "japanese")
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if again := Tidy(got, []string{tc.rule}, "japanese"); again != got {
				t.Errorf("rule is not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestTidyRulesPreserveCodeAndAddresses(t *testing.T) {
	inputs := []string{
		"print(“ＡＢＣ  ｶﾞ，word．”)\n",
		"```text\nＡＢＣ  ｶﾞ，‘word’．\n* item\n```\n",
		"~~~~text\n```\nＡＢＣ  ｶﾞ，‘word’．\n~~~~\n",
		"```\nＡＢＣ  ｶﾞ，‘word’．", // unclosed fence
		"    ＡＢＣ  ｶﾞ，‘word’．\n\t* code  ",
		"const s = “ＡＢＣ  ｶﾞ，word．”;",
		"x := “ＡＢＣ  ｶﾞ，word．”",
		"Use `ＡＢＣ  ｶﾞ，‘word’．` literally.",
		"https://example.com/ＡＢＣ?q=‘word’\nuser＠example.com\nuser@example.com  ",
	}
	for _, rule := range []string{"newlines", "width", "punct", "spaces", "quotes", "bullets"} {
		t.Run(rule, func(t *testing.T) {
			for _, input := range inputs {
				if got := Tidy(input, []string{rule}, "japanese"); got != input {
					t.Errorf("changed protected text %q to %q", input, got)
				}
			}
		})
	}
}

func TestTidyOptInAndPunctuationStyle(t *testing.T) {
	input := "Ａ  ｶﾞ，“hello”．"
	if got := Tidy(input, nil, "japanese"); got != input {
		t.Fatalf("no rules changed text to %q", got)
	}
	if got := CleanPunctuation("文、末。 A, B. 1.2", "fullwidth"); got != "文，末． A, B. 1.2" {
		t.Fatalf("fullwidth punctuation: %q", got)
	}
	if got := Tidy(input, []string{"width", "punct", "spaces", "quotes"}, "japanese"); got != "A ガ、\"hello\"。" {
		t.Fatalf("combined rules: %q", got)
	}
}
