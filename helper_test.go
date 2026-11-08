package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
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
	// No test may download a real model: model downloads only reach loopback.
	downloadTransport = loopbackOnly{}
	os.Exit(m.Run())
}

func runFakeServer() {
	port := "0"
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	// A model path with FAIL in it makes the fake server exit at once, like a
	// model that cannot be loaded (too little memory, a damaged file).
	for i, a := range os.Args {
		if a == "-m" && i+1 < len(os.Args) && strings.Contains(os.Args[i+1], "FAIL") {
			os.Exit(1)
		}
	}
	// CT_TEST_FAKE_SERVER_DELAY (milliseconds) makes the fake server slow to come up,
	// like a big model loading, so a "busy" screen can be looked at.
	if ms, _ := strconv.Atoi(os.Getenv("CT_TEST_FAKE_SERVER_DELAY")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	// Like llama-server: with LLAMA_API_KEY set, every endpoint but /health
	// needs "Authorization: Bearer <key>"; without it, nothing is checked.
	key := os.Getenv("LLAMA_API_KEY")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"Invalid API Key","type":"authentication_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"`+fakeTranslation+`"},"finish_reason":"stop"}]}`)
	})
	_ = http.ListenAndServe("127.0.0.1:"+port, mux)
}

// fakeTranslation is what the fake server answers to every chat request.
const fakeTranslation = "fake translation"

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
