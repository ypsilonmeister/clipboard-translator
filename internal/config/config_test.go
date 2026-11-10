package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadConfigPortRange(t *testing.T) {
	for _, port := range []string{"1024", "18119", "65534"} {
		if _, err := Load(writeConfig(t, `{"port": `+port+`}`)); err != nil {
			t.Errorf("port %s must be accepted: %v", port, err)
		}
	}
	// 65535 would make the single-instance lock (port+1) listen on 65536.
	for _, port := range []string{"65535", "65536", "1023", "0", "-1"} {
		err := loadConfig2(t, `{"port": `+port+`}`)
		if err == nil || !strings.Contains(err.Error(), "1024..65534") {
			t.Errorf("port %s must be rejected with the valid range, got %v", port, err)
		}
	}
}

func loadConfig2(t *testing.T, content string) error {
	t.Helper()
	_, err := Load(writeConfig(t, content))
	return err
}

func TestLoadConfigHotkeyValidation(t *testing.T) {
	good := []string{"Ctrl+Alt+E", "ctrl+shift+y", "Control + Alt + T", "Win+Q", "Ctrl+Alt+Shift+Z", "Ctrl+Alt+1", "Ctrl+F5", "Win+9", "Ctrl+Shift+F12", "alt+control+0"}
	for _, h := range good {
		if err := loadConfig2(t, `{"hotkey": "`+h+`"}`); err != nil {
			t.Errorf("hotkey %q must be accepted: %v", h, err)
		}
	}
	bad := []string{"F8", "E", "1", "F5", "F0", "F13", "Ctrl+Alt", "Ctrl+Alt+E+R", "Ctrl+1+2", "Ctrl+F1+F2", "Ctrl++E", "Ctrl+Alt+", "Ctrl+Alt+Esc", "Ctrl+Alt+F0", "Ctrl+Alt+F13", "Ctrl+Alt+!", "Hyper+E", ""}
	for _, h := range bad {
		err := loadConfig2(t, `{"hotkey": "`+h+`"}`)
		if err == nil {
			t.Errorf("hotkey %q must be rejected by loadConfig", h)
		} else if h != "" && !strings.Contains(err.Error(), "hotkey") {
			t.Errorf("hotkey %q: the error should say what is wrong with the hotkey, got %v", h, err)
		}
	}
	// "Super" is the X11 name for the Windows key; Windows does not know it.
	err := validateHotkey("Super+E")
	if runtime.GOOS == "windows" && err == nil {
		t.Error("Super+E must be rejected on Windows")
	}
	if runtime.GOOS != "windows" && err != nil {
		t.Errorf("Super+E must be accepted off Windows: %v", err)
	}
}

func TestLoadConfigAcceptsUTF8BOM(t *testing.T) {
	dir := writeConfig(t, "\xEF\xBB\xBF"+`{"hotkey":"Ctrl+Alt+T","port":18200}`)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("a BOM-prefixed config.json must load: %v", err)
	}
	if c.Hotkey != "Ctrl+Alt+T" || c.Port != 18200 {
		t.Fatalf("loaded %+v", c)
	}
}

func TestUpdateConfigFileAcceptsUTF8BOM(t *testing.T) {
	original := "\xEF\xBB\xBF" + `{
  "hotkey": "Ctrl+Alt+E",
  "some_future_setting": [1, 2],
  "port": 18119
}
`
	dir := writeConfig(t, original)
	if err := UpdateFile(dir, map[string]any{"hotkey": "Ctrl+Alt+J"}); err != nil {
		t.Fatalf("a BOM-prefixed config.json must be updatable: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	want := `{
  "hotkey": "Ctrl+Alt+J",
  "some_future_setting": [1,2],
  "port": 18119
}
`
	if string(b) != want {
		t.Fatalf("config.json =\n%q\nwant (no BOM, order and unknown keys kept)\n%q", b, want)
	}
	if bak, _ := os.ReadFile(filepath.Join(dir, "config.json.bak")); string(bak) != original {
		t.Fatalf("config.json.bak must hold the previous bytes untouched, got %q", bak)
	}
	if c, err := Load(dir); err != nil || c.Hotkey != "Ctrl+Alt+J" {
		t.Fatalf("loadConfig after update: %+v, %v", c, err)
	}
}

func TestParseConfigEntries(t *testing.T) {
	entries, err := parseConfigEntries([]byte("\xEF\xBB\xBF \n{\"a\": 1, \"b\": {\"x\": [1]}, \"a\": 2}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].key != "a" || string(entries[0].val) != "2" || entries[1].key != "b" {
		t.Fatalf("duplicate keys must collapse to the first position with the last value: %+v", entries)
	}
	if entries, err = parseConfigEntries([]byte(`{}`)); err != nil || len(entries) != 0 {
		t.Fatalf("empty object: %+v, %v", entries, err)
	}
	for name, in := range map[string]string{
		"trailing object":  `{"a": 1} {"b": 2}`,
		"trailing text":    `{"a": 1} x`,
		"trailing comma":   `{"a": 1},`,
		"truncated":        `{"a": 1`,
		"truncated string": `{"a": "x`,
		"array":            `[1]`,
		"empty":            ``,
		"bom only":         "\xEF\xBB\xBF",
	} {
		if _, err := parseConfigEntries([]byte(in)); err == nil {
			t.Errorf("%s: %q must be rejected", name, in)
		}
	}
}

func TestUpdateConfigFileRefusesTrailingDataAndDedups(t *testing.T) {
	bad := writeConfig(t, `{"hotkey": "Ctrl+Alt+E"} garbage`)
	if err := UpdateFile(bad, map[string]any{"hotkey": "Ctrl+Alt+J"}); err == nil {
		t.Fatal("trailing data must be refused")
	}
	if b, _ := os.ReadFile(filepath.Join(bad, "config.json")); string(b) != `{"hotkey": "Ctrl+Alt+E"} garbage` {
		t.Fatal("a refused update must leave the file untouched")
	}
	dup := writeConfig(t, `{"port": 18119, "hotkey": "Ctrl+Alt+E", "port": 18200}`)
	if err := UpdateFile(dup, map[string]any{"auto_paste": true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dup, "config.json"))
	if want := "{\n  \"port\": 18200,\n  \"hotkey\": \"Ctrl+Alt+E\",\n  \"auto_paste\": true\n}\n"; string(b) != want {
		t.Fatalf("config.json =\n%s\nwant\n%s", b, want)
	}
}

func TestPathWithin(t *testing.T) {
	base := t.TempDir()
	good := map[string]string{
		"models/m.gguf":     filepath.Join(base, "models", "m.gguf"),
		`models\m.gguf`:     "", // checked below: separators differ per OS
		"./models/m.gguf":   filepath.Join(base, "models", "m.gguf"),
		"models/../a.bin":   filepath.Join(base, "a.bin"),
		"runtime/cpu/x.exe": filepath.Join(base, "runtime", "cpu", "x.exe"),
	}
	for in, want := range good {
		if in == `models\m.gguf` && runtime.GOOS != "windows" {
			continue
		}
		if want == "" {
			want = filepath.Join(base, "models", "m.gguf")
		}
		got, err := PathWithin(base, in)
		if err != nil || got != want {
			t.Errorf("pathWithin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", " ", ".", "./", "models/..", "models/../.",
		"..", "../x", "models/../../x",
		"/etc/passwd", `\windows\system32`, `\\server\share\f`,
		filepath.Join(base, "abs.gguf"),
	}
	if runtime.GOOS == "windows" {
		bad = append(bad, `..\x`, `C:foo`, `c:models\m.gguf`, `C:\abs.gguf`, `C:`, `D:\`)
	}
	for _, in := range bad {
		if got, err := PathWithin(base, in); err == nil {
			t.Errorf("pathWithin(%q) = %q, want an error", in, got)
		}
	}
}

func TestResolvePathsNamesTheBadKey(t *testing.T) {
	dir := t.TempDir()
	conf := Default()
	conf.TemplateFile = ""
	_, err := ResolvePaths(dir, conf)
	if err == nil || !strings.Contains(err.Error(), "template_file") {
		t.Fatalf("resolvePaths must say which key is wrong, got %v", err)
	}
	for key, set := range map[string]func(*Config){
		"model_file":           func(c *Config) { c.ModelFile = "." },
		"server_file":          func(c *Config) { c.ServerFile = "" },
		"fallback_server_file": func(c *Config) { c.FallbackServerFile = "models/.." },
	} {
		c := Default()
		set(&c)
		if _, err := ResolvePaths(dir, c); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: got %v", key, err)
		}
	}
}

func TestMissingTreatsDirectoriesAsMissing(t *testing.T) {
	dir := t.TempDir()
	conf := Default()
	p, err := ResolvePaths(dir, conf)
	if err != nil {
		t.Fatal(err)
	}
	// A folder where a file is expected must not count as installed.
	for _, f := range []string{p.Model, p.Template, p.Server} {
		if err := os.MkdirAll(f, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Missing(); len(got) != 3 {
		t.Fatalf("folders in place of files: missing = %v, want all 3", got)
	}
	if err := CheckFile(p.Model); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("checkFile on a folder = %v", err)
	}
	if err := CheckFile(filepath.Join(dir, "nope")); !os.IsNotExist(err) {
		t.Fatalf("checkFile on a missing path = %v", err)
	}
	for _, f := range []string{p.Model, p.Template, p.Server} {
		os.Remove(f)
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Missing(); len(got) != 0 {
		t.Fatalf("regular files: missing = %v", got)
	}
}

func TestHotkeyDuplicateDetection(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		{"Ctrl+Alt+E", "alt+control+e"},
		{"ctrl+f5", "Ctrl+F5"},
		{"Ctrl+7", "ctrl+7"},
		{"Shift+Ctrl+F12", "ctrl+shift+f12"},
		{"Win+Alt+0", "alt+win+0"},
	}
	for _, tc := range cases {
		c := Default()
		c.Profiles = []Profile{
			{Hotkey: tc.a, Languages: []string{"ja", "en"}},
			{Hotkey: tc.b, Target: "ko"},
		}
		if err := c.ResolveProfiles(true); err == nil || !strings.Contains(err.Error(), "same key combination") {
			t.Errorf("expected duplicate error for %q and %q, got %v", tc.a, tc.b, err)
		}
	}
}

func TestProfileOutputConfig(t *testing.T) {
	// Legacy config without profiles defaults to replace.
	c, err := Load(writeConfig(t, `{"hotkey": "Ctrl+Alt+E"}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Output != OutputReplace {
		t.Fatalf("legacy config output: %+v, %v", c.Profiles, err)
	}

	// Profile with output omitted defaults to replace.
	c, err = Load(writeConfig(t, `{"profiles": [{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]}]}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Output != OutputReplace {
		t.Fatalf("omitted output: %+v, %v", c.Profiles, err)
	}

	// Explicit replace and bilingual modes with case/whitespace normalization.
	c, err = Load(writeConfig(t, `{"profiles": [
		{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"], "output": " Replace "},
		{"hotkey": "Ctrl+Alt+K", "target": "ko", "output": "BILINGUAL"}
	]}`))
	if err != nil || len(c.Profiles) != 2 {
		t.Fatalf("explicit output modes: %+v, %v", c.Profiles, err)
	}
	if c.Profiles[0].Output != OutputReplace {
		t.Errorf("profile 0 output = %q, want %q", c.Profiles[0].Output, OutputReplace)
	}
	if c.Profiles[1].Output != OutputBilingual {
		t.Errorf("profile 1 output = %q, want %q", c.Profiles[1].Output, OutputBilingual)
	}

	// Unknown output value rejected with helpful error listing valid values.
	err = loadConfig2(t, `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "ko", "output": "invalid_mode"}]}`)
	if err == nil {
		t.Fatal("unknown output mode must be rejected")
	}
	for _, want := range []string{"profiles[0]", "invalid_mode", "replace", "bilingual"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err.Error(), want)
		}
	}
}

func TestProfileInputConfig(t *testing.T) {
	// Legacy config without profiles defaults to selection.
	c, err := Load(writeConfig(t, `{"hotkey": "Ctrl+Alt+E"}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Input != InputSelection {
		t.Fatalf("legacy config input: %+v, %v", c.Profiles, err)
	}
	// Omitted input defaults to selection; case and spaces are ignored.
	c, err = Load(writeConfig(t, `{"profiles": [
		{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]},
		{"hotkey": "Ctrl+Alt+K", "target": "ko", "input": " Clipboard "},
		{"hotkey": "Ctrl+Alt+D", "target": "de", "input": "SELECTION"}
	]}`))
	if err != nil || len(c.Profiles) != 3 {
		t.Fatalf("explicit input modes: %+v, %v", c.Profiles, err)
	}
	for i, want := range []string{InputSelection, InputClipboard, InputSelection} {
		if c.Profiles[i].Input != want || c.Profiles[i].InputMode() != want {
			t.Errorf("profile %d input = %q, want %q", i, c.Profiles[i].Input, want)
		}
	}
	// An unknown value is rejected with an error that lists the valid values.
	err = loadConfig2(t, `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "ko", "input": "mouse"}]}`)
	if err == nil {
		t.Fatal("unknown input mode must be rejected")
	}
	for _, want := range []string{"profiles[0]", "mouse", "selection", "clipboard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err.Error(), want)
		}
	}
}

func TestUpdateConfigFilePreservesUnknownKeysWithProfileOutput(t *testing.T) {
	dir := writeConfig(t, `{
  "custom_unknown_key": "custom_value",
  "port": 18119,
  "profiles": [
    {
      "hotkey": "Ctrl+Alt+E",
      "languages": ["ja", "en"],
      "output": "replace"
    }
  ]
}
`)
	newProfiles := []Profile{
		{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: OutputBilingual},
	}
	if err := UpdateFile(dir, map[string]any{"profiles": newProfiles}); err != nil {
		t.Fatalf("updateConfigFile: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.Contains(content, `"custom_unknown_key": "custom_value"`) {
		t.Fatalf("unknown key lost: %s", content)
	}
	if !strings.Contains(content, `"output":"bilingual"`) {
		t.Fatalf("bilingual output not saved: %s", content)
	}
	loaded, err := Load(dir)
	if err != nil || len(loaded.Profiles) != 1 || loaded.Profiles[0].Output != OutputBilingual {
		t.Fatalf("loaded config: %+v, %v", loaded, err)
	}
}

func TestProfileCleanupNewlinesConfig(t *testing.T) {
	// Legacy config without profiles defaults to off.
	c, err := Load(writeConfig(t, `{"hotkey": "Ctrl+Alt+E"}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].CleanupNewlines != CleanupNewlinesOff {
		t.Fatalf("legacy config cleanup_newlines: %+v, %v", c.Profiles, err)
	}

	// Profile with omitted cleanup_newlines defaults to off.
	c, err = Load(writeConfig(t, `{"profiles": [{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]}]}`))
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].CleanupNewlines != CleanupNewlinesOff {
		t.Fatalf("omitted cleanup_newlines: %+v, %v", c.Profiles, err)
	}

	// Explicit off and auto modes with case and whitespace trimming.
	c, err = Load(writeConfig(t, `{"profiles": [
		{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"], "cleanup_newlines": " Off "},
		{"hotkey": "Ctrl+Alt+K", "target": "ko", "cleanup_newlines": "AUTO"}
	]}`))
	if err != nil || len(c.Profiles) != 2 {
		t.Fatalf("explicit cleanup_newlines modes: %+v, %v", c.Profiles, err)
	}
	if c.Profiles[0].CleanupNewlines != CleanupNewlinesOff || c.Profiles[0].CleanupNewlinesMode() != CleanupNewlinesOff {
		t.Errorf("profile 0 cleanup_newlines = %q, want %q", c.Profiles[0].CleanupNewlines, CleanupNewlinesOff)
	}
	if c.Profiles[1].CleanupNewlines != CleanupNewlinesAuto || c.Profiles[1].CleanupNewlinesMode() != CleanupNewlinesAuto {
		t.Errorf("profile 1 cleanup_newlines = %q, want %q", c.Profiles[1].CleanupNewlines, CleanupNewlinesAuto)
	}

	// Unknown cleanup_newlines value rejected with helpful error listing valid values.
	err = loadConfig2(t, `{"profiles": [{"hotkey": "Ctrl+Alt+E", "target": "ko", "cleanup_newlines": "sometimes"}]}`)
	if err == nil {
		t.Fatal("unknown cleanup_newlines mode must be rejected")
	}
	for _, want := range []string{"profiles[0]", "sometimes", "off", "auto"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err.Error(), want)
		}
	}
}
