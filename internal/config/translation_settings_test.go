package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateTranslationSettings(t *testing.T) {
	for _, tc := range []struct {
		name, old, want string
		p               Profile
	}{
		{"target to pair", `{"profiles":[{"hotkey" : "Ctrl+Alt+E", "target":"de", "extra":1},{"hotkey":"Ctrl+Alt+T","target":"en"}]}`, `{"profiles":[{"hotkey" : "Ctrl+Alt+E", "target":"", "extra":1,"languages":["ja","en"]},{"hotkey":"Ctrl+Alt+T","target":"en"}]}`, Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}}},
		{"target without languages", `{"profiles":[{"hotkey":"Ctrl+Alt+E","target":"de"}]}`, `{"profiles":[{"hotkey":"Ctrl+Alt+E","target":"fr"}]}`, Profile{Hotkey: "Ctrl+Alt+E", Target: "fr"}},
		{"pair without target", `{"profiles":[{"hotkey":"Ctrl+Alt+E","languages":["ja","en"]}]}`, `{"profiles":[{"hotkey":"Ctrl+Alt+E","languages":["ja","de"]}]}`, Profile{Languages: []string{"ja", "de"}}},
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
			if c.Profiles[0].Target != tc.p.Target || (len(tc.p.Languages) > 0 && c.Profiles[0].Languages[1] != tc.p.Languages[1]) {
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

func TestTranslationSaveNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	old := []byte(`{"profiles":[{"languages": ["ja", "en"]}], "future": true}`)
	os.WriteFile(path, old, 0600)
	if err := UpdateTranslationSettings(dir, Profile{Languages: []string{"ja", "en"}}, true, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, old) {
		t.Fatal(string(got))
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("no-op created backup")
	}
}

// Saving legacy language fields must not relax the loader's empty-array contract.
func TestTranslationSaveEmptyProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	old := []byte(`{"profiles": [], "languages":["ja","en"], "future":true}`)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateTranslationSettings(dir, Profile{Languages: []string{"ja", "de"}}, true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"profiles": [], "languages":["ja","de"], "future":true}`; string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bak, old) {
		t.Fatal("backup changed")
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty profiles must remain invalid at load time: %v", err)
	}
}
