package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// putModel creates a (sparse) file big enough to count as the model's installed file.
func putModel(t *testing.T, dir string, e modelEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(e.path(dir))
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
func withModels(t *testing.T, entries ...modelEntry) {
	t.Helper()
	saved := modelCatalog
	modelCatalog = entries
	t.Cleanup(func() { modelCatalog = saved })
}

func testEntry(key, file string) modelEntry {
	return modelEntry{Key: key, Name: "Model " + key, File: file, Repo: "r", Revision: "v", SizeLabel: "1 MB", MinBytes: 1 << 20}
}

func findModel(st settingsState, key string) modelState {
	for _, m := range st.Models {
		if m.Key == key {
			return m
		}
	}
	return modelState{}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSettingsStateListsModelsAndEngines(t *testing.T) {
	a, _, _ := newTestApp(t)
	st := a.settingsState()
	if len(st.Models) != len(modelCatalog) || st.LicenseURL != plamoLicenseURL {
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

	e, _ := modelByKey("0.8b")
	putModel(t, a.dir, e)
	a.conf.ModelFile = "models\\" + e.File // a Windows-style path still names the catalog entry
	st = a.settingsState()
	if m := findModel(st, "0.8b"); !m.Installed || !m.Active || st.CustomModel != "" {
		t.Errorf("0.8b: %+v custom %q", m, st.CustomModel)
	}
	// A file that is too small is not an installed model.
	big, _ := modelByKey("8b")
	os.WriteFile(big.path(a.dir), []byte("partial"), 0o644)
	if findModel(a.settingsState(), "8b").Installed {
		t.Error("a truncated file counts as installed")
	}
}

func TestCheckModelSwitch(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a, _, _ := newTestApp(t)
	putModel(t, a.dir, modelCatalog[0])
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
	a.dl.set(func(s *downloadState) { *s = downloadState{Key: "a", Phase: "downloading"} })
	if _, err := a.checkModelSwitch("a"); err == nil || !strings.Contains(err.Error(), "still being downloaded") {
		t.Errorf("downloading: %v", err)
	}
	a.dl.set(func(s *downloadState) { *s = downloadState{} })
	// No template: refused with a pointer to setup.
	os.Remove(a.paths.template)
	if _, err := a.checkModelSwitch("a"); err == nil || !strings.Contains(err.Error(), "template") {
		t.Errorf("template: %v", err)
	}
}

func TestSwitchModelWorksAndIsSaved(t *testing.T) {
	withModels(t, testEntry("a", "a.gguf"), testEntry("b", "b.gguf"))
	a, tray, _ := newTestApp(t)
	putModel(t, a.dir, modelCatalog[1])
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := a.switchModel(modelCatalog[1]); err != nil {
		t.Fatal(err)
	}
	if a.conf.ModelFile != "models/b.gguf" || !strings.HasSuffix(filepath.ToSlash(a.sup.model), "models/b.gguf") || !strings.HasSuffix(filepath.ToSlash(a.paths.model), "models/b.gguf") {
		t.Fatalf("not switched: conf=%q sup=%q paths=%q", a.conf.ModelFile, a.sup.model, a.paths.model)
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
	putModel(t, a.dir, modelCatalog[1])
	a.conf.ModelFile = "models/m.gguf"
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	oldPaths, oldModel := a.paths, a.sup.model
	before, _ := os.ReadFile(filepath.Join(a.dir, "config.json"))

	err := a.switchModel(modelCatalog[1]) // the fake server exits at once for a path with FAIL in it
	if err == nil || !strings.Contains(err.Error(), "previous model is back") || !strings.Contains(err.Error(), "smaller") {
		t.Fatalf("error should say the old model is back and suggest a smaller one: %v", err)
	}
	if a.conf.ModelFile != "models/m.gguf" || a.paths != oldPaths || a.sup.model != oldModel {
		t.Errorf("the old model must be restored: conf=%q sup=%q", a.conf.ModelFile, a.sup.model)
	}
	if !healthy(a) || !a.sup.Running() {
		t.Error("the server must be running again with the old model")
	}
	if after, _ := os.ReadFile(filepath.Join(a.dir, "config.json")); string(after) != string(before) {
		t.Error("config.json must not change when the new model did not load")
	}
	if !strings.Contains(tray.lastNote(), "Could not load") {
		t.Errorf("the user is not told: %q", tray.lastNote())
	}
}

func TestServiceSettingsWhileBusyOnlyAnswersStateRequests(t *testing.T) {
	a, _, _ := newTestApp(t)
	form := validForm()
	reqs := map[string]settingsRequest{
		"state":   newSettingsRequest(nil, time.Minute),
		"save":    newSettingsRequest(&form, time.Minute),
		"action":  newActionRequest("use_model", "x", time.Minute),
		"expired": newSettingsRequest(nil, -time.Second),
	}
	for _, r := range reqs {
		a.settingsReqs <- r
	}
	a.serviceSettingsWhileBusy("loading Model b")
	if rep := <-reqs["state"].reply; rep.err != nil || rep.state.Busy != "loading Model b" || len(rep.state.Profiles) == 0 {
		t.Errorf("state while busy: %+v", rep)
	}
	for _, k := range []string{"save", "action", "expired"} {
		if rep := <-reqs[k].reply; rep.err != errSettingsBusy {
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
	putModel(t, a.dir, modelCatalog[1])
	putModel(t, a.dir, modelCatalog[2])
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
	waitFor(t, "the switch to finish", func() bool {
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
	waitFor(t, "the failed switch to be reported", func() bool {
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
	waitFor(t, "the engine switch", func() bool {
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
	waitFor(t, "the failed engine switch", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		return st != nil && st.Busy == "" && st.Notice != ""
	})
	if currentBackend(a.conf.ServerFile) != "cuda" || !healthy(a) {
		t.Errorf("the working engine must stay: %s", a.conf.ServerFile)
	}
}

func TestPageDownloadsAModelAndTheTrayIsTold(t *testing.T) {
	d := newDLFixture(t) // a fake model host; the catalog now has the small model "t1"
	f := newSettingsFixture(t)
	f.a.dl = newDownloadManager(f.a.dir)
	f.a.dl.baseURL = d.srv.URL
	t.Cleanup(f.a.dl.Close)
	s := f.server(t)

	if resp, _ := s.do(t, "POST", "/api/model/download", map[string]string{"key": "nope"}, nil); resp.StatusCode != 422 {
		t.Errorf("unknown model: %d", resp.StatusCode)
	}
	resp, body := s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil)
	if resp.StatusCode != 200 || decodeAPI(t, body).State == nil {
		t.Fatalf("download: %d %s", resp.StatusCode, body)
	}
	waitFor(t, "the model to be installed", func() bool {
		_, b := s.do(t, "GET", "/api/state", nil, nil)
		st := decodeAPI(t, b).State
		return st != nil && findModel(*st, "t1").Installed
	})
	_, b := s.do(t, "GET", "/api/state", nil, nil)
	st := decodeAPI(t, b).State
	if st.Download == nil || st.Download.Phase != "done" || findModel(*st, "t1").Downloading {
		t.Errorf("download state: %+v", st.Download)
	}
	// The main loop is told on the tray, not the page's goroutine.
	f.a.onDownloadFinished(<-f.a.dl.events)
	if note := f.a.tray.(*fakeTray).lastNote(); !strings.Contains(note, "downloaded") {
		t.Errorf("tray note: %q", note)
	}
	if resp, _ := s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil); resp.StatusCode != 422 {
		t.Errorf("a model that is installed cannot be downloaded again: %d", resp.StatusCode)
	}
}

func TestDownloadOutlivesTheSettingsPage(t *testing.T) {
	d := newDLFixture(t)
	d.gate = make(chan struct{})
	f := newSettingsFixture(t)
	f.a.dl = newDownloadManager(f.a.dir)
	f.a.dl.baseURL = d.srv.URL
	t.Cleanup(f.a.dl.Close)
	s := f.server(t)
	s.do(t, "POST", "/api/model/download", map[string]string{"key": "t1"}, nil)
	waitFor(t, "the download to start", func() bool { return f.a.dl.State().Done > 0 })

	s.Close() // the page is closed (or went idle)
	if !f.a.dl.Downloading("t1") {
		t.Fatal("closing the settings page stopped the download")
	}
	d.mu.Lock()
	d.gate <- struct{}{} // let the rest arrive
	d.mu.Unlock()
	waitFor(t, "the download to finish without a page", func() bool { return f.a.dl.State().Phase == "done" })
	if e, _ := modelByKey("t1"); !e.installed(f.a.dir) {
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
	if c, err := loadConfig(f.a.dir); err != nil || c.MaxChars != 250 {
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
	if validateMaxChars(1) != nil || validateMaxChars(100000) != nil || validateMaxChars(100001) == nil {
		t.Error("validateMaxChars")
	}
}
