package settings

import (
	"runtime"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
)

func TestProfileForm_ActionPreservationOnDelete(t *testing.T) {
	// Case 1: [Ctrl+Alt+E: telephone, Ctrl+Alt+T: translate], remove row 0 (Ctrl+Alt+E)
	base1 := config.Config{
		Profiles: []config.Profile{
			{Hotkey: "Ctrl+Alt+E", Action: config.ActionTelephone, Languages: []string{"ja", "en"}},
			{Hotkey: "Ctrl+Alt+T", Action: config.ActionTranslate, Languages: []string{"ja", "en"}},
		},
	}
	f1, err := FormFromProfile(base1.Profiles[1], runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	form1 := Form{
		Changed:  []string{"profiles"},
		Profiles: []ProfileForm{f1},
	}
	cfg1, err := form1.ToConfig(base1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg1.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(cfg1.Profiles))
	}
	if cfg1.Profiles[0].Action != config.ActionTranslate {
		t.Errorf("Case 1 surviving profile Action is %q, want %q", cfg1.Profiles[0].Action, config.ActionTranslate)
	}

	// Case 2: [Ctrl+Alt+T: translate, Ctrl+Alt+E: telephone], remove row 0 (Ctrl+Alt+T)
	base2 := config.Config{
		Profiles: []config.Profile{
			{Hotkey: "Ctrl+Alt+T", Action: config.ActionTranslate, Languages: []string{"ja", "en"}},
			{Hotkey: "Ctrl+Alt+E", Action: config.ActionTelephone, Languages: []string{"ja", "en"}},
		},
	}
	f2, err := FormFromProfile(base2.Profiles[1], runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	form2 := Form{
		Changed:  []string{"profiles"},
		Profiles: []ProfileForm{f2},
	}
	cfg2, err := form2.ToConfig(base2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg2.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(cfg2.Profiles))
	}
	if cfg2.Profiles[0].Action != config.ActionTelephone {
		t.Errorf("Case 2 surviving profile Action is %q, want %q", cfg2.Profiles[0].Action, config.ActionTelephone)
	}
}

func TestProfileForm_ActionValidation(t *testing.T) {
	base := config.Config{
		Profiles: []config.Profile{
			{Hotkey: "Ctrl+Alt+E", Action: config.ActionTelephone, Languages: []string{"ja", "en"}},
		},
	}
	f, err := FormFromProfile(base.Profiles[0], runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}

	// Target mode not allowed for telephone action
	f.Target = "ja"
	f.Languages = nil
	form := Form{Changed: []string{"profiles"}, Profiles: []ProfileForm{f}}
	if _, err := form.ToConfig(base); err == nil {
		t.Errorf("expected error when telephone action uses target mode, got nil")
	}

	// Unsupported action
	f.Target = ""
	f.Languages = []string{"ja", "en"}
	f.Action = "invalid_action"
	form = Form{Changed: []string{"profiles"}, Profiles: []ProfileForm{f}}
	if _, err := form.ToConfig(base); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected unsupported action error, got %v", err)
	}
}
