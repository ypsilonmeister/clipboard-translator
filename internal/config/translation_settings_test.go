package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateTranslationSettings(t *testing.T) {
	for _, tc := range []struct {
		name, old, want string
		p               Profile
	}{
		{"target to pair", `{"profiles":[{"hotkey" : "Ctrl+Alt+E", "target":"de", "extra":1},{"hotkey":"Ctrl+Alt+T","target":"en"}]}`, `{"profiles":[{"hotkey" : "Ctrl+Alt+E", "target":"", "extra":1,"languages":["ja","en"]},{"hotkey":"Ctrl+Alt+T","target":"en"}]}`, Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}}},
		{"legacy pair", `{"hotkey" : "Ctrl+Alt+E", "languages":["ja","en"]}`, `{"hotkey" : "Ctrl+Alt+E", "languages":["ja","de"]}`, Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "de"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			original := append(append([]byte{}, utf8BOM...), []byte(tc.old)...)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := UpdateTranslationSettings(dir, tc.p, true, nil); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			want := append(append([]byte{}, utf8BOM...), []byte(tc.want)...)
			if !bytes.Equal(got, want) {
				t.Fatalf("got %s want %s", got, want)
			}
			bak, _ := os.ReadFile(path + ".bak")
			if !bytes.Equal(bak, original) {
				t.Fatal("backup changed")
			}
			c, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if c.Profiles[0].Languages[1] != tc.p.Languages[1] {
				t.Fatal(c.Profiles)
			}
		})
	}
}

func TestUpdateTranslationSettingsMissingFile(t *testing.T) {
	dir := t.TempDir()
	p := Profile{Hotkey: "Ctrl+Alt+E", Target: "de"}
	if err := UpdateTranslationSettings(dir, p, true, nil); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Profiles[0].Target != "de" || c.Profiles[0].Hotkey != p.Hotkey {
		t.Fatal(c.Profiles)
	}
}
