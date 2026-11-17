package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupConfigRecoveryChoices(t *testing.T) {
	for _, broken := range []struct{ name, content, message string }{
		{"truncated", `{"port":`, "unexpected end of JSON input"},
		{"syntax", "{\n  !\n}", "line 2, column 3"},
		{"range", `{"max_tokens": 0}`, "max_tokens out of range (1..32768)"},
	} {
		for _, choice := range []string{"restore", "editor", "fallback editor", "decline", "editor failure"} {
			t.Run(broken.name+"/"+choice, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.json")
				previous := "\xEF\xBB\xBF{\"max_tokens\": 1234, \"future_key\": true}\r\n"
				writeStartupFile(t, path, broken.content)
				writeStartupFile(t, path+".bak", previous)
				var prompts, opened, fallbacks, errs []string
				a := &app{dir: dir}
				a.askYesNo = func(msg string) bool {
					prompts = append(prompts, msg)
					if !strings.Contains(msg, path) || !strings.Contains(msg, broken.message) {
						t.Errorf("prompt lacks config diagnostic: %q", msg)
					}
					switch len(prompts) {
					case 1:
						if !strings.Contains(msg, "Start with the previous settings") {
							t.Errorf("expected backup offer, got %q", msg)
						}
						return choice == "restore"
					case 2:
						if !strings.Contains(msg, "Open config.json") {
							t.Errorf("expected editor offer, got %q", msg)
						}
						return choice != "decline"
					default:
						t.Fatalf("unexpected prompt: %q", msg)
						return false
					}
				}
				a.openURL = func(target string) error {
					opened = append(opened, target)
					if choice == "fallback editor" || choice == "editor failure" {
						return errors.New("association failed")
					}
					return nil
				}
				a.fallbackEditor = func(target string) error {
					fallbacks = append(fallbacks, target)
					if choice == "editor failure" {
						return errors.New("editor failed")
					}
					return nil
				}
				a.showErr = func(msg string) { errs = append(errs, msg) }
				conf, ok := a.loadStartupConfig()
				if ok != (choice == "restore") {
					t.Fatalf("continue startup = %v for %s", ok, choice)
				}
				if choice == "restore" {
					if conf.MaxTokens != 1234 || len(prompts) != 1 {
						t.Fatalf("previous settings not loaded: %+v; prompts %v", conf, prompts)
					}
					assertStartupFile(t, path, previous)
					assertStartupFile(t, path+".broken", broken.content)
				} else {
					if len(prompts) != 2 {
						t.Fatalf("expected both choices, got %v", prompts)
					}
					assertStartupFile(t, path, broken.content)
					if _, err := os.Stat(path + ".broken"); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("declining restore must not create a copy: %v", err)
					}
				}
				assertStartupFile(t, path+".bak", previous)
				wantOpen := choice != "restore" && choice != "decline"
				if wantOpen && (len(opened) != 1 || opened[0] != path) || !wantOpen && len(opened) != 0 {
					t.Errorf("opened paths = %v", opened)
				}
				wantFallback := choice == "fallback editor" || choice == "editor failure"
				if wantFallback && (len(fallbacks) != 1 || fallbacks[0] != path) || !wantFallback && len(fallbacks) != 0 {
					t.Errorf("fallback paths = %v", fallbacks)
				}
				if choice == "editor failure" {
					if len(errs) != 1 || !strings.Contains(errs[0], path) || !strings.Contains(errs[0], "text editor") {
						t.Errorf("missing manual recovery instructions: %v", errs)
					}
				} else if len(errs) != 0 {
					t.Errorf("unexpected errors: %v", errs)
				}
			})
		}
	}
}

func TestStartupConfigWithoutUsableBackup(t *testing.T) {
	for _, backup := range []string{"missing", "", `{"max_tokens":`, `{"max_tokens": 0}`} {
		t.Run(backup, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			writeStartupFile(t, path, `{"port":`)
			if backup != "missing" {
				writeStartupFile(t, path+".bak", backup)
			}
			prompts, opens := 0, 0
			a := &app{
				dir: dir,
				askYesNo: func(msg string) bool {
					prompts++
					if !strings.Contains(msg, "Open config.json") || strings.Contains(msg, "Start with the previous settings") {
						t.Errorf("unusable backup offered: %q", msg)
					}
					return true
				},
				openURL: func(target string) error {
					opens++
					if target != path {
						t.Errorf("editor opened %q, want %q", target, path)
					}
					return nil
				},
				showErr: func(msg string) { t.Errorf("unexpected error: %s", msg) },
			}
			if _, ok := a.loadStartupConfig(); ok || prompts != 1 || opens != 1 {
				t.Fatalf("continue = %v, prompts = %d, opens = %d", ok, prompts, opens)
			}
			assertStartupFile(t, path, `{"port":`)
			if backup != "missing" {
				assertStartupFile(t, path+".bak", backup)
			}
			if _, err := os.Stat(path + ".broken"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("editor choice must not create a copy: %v", err)
			}
		})
	}
}

func TestStartupConfigHealthyOrMissing(t *testing.T) {
	for _, existing := range []bool{false, true} {
		dir := t.TempDir()
		if existing {
			writeStartupFile(t, filepath.Join(dir, "config.json"), `{"max_tokens": 1234}`)
		}
		a := &app{dir: dir, askYesNo: func(msg string) bool {
			t.Fatalf("unexpected prompt: %s", msg)
			return false
		}}
		conf, ok := a.loadStartupConfig()
		if !ok || existing && conf.MaxTokens != 1234 || !existing && conf.MaxTokens != 2048 {
			t.Fatalf("existing=%v, config=%+v, continue=%v", existing, conf, ok)
		}
	}
}

func TestStartupConfigRestorePreservesEarlierCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeStartupFile(t, path, `{"port":`)
	writeStartupFile(t, path+".bak", `{"max_tokens": 1234}`)
	writeStartupFile(t, path+".broken", "earlier broken config")
	writeStartupFile(t, path+".broken.1", "another broken config")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		dir:      dir,
		askYesNo: func(string) bool { return true },
		showErr:  func(msg string) { t.Errorf("unexpected error: %s", msg) },
	}
	if conf, ok := a.loadStartupConfig(); !ok || conf.MaxTokens != 1234 {
		t.Fatalf("previous settings not loaded: %+v, %v", conf, ok)
	}
	assertStartupFile(t, path+".broken", "earlier broken config")
	assertStartupFile(t, path+".broken.1", "another broken config")
	assertStartupFile(t, path+".broken.2", `{"port":`)
	assertStartupFile(t, path, `{"max_tokens": 1234}`)
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("restoration must preserve file identity for Scoop hard links: %v", err)
	}
}

func TestStartupConfigRestoreFailureStopsStartup(t *testing.T) {
	for _, failure := range []string{"backup changed", "cannot read broken file"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			writeStartupFile(t, path, `{"port":`)
			writeStartupFile(t, path+".bak", `{"max_tokens": 1234}`)
			var errs []string
			a := &app{
				dir: dir,
				askYesNo: func(string) bool {
					if failure == "backup changed" {
						writeStartupFile(t, path+".bak", `{"max_tokens": 0}`)
					} else if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					return true
				},
				showErr: func(msg string) { errs = append(errs, msg) },
			}
			if _, ok := a.loadStartupConfig(); ok || len(errs) != 1 || !strings.Contains(errs[0], path) {
				t.Fatalf("failed restoration must stop and report its path: continue=%v, errors=%v", ok, errs)
			}
			if failure == "backup changed" {
				assertStartupFile(t, path, `{"port":`)
			} else {
				assertStartupFile(t, path+".bak", `{"max_tokens": 1234}`)
			}
			if _, err := os.Stat(path + ".broken"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("read failure must stop before writing anything: %v", err)
			}
		})
	}
}

func writeStartupFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertStartupFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != want {
		t.Fatalf("%s contents = %q, %v; want %q", path, b, err, want)
	}
}
