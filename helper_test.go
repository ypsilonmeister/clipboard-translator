package main

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

// TestMain lets the test binary double as a fake llama-server: tests start it
// through the supervisor (os.Args[0]) with CT_TEST_FAKE_SERVER=1 in the
// environment, so crash recovery and engine switching can be tested without
// the real model.
func TestMain(m *testing.M) {
	if os.Getenv("CT_TEST_FAKE_SERVER") == "1" {
		runFakeServer()
		return
	}
	if runPlatformSmokeTests() {
		return
	}
	os.Exit(m.Run())
}

func runFakeServer() {
	port := "0"
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	_ = http.ListenAndServe("127.0.0.1:"+port, mux)
}

// fakeTray records what the app tells the tray.
type fakeTray struct {
	quit   bool
	status string
	notes  []string
	cmds   []int
	build  func() []MenuItem
}

func (f *fakeTray) QuitRequested() bool { return f.quit }
func (f *fakeTray) SetStatus(s string)  { f.status = s }
func (f *fakeTray) Notify(title, msg string, isError bool) {
	f.notes = append(f.notes, msg)
}
func (f *fakeTray) SetMenu(b func() []MenuItem) { f.build = b }
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

// fakeHotkey is a Hotkey that reports no presses.
type fakeHotkey struct{ spec string }

func (fakeHotkey) Pressed() (bool, error) { return false, nil }
func (fakeHotkey) Close() error           { return nil }
