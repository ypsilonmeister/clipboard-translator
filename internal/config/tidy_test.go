package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileActionValidation(t *testing.T) {
	cases := []struct {
		name, profile, wantAction, wantError string
	}{
		{"default", `{"hotkey":"Ctrl+Alt+T","target":"en"}`, ActionTranslate, ""},
		{"translate", `{"hotkey":"Ctrl+Alt+T","action":"translate","target":"en"}`, ActionTranslate, ""},
		{"tidy without languages", `{"hotkey":"Ctrl+Alt+T","action":"tidy","rules":["newlines","width","punct","spaces","quotes","bullets"]}`, ActionTidy, ""},
		{"tidy no rules", `{"hotkey":"Ctrl+Alt+T","action":"tidy"}`, ActionTidy, ""},
		{"normalize action", `{"hotkey":"Ctrl+Alt+T","action":" TIDY "}`, ActionTidy, ""},
		{"bad action", `{"hotkey":"Ctrl+Alt+T","action":"rewrite","target":"en"}`, "", "action"},
		{"bad tidy rule", `{"hotkey":"Ctrl+Alt+T","action":"tidy","rules":["newline"]}`, "", "rules[0]"},
		{"bad translate rule", `{"hotkey":"Ctrl+Alt+T","target":"en","rules":["unknown"]}`, "", "rules[0]"},
		{"empty rule", `{"hotkey":"Ctrl+Alt+T","action":"tidy","rules":[""]}`, "", "rules[0]"},
		{"bad style", `{"hotkey":"Ctrl+Alt+T","action":"tidy","punct_style":"ascii"}`, "", "punct_style"},
		{"bad output still rejected", `{"hotkey":"Ctrl+Alt+T","action":"tidy","output":"invalid"}`, "", "output"},
		{"bad languages still rejected", `{"hotkey":"Ctrl+Alt+T","action":"tidy","target":"xx"}`, "", "target"},
		{"bad hotkey", `{"hotkey":"T","action":"tidy"}`, "", "profiles[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"profiles":[`+tc.profile+`]}`), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(dir)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), "profiles[0]") || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want indexed %q error, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil || c.Profiles[0].ActionMode() != tc.wantAction {
				t.Fatalf("got %+v, %v", c.Profiles, err)
			}
			if c.NeedsTranslation() != (tc.wantAction == ActionTranslate) {
				t.Fatal("wrong model startup decision")
			}
		})
	}
}

func TestTidyCheckDoesNotUseTranslationHeuristics(t *testing.T) {
	p := Profile{Action: ActionTidy}
	for _, input := range []string{"１２３４", "“”", "https://example.com", "カタカナ"} {
		if reason, _ := p.Check(input, 100); reason != SkipNone {
			t.Errorf("tidy skipped %q: %v", input, reason)
		}
	}
	if reason, _ := p.Check("", 100); reason != SkipEmpty {
		t.Fatal("empty input must still be skipped")
	}
	if reason, _ := p.Check("１２３４", 3); reason != SkipTooLong {
		t.Fatal("tidy must honor max_chars")
	}
	if !Default().NeedsTranslation() {
		t.Fatal("legacy defaults must translate")
	}
	c := Config{Profiles: []Profile{{Action: ActionTidy}, {Target: "en"}}}
	if !c.NeedsTranslation() {
		t.Fatal("mixed profiles need the translation engine")
	}
}
