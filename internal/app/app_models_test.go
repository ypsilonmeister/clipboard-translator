package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/models/modeltest"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
	"github.com/ypsilonmeister/clipboard-translator/internal/update"
)

// putModel creates a (sparse) file big enough to count as the model's installed file.
func putModel(t *testing.T, dir string, e models.Entry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(e.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(e.MinBytes); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
}

// withModels replaces the catalog for one test.
func withModels(t *testing.T, entries ...models.Entry) {
	t.Helper()
	saved := models.Catalog
	models.Catalog = entries
	t.Cleanup(func() { models.Catalog = saved })
}

func testEntry(key, file string) models.Entry {
	return models.Entry{Key: key, Name: "Model " + key, File: file, Repo: "r", Revision: "v", SizeLabel: "1 MB", MinBytes: 1 << 20}
}

func findModel(st settings.State, key string) settings.ModelState {
	for _, m := range st.Models {
		if m.Key == key {
			return m
		}
	}
	return settings.ModelState{}
}

// newDLFixture starts a fake model host and puts its small model "t1" in the catalog.
func newDLFixture(t *testing.T) *modeltest.Host {
	t.Helper()
	d := modeltest.NewHost(t)
	saved := models.Catalog
	models.Catalog = []models.Entry{{Key: "t1", Name: "Test model", File: "test-model.gguf", Repo: "test-repo", Revision: "rev1",
		Sha256: modeltest.Sum(d.Model), TemplateSha256: modeltest.Sum(d.Tpl), SizeLabel: "1 MB", MinBytes: 1 << 20, Bytes: int64(len(d.Model))}}
	t.Cleanup(func() { models.Catalog = saved })
	return d
}
func TestSettingsStateListsModelsAndEngines(t *testing.T) {
	a, _, _ := newTestApp(t)
	st := a.settingsState()
	if len(st.Models) != len(models.Catalog) || st.LicenseURL != update.PlamoLicenseURL {
		t.Fatalf("models: %+v license %q", st.Models, st.LicenseURL)
	}
	for _, m := range st.Models {
		if m.Installed || m.Active {
			t.Errorf("nothing is installed yet: %+v", m)
		}
	}
	// The test app runs models/m.gguf, which is not in the catalog.
	if st.CustomModel != "models/m.gguf" {
		t.Errorf("custom model: %q", st.CustomModel)
	}
	if len(st.Engines) != 3 || !st.Engines[0].Active || st.Engines[0].Name != "cpu" || st.Engines[0].Label != "CPU" {
		t.Errorf("engines: %+v", st.Engines)
	}
	if st.MaxChars != 4000 {
		t.Errorf("max chars: %d", st.MaxChars)
	}

	e, _ := models.ByKey("0.8b")
	putModel(t, a.dir, e)
	a.conf.ModelFile = "models\\" + e.File // a Windows-style path still names the catalog entry
	st = a.settingsState()
	if m := findModel(st, "0.8b"); !m.Installed || !m.Active || st.CustomModel != "" {
		t.Errorf("0.8b: %+v custom %q", m, st.CustomModel)
	}
	// A file that is too small is not an installed model.
	big, _ := models.ByKey("8b")
	os.WriteFile(big.Path(a.dir), []byte("partial"), 0o644)
	if findModel(a.settingsState(), "8b").Installed {
		t.Error("a truncated file counts as installed")
	}
}

func TestCheckModelSwitch(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a, _, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[0])
	cases := []struct{ key, want string }{
		{"zzz", "unknown model"},
		{"b", "not installed"},
	}
	for _, c := range cases {
		if _, err := a.checkModelSwitch(c.key); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.key, err)
		}
	}
	if _, err := a.checkModelSwitch("a"); err != nil {
		t.Errorf("an installed model must be allowed: %v", err)
	}
	// Still downloading: refused.
	a.dl.SetState(models.DownloadState{Key: "a", Phase: "downloading"})
	if _, err := a.checkModelSwitch("a"); err == nil || !strings.Contains(err.Error(), "still being downloaded") {
		t.Errorf("downloading: %v", err)
	}
	a.dl.SetState(models.DownloadState{})
	// No custom template: refused with a pointer to its configuration.
	os.Remove(a.paths.Template)
	if _, err := a.checkModelSwitch("a"); err == nil || !strings.Contains(err.Error(), "template") {
		t.Errorf("template: %v", err)
	}
}

func TestSwitchModelWorksAndIsSaved(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[1])
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := a.switchModel(models.Catalog[1]); err != nil {
		t.Fatal(err)
	}
	if a.conf.ModelFile != "models/b.gguf" || !strings.HasSuffix(filepath.ToSlash(a.sup.Model), "models/b.gguf") || !strings.HasSuffix(filepath.ToSlash(a.paths.Model), "models/b.gguf") {
		t.Fatalf("not switched: conf=%q sup=%q paths=%q", a.conf.ModelFile, a.sup.Model, a.paths.Model)
	}
	if !healthy(a) {
		t.Error("the server must be running with the new model")
	}
	raw, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if !strings.Contains(string(raw), `"model_file": "models/b.gguf"`) || !strings.Contains(string(raw), `"port"`) {
		t.Errorf("config.json: %s", raw)
	}
	if !strings.Contains(tray.lastNote(), "Model b") && !strings.Contains(tray.lastNote(), "Model: Model b") {
		t.Logf("last note: %q", tray.lastNote())
	}
}

func TestSwitchModelFailureRestoresTheOldOneBeforeRestarting(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("bad", "bad-FAIL.gguf"))
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[1])
	a.conf.ModelFile = "models/m.gguf"
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	oldPaths, oldModel := a.paths, a.sup.Model
	before, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))

	err := a.switchModel(models.Catalog[1]) // the fake server exits at once for a path with FAIL in it
	if err == nil || !strings.Contains(err.Error(), "previous model is back") || !strings.Contains(err.Error(), "smaller") {
		t.Fatalf("error should say the old model is back and suggest a smaller one: %v", err)
	}
	if a.conf.ModelFile != "models/m.gguf" || a.paths != oldPaths || a.sup.Model != oldModel {
		t.Errorf("the old model must be restored: conf=%q sup=%q", a.conf.ModelFile, a.sup.Model)
	}
	if !healthy(a) || !a.sup.Running() {
		t.Error("the server must be running again with the old model")
	}
	if after, _ := os.ReadFile(filepath.Join(a.dir, "config.json")); string(after) != string(before) {
		t.Error("config.json must not change when the new model did not load")
	}
	if tray.lastNote() != err.Error() || !tray.lastError() {
		t.Errorf("the user is not told: %q", tray.lastNote())
	}
	if tray.lastStatus() != a.readyTip {
		t.Errorf("restored model tooltip: %q, want %q", tray.lastStatus(), a.readyTip)
	}
}

func TestSwitchModelFailureWhenRestoreAlsoFails(t *testing.T) {
	for _, source := range []string{"tray", "settings"} {
		t.Run(source, func(t *testing.T) {
			withModels(t, testEntry("bad", "bad-FAIL.gguf"))
			a, tray, _ := newTestApp(t)
			putModel(t, a.dir, models.Catalog[0])
			if err := a.startServer(); err != nil {
				t.Fatal(err)
			}
			oldConf, oldPaths, oldModel := a.conf.ModelFile, a.paths, a.sup.Model
			configPath := filepath.Join(a.dir, "config.json")
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			// The new model fails via its FAIL path; even the previously working
			// model now exits at startup, so rollback cannot restore service.
			t.Setenv("CT_TEST_FAKE_EXIT_CODE", "1")
			if source == "tray" {
				a.handleCommand(cmdModelBase)
			} else {
				req := settings.NewActionRequest("use_model", "bad", time.Minute)
				a.startModelSwitch(req)
				if rep := <-req.Reply; rep.Err != nil || rep.State.Busy == "" {
					t.Fatalf("switch request: %+v", rep)
				}
			}
			const want = "The translation server stopped. Restarting it..."
			if tray.lastNote() != want || !tray.lastError() {
				t.Errorf("recovery notification: %q (error=%v)", tray.lastNote(), tray.lastError())
			}
			if source == "settings" && a.settingsNotice != want {
				t.Errorf("settings must report recovery, got %q", a.settingsNotice)
			}
			if tray.lastStatus() != "Clipboard Translator - server stopped, restarting..." {
				t.Errorf("recovery tooltip: %q", tray.lastStatus())
			}
			if a.sup.Running() || healthy(a) {
				t.Error("neither model should be running")
			}
			if a.conf.ModelFile != oldConf || a.paths != oldPaths || a.sup.Model != oldModel {
				t.Error("recovery must still target the previous model")
			}
			if after, err := os.ReadFile(configPath); err != nil || string(after) != string(before) {
				t.Errorf("failed switch changed config.json: %v", err)
			}
		})
	}
}

func TestServiceSettingsWhileBusyOnlyAnswersStateRequests(t *testing.T) {
	a, _, _ := newTestApp(t)
	form := validForm()
	reqs := map[string]settings.Request{
		"state":   settings.NewRequest(nil, time.Minute),
		"save":    settings.NewRequest(&form, time.Minute),
		"action":  settings.NewActionRequest("use_model", "x", time.Minute),
		"expired": settings.NewRequest(nil, -time.Second),
	}
	for _, r := range reqs {
		a.settingsReqs <- r
	}
	a.serviceSettingsWhileBusy("loading Model b")
	if rep := <-reqs["state"].Reply; rep.Err != nil || rep.State.Busy != "loading Model b" || len(rep.State.Profiles) == 0 {
		t.Errorf("state while busy: %+v", rep)
	}
	for _, k := range []string{"save", "action", "expired"} {
		if rep := <-reqs[k].Reply; rep.Err != settings.ErrBusy {
			t.Errorf("%s while busy must be refused, got %+v", k, rep)
		}
	}
	if len(a.conf.Profiles) != 1 {
		t.Error("a save arriving while busy was applied")
	}
	a.serviceSettingsWhileBusy("x") // nothing queued: returns at once
}

func TestPageSwitchesModelThroughTheMainLoop(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"), testEntry("bad", "bad-FAIL.gguf"))
	f := newSettingsFixture(t)
	a := f.a
	putModel(t, a.dir, models.Catalog[1])
	putModel(t, a.dir, models.Catalog[2])
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	s := f.server(t)

	// Not installed / unknown: refused at once.
	if resp, _ := s.do(t, "POST", "/api/model/use", map[string]string{"key": "a"}, nil); resp.StatusCode != 422 {
		t.Errorf("a model that is not installed: %d", resp.StatusCode)
	}
	if resp, _ := s.do(t, "POST", "/api/model/use", map[string]any{"key": "b", "x": 1}, nil); resp.StatusCode != 400 {
		t.Errorf("unknown field: %d", resp.StatusCode)
	}

	resp, body := s.do(t, "POST", "/api/model/use", map[string]string{"key": "b"}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.State == nil || !strings.Contains(r.State.Busy, "Model b") {
		t.Fatalf("the answer must come first and say the app is busy: %d %s", resp.StatusCode, body)
	}
	// While the server loads, the page's state requests are still answered.
	modeltest.WaitFor(t, "the switch to finish", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		return st != nil && st.Busy == "" && findModel(*st, "b").Active
	})
	_, b := s.do(t, "GET", "/api/state", nil, nil)
	if st := decodeAPI(t, b).State; st.Notice != "" || !healthy(a) {
		t.Errorf("after a good switch: notice %q", st.Notice)
	}

	// A model that cannot load: the old one comes back and the page is told why.
	resp, _ = s.do(t, "POST", "/api/model/use", map[string]string{"key": "bad"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("bad model request: %d", resp.StatusCode)
	}
	modeltest.WaitFor(t, "the failed switch to be reported", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		return st != nil && st.Busy == "" && st.Notice != ""
	})
	_, b = s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, b).State
	if !findModel(*st, "b").Active || findModel(*st, "bad").Active || !strings.Contains(st.Notice, "previous model is back") {
		t.Errorf("after a failed switch: %+v notice %q", st.Models, st.Notice)
	}
	if !healthy(a) {
		t.Error("the server must be running with the old model")
	}
}

func TestPageSwitchesEngine(t *testing.T) {
	f := newSettingsFixture(t)
	a := f.a
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	s := f.server(t)
	if resp, _ := s.do(t, "POST", "/api/engine", map[string]string{"name": "metal"}, nil); resp.StatusCode != 422 {
		t.Errorf("an engine that is not installed: %d", resp.StatusCode)
	}
	if resp, _ := s.do(t, "POST", "/api/engine", map[string]string{"name": "cuda"}, nil); resp.StatusCode != 200 {
		t.Fatalf("cuda: %d", resp.StatusCode)
	}
	modeltest.WaitFor(t, "the engine switch", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		if st == nil || st.Busy != "" {
			return false
		}
		for _, e := range st.Engines {
			if e.Name == "cuda" && e.Active {
				return true
			}
		}
		return false
	})
	// vulkan is a file that cannot be started: the engine stays and the page is told.
	s.do(t, "POST", "/api/engine", map[string]string{"name": "vulkan"}, nil)
	modeltest.WaitFor(t, "the failed engine switch", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		return st != nil && st.Busy == "" && st.Notice != ""
	})
	if config.CurrentBackend(a.conf.ServerFile) != "cuda" || !healthy(a) {
		t.Errorf("the working engine must stay: %s", a.conf.ServerFile)
	}
}

func TestPageDownloadsAModelAndTheTrayIsTold(t *testing.T) {
	d := newDLFixture(t) // a fake model host; the catalog now has the small model "t1"
	f := newSettingsFixture(t)
	f.a.dl = models.NewManager(f.a.dir, version)
	f.a.dl.SetBaseURL(d.Srv.URL)
	t.Cleanup(f.a.dl.Close)
	s := f.server(t)

	if resp, _ := s.do(t, "POST", "/api/model/download", map[string]string{"key": "nope"}, nil); resp.StatusCode != 422 {
		t.Errorf("unknown model: %d", resp.StatusCode)
	}
	resp, body := s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil)
	if resp.StatusCode != 200 || decodeAPI(t, body).State == nil {
		t.Fatalf("download: %d %s", resp.StatusCode, body)
	}
	// The model file appears before the template is fetched, so wait for the end.
	modeltest.WaitFor(t, "the download to finish", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		return st != nil && st.Download != nil && st.Download.Phase == "done" && findModel(*st, "t1").Installed
	})
	_, b := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, b).State
	if st.Download == nil || st.Download.Phase != "done" || findModel(*st, "t1").Downloading {
		t.Errorf("download state: %+v", st.Download)
	}
	// The main loop is told on the tray, not the page's goroutine.
	f.a.onDownloadFinished(<-f.a.dl.Events())
	if note := f.a.tray.(*fakeTray).lastNote(); !strings.Contains(note, "downloaded") {
		t.Errorf("tray note: %q", note)
	}
	if resp, _ := s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil); resp.StatusCode != 422 {
		t.Errorf("a model that is installed cannot be downloaded again: %d", resp.StatusCode)
	}
}

func TestPageRetriesMissingTemplate(t *testing.T) {
	for _, kind := range []string{"http", "hash", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			d := newDLFixture(t)
			d.FailTemplate(kind)
			f := newSettingsFixture(t)
			a := f.a
			a.conf.TemplateFile = "models/chat_template.jinja"
			a.paths.Template = filepath.Join(a.dir, "models", "chat_template.jinja")
			// Even a damaged default template must offer recovery.
			if err := os.WriteFile(a.paths.Template, []byte("broken"), 0o644); err != nil {
				t.Fatal(err)
			}
			a.dl = models.NewManager(a.dir, version)
			a.dl.SetBaseURL(d.Srv.URL)
			t.Cleanup(a.dl.Close)
			s := f.server(t)
			if resp, body := s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil); resp.StatusCode != 200 {
				t.Fatalf("initial download: %d %s", resp.StatusCode, body)
			}
			phase := "failed"
			var resumed int64
			if kind == "cancel" {
				resumed = modeltest.WaitTemplatePartial(t, a.dir, func() string { return a.dl.State().Phase })
				if resp, body := s.do(t, "POST", "/api/model/cancel", map[string]string{}, nil); resp.StatusCode != 204 {
					t.Fatalf("cancel: %d %s", resp.StatusCode, body)
				}
				phase = "cancelled"
			}
			modeltest.WaitFor(t, "template failure on the page", func() bool {
				_, body := s.do(t, "GET", "/api/state", nil, nil)
				st := decodeAPI(t, body).State
				return st != nil && st.Download != nil && st.Download.Phase == phase
			})
			_, body := s.do(t, "GET", "/api/state", nil, nil)
			m := findModel(*decodeAPI(t, body).State, "t1")
			if !m.Installed || m.TemplateInstalled || m.Downloading {
				t.Fatalf("page must distinguish the saved model from the missing template: %+v", m)
			}
			if resp, body := s.do(t, "POST", "/api/model/use", map[string]string{"key": "t1"}, nil); resp.StatusCode != 422 || !strings.Contains(string(body), "retry") {
				t.Fatalf("incomplete model switch must explain recovery: %d %s", resp.StatusCode, body)
			}
			d.RepairTemplate()
			if resp, body := s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil); resp.StatusCode != 200 {
				t.Fatalf("template retry: %d %s", resp.StatusCode, body)
			}
			modeltest.WaitFor(t, "template recovery on the page", func() bool {
				_, body := s.do(t, "GET", "/api/state", nil, nil)
				st := decodeAPI(t, body).State
				if st == nil || st.Download == nil || st.Download.Phase != "done" {
					return false
				}
				m := findModel(*st, "t1")
				return m.Installed && m.TemplateInstalled && !m.Downloading
			})
			d.CheckTemplateRecovery(t, a.dir, resumed)
			// The repaired model can now be selected through the same page.
			if resp, body := s.do(t, "POST", "/api/model/use", map[string]string{"key": "t1"}, nil); resp.StatusCode != 200 {
				t.Fatalf("use repaired model: %d %s", resp.StatusCode, body)
			}
			modeltest.WaitFor(t, "repaired model selection", func() bool {
				_, body := s.do(t, "GET", "/api/state", nil, nil)
				st := decodeAPI(t, body).State
				return st != nil && st.Busy == "" && findModel(*st, "t1").Active
			})
		})
	}
}

func TestDownloadOutlivesTheSettingsPage(t *testing.T) {
	d := newDLFixture(t)
	d.Gate = make(chan struct{})
	f := newSettingsFixture(t)
	f.a.dl = models.NewManager(f.a.dir, version)
	f.a.dl.SetBaseURL(d.Srv.URL)
	t.Cleanup(f.a.dl.Close)
	s := f.server(t)
	s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil)
	modeltest.WaitFor(t, "the download to start", func() bool { return f.a.dl.State().Done > 0 })

	s.Close() // the page is closed (or went idle)
	if !f.a.dl.Downloading("t1") {
		t.Fatal("closing the settings page stopped the download")
	}
	d.Mu.Lock()
	d.Gate <- struct{}{} // let the rest arrive
	d.Mu.Unlock()
	modeltest.WaitFor(t, "the download to finish without a page", func() bool { return f.a.dl.State().Phase == "done" })
	if e, _ := models.ByKey("t1"); !e.Installed(f.a.dir) {
		t.Error("the model is not installed")
	}
}

func TestPageSavesMaxChars(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	form := validForm()
	form.MaxChars = 250
	resp, body := s.do(t, "POST", "/api/save", form, nil)
	if resp.StatusCode != 200 || f.a.conf.MaxChars != 250 || decodeAPI(t, body).State.MaxChars != 250 {
		t.Fatalf("save: %d %s (conf %d)", resp.StatusCode, body, f.a.conf.MaxChars)
	}
	if c, err := config.Load(f.a.dir); err != nil || c.MaxChars != 250 {
		t.Errorf("saved config: %d %v", c.MaxChars, err)
	}
	// Only max_chars changed: the hotkeys were not registered again.
	before := f.reg.Load()
	form.MaxChars = 300
	s.do(t, "POST", "/api/save", form, nil)
	if f.reg.Load() != before || f.a.conf.MaxChars != 300 {
		t.Errorf("max_chars only: registrations %d -> %d, max %d", before, f.reg.Load(), f.a.conf.MaxChars)
	}
	for _, bad := range []int{0, -1, 100001} {
		form.MaxChars = bad
		if resp, _ := s.do(t, "POST", "/api/save", form, nil); resp.StatusCode != 422 {
			t.Errorf("max_chars %d: status %d", bad, resp.StatusCode)
		}
	}
	if f.a.conf.MaxChars != 300 {
		t.Error("an invalid max_chars was applied")
	}
	// The same rule as config.json.
	if err := loadConfig2(t, `{"max_chars": 0}`); err == nil || !strings.Contains(err.Error(), "max_chars") {
		t.Errorf("config.json: %v", err)
	}
	if config.ValidateMaxChars(1) != nil || config.ValidateMaxChars(100000) != nil || config.ValidateMaxChars(100001) == nil {
		t.Error("validateMaxChars")
	}
}

func TestModelDeleteInstalledAndPartial(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	e, _ := models.ByKey("2b")
	putModel(t, f.a.dir, e)
	partialPath := e.Path(f.a.dir) + ".partial"
	if err := os.WriteFile(partialPath, []byte("partial download data"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stateBytes := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, stateBytes).State
	m := findModel(*st, "2b")
	if !m.Installed || !m.HasFile || m.FileBytes != e.MinBytes || !m.Partial || !m.CanDelete || !m.CanDeletePartial || m.DeleteBlocked != "" {
		t.Fatalf("precondition model 2b: %+v", m)
	}

	// 1. Missing kind or invalid kind is rejected with 422.
	respMissing, bodyMissing := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b"}, nil)
	if respMissing.StatusCode != 422 {
		t.Errorf("missing kind should return 422, got %d body %s", respMissing.StatusCode, bodyMissing)
	}
	respInvalid, bodyInvalid := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "invalid"}, nil)
	if respInvalid.StatusCode != 422 {
		t.Errorf("invalid kind should return 422, got %d body %s", respInvalid.StatusCode, bodyInvalid)
	}

	// 2. Delete model file (kind: "model") removes models/<File> but leaves .partial alone.
	resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 || r.State == nil {
		t.Fatalf("delete model: status %d body %s", resp.StatusCode, body)
	}
	if !strings.HasPrefix(r.Notice, "Deleted 2B (") || !strings.HasSuffix(r.Notice, "freed).") {
		t.Errorf("notice: %q", r.Notice)
	}
	// Item 3: Delete result is returned in r.Notice, NOT stored in state.Notice.
	if r.State.Notice != "" {
		t.Errorf("reply state notice must be empty: %q", r.State.Notice)
	}

	// State request after a delete carries no stale notice.
	_, stBytesAfter := s.do(t, "GET", "/api/state", nil, nil)
	stAfter := decodeAPI(t, stBytesAfter).State
	if stAfter.Notice != "" {
		t.Errorf("subsequent state notice must be empty, got %q", stAfter.Notice)
	}

	// An existing error banner is not replaced by a stale success banner.
	f.a.settingsNotice = "an existing engine error"
	_, stErrBytes := s.do(t, "GET", "/api/state", nil, nil)
	stErr := decodeAPI(t, stErrBytes).State
	if stErr.Notice != "an existing engine error" {
		t.Errorf("error banner should remain intact, got %q", stErr.Notice)
	}
	f.a.settingsNotice = ""

	// Verify model file was removed, but partial file remains.
	if _, err := os.Stat(e.Path(f.a.dir)); !os.IsNotExist(err) {
		t.Errorf("model file was not removed: %v", err)
	}
	if _, err := os.Stat(partialPath); err != nil {
		t.Errorf("partial file should survive model file deletion: %v", err)
	}

	mAfterModel := findModel(*r.State, "2b")
	if mAfterModel.Installed || mAfterModel.HasFile || mAfterModel.CanDelete {
		t.Errorf("state after model delete: %+v", mAfterModel)
	}
	if !mAfterModel.Partial || !mAfterModel.CanDeletePartial {
		t.Errorf("partial should still be present and removable: %+v", mAfterModel)
	}

	// 3. Delete partial file (kind: "partial") removes .partial.
	respP, bodyP := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "partial"}, nil)
	rP := decodeAPI(t, bodyP)
	if respP.StatusCode != 200 {
		t.Fatalf("delete partial: status %d body %s", respP.StatusCode, bodyP)
	}
	if !strings.HasPrefix(rP.Notice, "Removed the partial download of 2B (") {
		t.Errorf("partial delete notice: %q", rP.Notice)
	}
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Errorf("partial file was not removed: %v", err)
	}

	// 4. Idempotent calls return 0 B freed.
	resp2, body2 := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
	r2 := decodeAPI(t, body2)
	if resp2.StatusCode != 200 || r2.Notice != "Deleted 2B (0 B freed)." {
		t.Errorf("idempotent model delete: status %d notice %q", resp2.StatusCode, r2.Notice)
	}

	respP2, bodyP2 := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "partial"}, nil)
	rP2 := decodeAPI(t, bodyP2)
	if respP2.StatusCode != 200 || rP2.Notice != "Removed the partial download of 2B (0 B freed)." {
		t.Errorf("idempotent partial delete: status %d notice %q", respP2.StatusCode, rP2.Notice)
	}
}

func TestModelDeleteIncompleteFile(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	e, _ := models.ByKey("8b")
	if err := os.MkdirAll(filepath.Join(f.a.dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an incomplete/corrupt .gguf (< e.MinBytes).
	incompleteData := []byte("corrupt incomplete gguf header")
	if err := os.WriteFile(e.Path(f.a.dir), incompleteData, 0o644); err != nil {
		t.Fatal(err)
	}

	_, stateBytes := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, stateBytes).State
	m := findModel(*st, "8b")
	if m.Installed {
		t.Errorf("incomplete file must not count as installed: %+v", m)
	}
	if !m.HasFile || m.FileBytes != int64(len(incompleteData)) || !m.CanDelete {
		t.Errorf("incomplete file must report has_file and file_bytes and can_delete: %+v", m)
	}

	resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "8b", "kind": "model"}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 {
		t.Fatalf("delete incomplete model: status %d body %s", resp.StatusCode, body)
	}
	wantNotice := fmt.Sprintf("Deleted 8B (%d B freed).", len(incompleteData))
	if r.Notice != wantNotice {
		t.Errorf("notice: want %q, got %q", wantNotice, r.Notice)
	}
	if _, err := os.Stat(e.Path(f.a.dir)); !os.IsNotExist(err) {
		t.Errorf("incomplete model file was not removed: %v", err)
	}
}

func TestModelDeletePartialOnly(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	e, _ := models.ByKey("8b")
	if err := os.MkdirAll(filepath.Join(f.a.dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	partialPath := e.Path(f.a.dir) + ".partial"
	pf, err := os.Create(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.Truncate(312000000); err != nil {
		pf.Close()
		t.Fatal(err)
	}
	pf.Close()

	_, stateBytes := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, stateBytes).State
	m := findModel(*st, "8b")
	if m.Installed || !m.Partial || m.PartialBytes != 312000000 || !m.CanDeletePartial || m.DeleteBlocked != "" {
		t.Fatalf("precondition model 8b partial: %+v", m)
	}

	resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "8b", "kind": "partial"}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 200 {
		t.Fatalf("delete: status %d body %s", resp.StatusCode, body)
	}
	if r.Notice != "Removed the partial download of 8B (312 MB freed)." {
		t.Errorf("notice: %q", r.Notice)
	}
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Errorf("partial file was not removed: %v", err)
	}
	mAfter := findModel(*r.State, "8b")
	if mAfter.Partial || mAfter.CanDeletePartial {
		t.Errorf("state after partial delete: %+v", mAfter)
	}
}

func TestModelDeleteRefusesActiveModel(t *testing.T) {
	e, _ := models.ByKey("0.8b")
	cases := []struct {
		name      string
		modelFile string
	}{
		{"standard forward slash", "models/" + e.File},
		{"windows backslash", "models\\" + e.File},
		{"mixed case uppercase", "MODELS/" + strings.ToUpper(e.File)},
		{"file leaf only", e.File},
		{"different relative spelling", "./models/../models/" + e.File},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSettingsFixture(t)
			s := f.server(t)
			putModel(t, f.a.dir, e)
			f.a.conf.ModelFile = tc.modelFile
			f.a.paths, _ = config.ResolvePaths(f.a.dir, f.a.conf)

			_, stateBytes := s.do(t, "GET", "/api/state", nil, nil)
			st := decodeAPI(t, stateBytes).State
			m := findModel(*st, "0.8b")
			if !m.Active || m.CanDelete || m.DeleteBlocked != "active" {
				t.Fatalf("precondition active model 0.8b: %+v", m)
			}

			// Deleting active model file is refused.
			resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "0.8b", "kind": "model"}, nil)
			r := decodeAPI(t, body)
			if resp.StatusCode != 422 || !strings.Contains(r.Error, "currently in use") {
				t.Errorf("active model deletion: status %d error %q", resp.StatusCode, r.Error)
			}
			if _, err := os.Stat(e.Path(f.a.dir)); err != nil {
				t.Errorf("active model file must not be removed: %v", err)
			}

			// A partial file for the active model CAN be removed!
			partialPath := e.Path(f.a.dir) + ".partial"
			if err := os.WriteFile(partialPath, []byte("leftover partial"), 0o644); err != nil {
				t.Fatal(err)
			}
			respPartial, bodyPartial := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "0.8b", "kind": "partial"}, nil)
			rPartial := decodeAPI(t, bodyPartial)
			if respPartial.StatusCode != 200 || !strings.HasPrefix(rPartial.Notice, "Removed the partial download of 0.8B (") {
				t.Errorf("active model partial deletion should succeed: status %d body %s", respPartial.StatusCode, bodyPartial)
			}
			if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
				t.Errorf("active model partial file was not removed: %v", err)
			}
			// Active model file remains untouched.
			if _, err := os.Stat(e.Path(f.a.dir)); err != nil {
				t.Errorf("active model file must still be present: %v", err)
			}
		})
	}

	// Test custom alias pointing to the catalog model via symlink.
	t.Run("symlink alias", func(t *testing.T) {
		f := newSettingsFixture(t)
		s := f.server(t)
		putModel(t, f.a.dir, e)

		linkPath := filepath.Join(f.a.dir, "models", "custom-alias.gguf")
		if err := os.Symlink(e.Path(f.a.dir), linkPath); err != nil {
			t.Skipf("symlink creation not supported in this environment: %v", err)
		}
		defer os.Remove(linkPath)

		f.a.conf.ModelFile = "models/custom-alias.gguf"
		f.a.paths, _ = config.ResolvePaths(f.a.dir, f.a.conf)

		_, stateBytes := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, stateBytes).State
		m := findModel(*st, "0.8b")
		if !m.Active || m.CanDelete {
			t.Fatalf("catalog model must be detected active via symlink alias: %+v", m)
		}

		resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "0.8b", "kind": "model"}, nil)
		r := decodeAPI(t, body)
		if resp.StatusCode != 422 || !strings.Contains(r.Error, "currently in use") {
			t.Errorf("deleting catalog model while symlink alias is active should be refused: status %d error %q", resp.StatusCode, r.Error)
		}
	})
}

func TestModelDownloadAndDeletionRace(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	e, _ := models.ByKey("2b")
	putModel(t, f.a.dir, e)

	// 1. Delete reservation held -> Start refused.
	release, err := f.a.dl.ReserveDelete("0.8b")
	if err != nil {
		t.Fatalf("ReserveDelete failed: %v", err)
	}

	// dl.Start is refused.
	if err := f.a.dl.Start("0.8b"); err == nil || !strings.Contains(err.Error(), "currently being deleted") {
		t.Errorf("Start during delete reservation should be refused: %v", err)
	}

	// HTTP POST /api/model/download is refused.
	respDl, bodyDl := s.do(t, "POST", "/api/model/download", map[string]string{"key": "0.8b"}, nil)
	rDl := decodeAPI(t, bodyDl)
	if respDl.StatusCode != 422 || !strings.Contains(rDl.Error, "currently being deleted") {
		t.Errorf("HTTP download during delete reservation: status %d body %s", respDl.StatusCode, bodyDl)
	}

	// Release reservation.
	release()

	// 2. Download running -> delete refused.
	f.a.dl.SetState(models.DownloadState{Key: "2b", Phase: "downloading"})
	defer f.a.dl.SetState(models.DownloadState{})

	// dl.ReserveDelete is refused.
	if _, err := f.a.dl.ReserveDelete("2b"); err == nil || !strings.Contains(err.Error(), "currently being downloaded") {
		t.Errorf("ReserveDelete during download should be refused: %v", err)
	}

	// HTTP POST /api/model/delete is refused for kind "model".
	respDelModel, bodyDelModel := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
	rDelModel := decodeAPI(t, bodyDelModel)
	if respDelModel.StatusCode != 422 || !strings.Contains(rDelModel.Error, "currently being downloaded") {
		t.Errorf("HTTP delete model during download: status %d error %q", respDelModel.StatusCode, rDelModel.Error)
	}

	// HTTP POST /api/model/delete is refused for kind "partial".
	respDelPartial, bodyDelPartial := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "partial"}, nil)
	rDelPartial := decodeAPI(t, bodyDelPartial)
	if respDelPartial.StatusCode != 422 || !strings.Contains(rDelPartial.Error, "currently being downloaded") {
		t.Errorf("HTTP delete partial during download: status %d error %q", respDelPartial.StatusCode, rDelPartial.Error)
	}
}

func TestModelDeleteRefusesUnknownAndPathTraversal(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)

	if err := os.MkdirAll(filepath.Join(f.a.dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(f.a.dir, "models", "chat_template.jinja")
	if err := os.WriteFile(templatePath, []byte("shared jinja template"), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelatedPath := filepath.Join(f.a.dir, "models", "unrelated.txt")
	if err := os.WriteFile(unrelatedPath, []byte("keep this"), 0o644); err != nil {
		t.Fatal(err)
	}

	badKeys := []string{
		"unknown",
		"../models/chat_template.jinja",
		"../../something",
		"chat_template.jinja",
		"0.8B", // exact keys are lowercase "0.8b", "2b", "8b"
		"",
	}

	for _, k := range badKeys {
		resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": k, "kind": "model"}, nil)
		r := decodeAPI(t, body)
		if resp.StatusCode != 422 || !strings.Contains(r.Error, "unknown model") {
			t.Errorf("bad key %q: status %d error %q", k, resp.StatusCode, r.Error)
		}
	}

	// Verify template and unrelated files survive untouched.
	tb, err := os.ReadFile(templatePath)
	if err != nil || string(tb) != "shared jinja template" {
		t.Errorf("chat_template.jinja changed: %q %v", tb, err)
	}
	ub, err := os.ReadFile(unrelatedPath)
	if err != nil || string(ub) != "keep this" {
		t.Errorf("unrelated.txt changed: %q %v", ub, err)
	}
}

func TestModelDeleteRefusesNonRegularFile(t *testing.T) {
	f := newSettingsFixture(t)
	s := f.server(t)
	e, _ := models.ByKey("2b")

	// 1. Directory with the catalog name.
	dirPath := e.Path(f.a.dir)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	resp, body := s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
	r := decodeAPI(t, body)
	if resp.StatusCode != 422 || !strings.Contains(r.Error, "not a regular file") {
		t.Errorf("directory model deletion: status %d error %q", resp.StatusCode, r.Error)
	}
	info, err := os.Stat(dirPath)
	if err != nil || !info.IsDir() {
		t.Errorf("directory should survive: info=%v err=%v", info, err)
	}
	_ = os.RemoveAll(dirPath)

	// 2. Symlink if symlink creation is permitted.
	target := filepath.Join(f.a.dir, "models", "target.txt")
	_ = os.WriteFile(target, []byte("target"), 0o644)
	if symErr := os.Symlink(target, dirPath); symErr == nil {
		resp, body = s.do(t, "POST", "/api/model/delete", map[string]string{"key": "2b", "kind": "model"}, nil)
		r = decodeAPI(t, body)
		if resp.StatusCode != 422 || !strings.Contains(r.Error, "not a regular file") {
			t.Errorf("symlink model deletion: status %d error %q", resp.StatusCode, r.Error)
		}
		if _, err := os.Lstat(dirPath); err != nil {
			t.Errorf("symlink should survive: %v", err)
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("target should survive: %v", err)
		}
		_ = os.Remove(dirPath)
	}
}

func findMenuItem(items []platform.MenuItem, label string) *platform.MenuItem {
	for i := range items {
		if items[i].Label == label || strings.HasPrefix(items[i].Label, label) {
			return &items[i]
		}
	}
	return nil
}

func TestTranslationModelMenuInstalledCounts(t *testing.T) {
	for n := 0; n <= 3; n++ {
		t.Run(fmt.Sprintf("%d installed models", n), func(t *testing.T) {
			a, _, _ := newTestApp(t)
			for i := 0; i < n && i < len(models.Catalog); i++ {
				putModel(t, a.dir, models.Catalog[i])
			}
			if n > 0 {
				a.conf.ModelFile = "models/" + models.Catalog[0].File
				paths, err := config.ResolvePaths(a.dir, a.conf)
				if err != nil {
					t.Fatal(err)
				}
				a.paths = paths
			}

			items := a.buildMenu()
			var topLabels []string
			unloadIdx, modelSubIdx, recentIdx := -1, -1, -1
			for idx, it := range items {
				if !it.Separator {
					topLabels = append(topLabels, it.Label)
				}
				switch it.Label {
				case "Unload model":
					unloadIdx = idx
				case "Translation model":
					modelSubIdx = idx
				case "Recent translations (click to copy)":
					recentIdx = idx
				}
			}

			if n < 2 {
				if modelSubIdx != -1 {
					t.Fatalf("Translation model must not appear for %d installed models: %v", n, topLabels)
				}
				if findMenuItem(items, "Translation model") != nil {
					t.Fatalf("findMenuItem found Translation model for %d installed models", n)
				}
				return
			}

			// For n >= 2:
			if modelSubIdx == -1 {
				t.Fatalf("Translation model missing for %d installed models: %v", n, topLabels)
			}
			if modelSubIdx != unloadIdx+1 || recentIdx != modelSubIdx+1 {
				t.Fatalf("Translation model must be immediately after Unload model and before Recent translations: unload=%d sub=%d recent=%d", unloadIdx, modelSubIdx, recentIdx)
			}

			sub := items[modelSubIdx].Sub
			if len(sub) != n {
				t.Fatalf("expected %d submenu entries, got %d", n, len(sub))
			}
			for j := 0; j < n; j++ {
				wantLabel := models.Catalog[j].Name + " (" + models.Catalog[j].SizeLabel + ")"
				if sub[j].Label != wantLabel {
					t.Errorf("entry %d: got label %q, want %q", j, sub[j].Label, wantLabel)
				}
				if sub[j].ID != cmdModelBase+j {
					t.Errorf("entry %d: got ID %d, want %d", j, sub[j].ID, cmdModelBase+j)
				}
				wantChecked := j == 0 // we configured models.Catalog[0]
				if sub[j].Checked != wantChecked {
					t.Errorf("entry %d: got checked %v, want %v", j, sub[j].Checked, wantChecked)
				}
				if sub[j].Disabled {
					t.Errorf("entry %d: unexpectedly disabled while idle", j)
				}
			}
		})
	}
}

func TestTranslationModelMenuCustomModelActive(t *testing.T) {
	a, _, _ := newTestApp(t)
	for _, m := range models.Catalog {
		putModel(t, a.dir, m)
	}
	a.conf.ModelFile = "models/custom-translate.gguf"
	items := a.buildMenu()
	tm := findMenuItem(items, "Translation model")
	if tm == nil {
		t.Fatal("Translation model submenu missing")
	}
	if len(tm.Sub) != len(models.Catalog) {
		t.Fatalf("expected %d entries, got %d", len(models.Catalog), len(tm.Sub))
	}
	for i, sub := range tm.Sub {
		if sub.Checked {
			t.Errorf("entry %d (%q) should not be checked when a custom model is in use", i, sub.Label)
		}
	}
}

func TestTranslationModelMenuDisabledWhileBusy(t *testing.T) {
	a, _, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[0])
	putModel(t, a.dir, models.Catalog[1])
	a.conf.ModelFile = "models/" + models.Catalog[1].File
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		busy         bool
		translating  bool
		serverWork   int
		starting     bool
		settingsServ bool
		unloaded     bool
	}{
		{name: "idle", busy: false},
		{name: "translating", busy: true, translating: true},
		{name: "switch", busy: true, serverWork: 1},
		{name: "startup", busy: true, starting: true},
		{name: "settings", busy: true, settingsServ: true},
		{name: "unloaded", busy: false, unloaded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.translating = tc.translating
			a.serverWork = tc.serverWork
			a.sup.SetStarting(tc.starting)
			a.settingsServing = tc.settingsServ
			a.modelUnloaded = tc.unloaded

			items := a.buildMenu()
			tm := findMenuItem(items, "Translation model")
			if tm == nil {
				t.Fatal("Translation model submenu missing")
			}
			for i, sub := range tm.Sub {
				if sub.Disabled != tc.busy {
					t.Errorf("entry %d (%s) disabled=%v, want %v", i, sub.Label, sub.Disabled, tc.busy)
				}
			}

			if tc.busy {
				// While busy, handleCommand must be a no-op.
				oldConf := a.conf.ModelFile
				a.handleCommand(cmdModelBase + 0)
				if a.conf.ModelFile != oldConf {
					t.Errorf("handleCommand while busy changed model from %s to %s", oldConf, a.conf.ModelFile)
				}
			}
		})
	}
}

func TestTranslationModelMenuSwitching(t *testing.T) {
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[0])
	putModel(t, a.dir, models.Catalog[1])
	a.conf.ModelFile = "models/" + models.Catalog[1].File
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	// Verify initial menu has 2B checked.
	items := a.buildMenu()
	tm := findMenuItem(items, "Translation model")
	if tm == nil || len(tm.Sub) != 2 || !tm.Sub[1].Checked || tm.Sub[0].Checked {
		t.Fatalf("unexpected initial menu state: %+v", tm)
	}

	// Select 0.8B (cmdModelBase + 0).
	a.handleCommand(cmdModelBase + 0)

	// Verify model switched.
	wantModelRel := "models/" + models.Catalog[0].File
	if a.conf.ModelFile != wantModelRel {
		t.Fatalf("conf.ModelFile = %q, want %q", a.conf.ModelFile, wantModelRel)
	}
	if !strings.HasSuffix(filepath.ToSlash(a.paths.Model), wantModelRel) {
		t.Fatalf("paths.model = %q, want suffix %q", a.paths.Model, wantModelRel)
	}
	if !strings.HasSuffix(filepath.ToSlash(a.sup.Model), wantModelRel) {
		t.Fatalf("sup.Model = %q, want suffix %q", a.sup.Model, wantModelRel)
	}
	if !healthy(a) {
		t.Fatal("server is not healthy after model switch")
	}

	// Verify config.json on disk.
	raw, err := os.ReadFile(filepath.Join(a.dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"model_file": "`+wantModelRel+`"`) {
		t.Fatalf("config.json does not contain switched model: %s", raw)
	}

	// Verify tooltip.
	if !strings.Contains(a.readyTip, "ready") || tray.status != a.readyTip {
		t.Fatalf("tooltip status=%q readyTip=%q", tray.status, a.readyTip)
	}

	// Verify notifications: success notification sent, no error notification.
	for i, isErr := range tray.errs {
		if isErr {
			t.Fatalf("unexpected error notification %d: %q", i, tray.notes[i])
		}
	}
	if !strings.Contains(tray.lastNote(), models.Catalog[0].Name) {
		t.Fatalf("last note %q does not contain model name %q", tray.lastNote(), models.Catalog[0].Name)
	}

	// Verify menu checkmark moved to 0.8B.
	items = a.buildMenu()
	tm = findMenuItem(items, "Translation model")
	if tm == nil || len(tm.Sub) != 2 || !tm.Sub[0].Checked || tm.Sub[1].Checked {
		t.Fatalf("after switch menu state invalid: %+v", tm)
	}
}

func TestTranslationModelMenuSelectActiveDoesNothing(t *testing.T) {
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[0])
	putModel(t, a.dir, models.Catalog[1])
	a.conf.ModelFile = "models/" + models.Catalog[0].File
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	noteCount := len(tray.notes)
	oldPID := a.sup.PID()

	// Select 0.8B again.
	a.handleCommand(cmdModelBase + 0)

	if len(tray.notes) != noteCount {
		t.Fatalf("selecting active model produced notifications: %v", tray.notes[noteCount:])
	}
	if a.sup.PID() != oldPID {
		t.Fatalf("server restarted when active model was selected: pid %d != %d", a.sup.PID(), oldPID)
	}
	if a.conf.ModelFile != "models/"+models.Catalog[0].File {
		t.Fatalf("conf.ModelFile changed: %q", a.conf.ModelFile)
	}
}

func TestTranslationModelMenuFailureKeepsOldModel(t *testing.T) {
	t.Run("truncated file at switch time", func(t *testing.T) {
		a, tray, _ := newTestApp(t)
		putModel(t, a.dir, models.Catalog[0])
		putModel(t, a.dir, models.Catalog[1])
		a.conf.ModelFile = "models/" + models.Catalog[1].File
		paths, err := config.ResolvePaths(a.dir, a.conf)
		if err != nil {
			t.Fatal(err)
		}
		a.paths = paths
		a.sup.Model = paths.Model
		if err := a.startServer(); err != nil {
			t.Fatal(err)
		}

		// Truncate 0.8B so checkModelSwitch fails.
		if err := os.Truncate(models.Catalog[0].Path(a.dir), 10); err != nil {
			t.Fatal(err)
		}

		a.handleCommand(cmdModelBase + 0)

		// Old model kept.
		if a.conf.ModelFile != "models/"+models.Catalog[1].File {
			t.Fatalf("model changed on failed switch: %q", a.conf.ModelFile)
		}
		if !healthy(a) {
			t.Fatal("server should still be healthy with old model")
		}

		// Error notification delivered with actionable message.
		if len(tray.errs) == 0 || !tray.errs[len(tray.errs)-1] {
			t.Fatalf("expected error notification on failed switch: errs=%v", tray.errs)
		}
		lastNote := tray.lastNote()
		if !strings.Contains(lastNote, "not installed") && !strings.Contains(lastNote, "download it first") {
			t.Fatalf("notification %q is not actionable", lastNote)
		}
	})

	t.Run("server startup failure restores old model", func(t *testing.T) {
		withModels(t, testEntry("ok", "ok.gguf"), testEntry("bad", "bad-FAIL.gguf"))
		a, tray, _ := newTestApp(t)
		putModel(t, a.dir, models.Catalog[0])
		putModel(t, a.dir, models.Catalog[1])
		a.conf.ModelFile = "models/" + models.Catalog[0].File
		paths, err := config.ResolvePaths(a.dir, a.conf)
		if err != nil {
			t.Fatal(err)
		}
		a.paths = paths
		a.sup.Model = paths.Model
		if err := a.startServer(); err != nil {
			t.Fatal(err)
		}

		a.handleCommand(cmdModelBase + 1)

		// Old model restored.
		if a.conf.ModelFile != "models/"+models.Catalog[0].File {
			t.Fatalf("old model not kept: conf=%q", a.conf.ModelFile)
		}
		if !healthy(a) {
			t.Fatal("server should be restored and healthy with old model")
		}
		if len(tray.errs) == 0 || !tray.errs[len(tray.errs)-1] {
			t.Fatalf("expected error notification: errs=%v", tray.errs)
		}
		if !strings.Contains(tray.lastNote(), "Could not load") || !strings.Contains(tray.lastNote(), "smaller one may work") {
			t.Fatalf("unexpected failure message: %q", tray.lastNote())
		}
	})
}

func TestTranslationModelMenuUnloadedState(t *testing.T) {
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, models.Catalog[0])
	putModel(t, a.dir, models.Catalog[1])
	a.conf.ModelFile = "models/" + models.Catalog[1].File
	paths, err := config.ResolvePaths(a.dir, a.conf)
	if err != nil {
		t.Fatal(err)
	}
	a.paths = paths
	a.sup.Model = paths.Model
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}

	// Unload the model.
	a.unloadModel()
	if !a.modelUnloaded || a.sup.Running() {
		t.Fatal("model should be unloaded")
	}

	// Verify menu in unloaded state.
	items := a.buildMenu()
	unloadItem := findMenuItem(items, "Unload model")
	if unloadItem == nil || !unloadItem.Disabled {
		t.Fatalf("Unload model should be disabled when unloaded: %+v", unloadItem)
	}
	tm := findMenuItem(items, "Translation model")
	if tm == nil || len(tm.Sub) != 2 {
		t.Fatalf("Translation model submenu missing or wrong length: %+v", tm)
	}
	for i, sub := range tm.Sub {
		if sub.Disabled {
			t.Errorf("submenu entry %d (%s) must not be disabled when unloaded", i, sub.Label)
		}
	}
	if !tm.Sub[1].Checked || tm.Sub[0].Checked {
		t.Fatalf("2B must still be checked when unloaded: %+v", tm.Sub)
	}

	// Case A: Selecting already-active model (2B) while unloaded does nothing harmful.
	a.handleCommand(cmdModelBase + 1)
	if !a.modelUnloaded || a.sup.Running() {
		t.Fatal("selecting already-active entry while unloaded must not change unloaded state")
	}

	// Case B: Switch to 0.8B while unloaded.
	a.handleCommand(cmdModelBase + 0)
	if a.modelUnloaded {
		t.Fatal("modelUnloaded must be false after successful model switch")
	}
	if !a.sup.Running() || !healthy(a) {
		t.Fatal("server must be running and healthy with new model")
	}
	if a.conf.ModelFile != "models/"+models.Catalog[0].File {
		t.Fatalf("conf.ModelFile = %q, want 0.8b", a.conf.ModelFile)
	}
	if !strings.Contains(a.readyTip, "ready") || tray.status != a.readyTip {
		t.Fatalf("status not updated to ready: status=%q readyTip=%q", tray.status, a.readyTip)
	}

	// Case C: Switch while unloaded to a damaged file.
	if err := os.Truncate(models.Catalog[1].Path(a.dir), 10); err != nil {
		t.Fatal(err)
	}
	a.unloadModel()
	if !a.modelUnloaded {
		t.Fatal("model should be unloaded")
	}
	a.handleCommand(cmdModelBase + 1) // 2B is truncated
	if !a.modelUnloaded {
		t.Fatal("modelUnloaded must remain true after failed switch attempt")
	}
	if a.conf.ModelFile != "models/"+models.Catalog[0].File {
		t.Fatalf("conf.ModelFile must remain 0.8b: got %q", a.conf.ModelFile)
	}
	if !strings.Contains(tray.status, "model unloaded") {
		t.Fatalf("tooltip status=%q, want model unloaded", tray.status)
	}
	if len(tray.errs) == 0 || !tray.errs[len(tray.errs)-1] {
		t.Fatal("expected error notification on failed switch while unloaded")
	}
}
