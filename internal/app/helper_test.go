package app

import (
	"os"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/models"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/testsupport"
)

// TestMain lets the test binary double as a fake llama-server: tests start it
// through the supervisor (os.Args[0]) with CT_TEST_FAKE_SERVER=1 in the
// environment, so crash recovery and engine switching can be tested without
// the real model.
func TestMain(m *testing.M) {
	testsupport.MaybeRunFakeServer()
	// No test may download a real model: model downloads only reach loopback.
	models.DownloadTransport = testsupport.LoopbackOnly()
	// The fake server is a Go program: it does not need the VC++ runtime, whether or not the machine has it.
	platform.VCRuntimeAvailable = func(string) bool { return true }
	os.Exit(m.Run())
}

// fakeTray records what the app tells the tray.
type fakeTray struct {
	quit     bool
	status   string
	statuses []string
	notes    []string
	titles   []string
	errs     []bool
	cmds     []int
	build    func() []platform.MenuItem
}

func (f *fakeTray) QuitRequested() bool { return f.quit }
func (f *fakeTray) SetStatus(s string) {
	f.status = s
	f.statuses = append(f.statuses, s)
}
func (f *fakeTray) Notify(title, msg string, isError bool) {
	f.titles = append(f.titles, title)
	f.notes = append(f.notes, msg)
	f.errs = append(f.errs, isError)
}
func (f *fakeTray) SetMenu(b func() []platform.MenuItem) { f.build = b }
func (f *fakeTray) NextCommand() (int, bool) {
	if len(f.cmds) == 0 {
		return 0, false
	}
	id := f.cmds[0]
	f.cmds = f.cmds[1:]
	return id, true
}
func (f *fakeTray) Close() {}

func (f *fakeTray) lastNote() string {
	if len(f.notes) == 0 {
		return ""
	}
	return f.notes[len(f.notes)-1]
}

func (f *fakeTray) lastTitle() string {
	if len(f.titles) == 0 {
		return ""
	}
	return f.titles[len(f.titles)-1]
}

func (f *fakeTray) lastStatus() string {
	if len(f.statuses) == 0 {
		return ""
	}
	return f.statuses[len(f.statuses)-1]
}

func (f *fakeTray) lastError() bool {
	if len(f.errs) == 0 {
		return false
	}
	return f.errs[len(f.errs)-1]
}

// fakeHotkey is a Hotkey that reports no presses.
type fakeHotkey struct{ spec string }

func (fakeHotkey) Pressed() (bool, error) { return false, nil }
func (fakeHotkey) Close() error           { return nil }
