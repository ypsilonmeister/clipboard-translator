package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
)

func TestProfileIDsSurviveDeletingMiddleProfileAndReordering(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"profiles":[{"hotkey":"Ctrl+E","target":"en"},{"hotkey":"Ctrl+J","target":"ja"},{"hotkey":"Ctrl+K","target":"ko"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var form Form
	form.Changed = []string{"profiles"}
	// Remove the middle profile, then reverse the surviving rows.
	for _, i := range []int{2, 0} {
		row, err := FormFromProfile(c.Profiles[i], runtime.GOOS)
		if err != nil {
			t.Fatal(err)
		}
		form.Profiles = append(form.Profiles, row)
	}
	// A new profile can use the deleted profile's requested trigger without
	// inheriting its portal binding.
	newID := ""
	form.Profiles = append(form.Profiles, ProfileForm{ID: &newID, Ctrl: true, Key: "J", Target: "ja"})
	saved, err := form.ToConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.UpdateFile(dir, map[string]any{"profiles": saved.Profiles}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := reloaded.Profiles
	if p[0].Hotkey != "Ctrl+K" || p[0].ID != "profile_3" || p[1].Hotkey != "Ctrl+E" || p[1].ID != "profile_1" {
		t.Fatalf("surviving profiles lost their IDs: %+v", p)
	}
	if p[2].ID == "" || p[2].ID == "profile_2" {
		t.Fatalf("new profile reused a removed binding: %+v", p[2])
	}
}
