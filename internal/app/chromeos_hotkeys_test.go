package app

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestChromeOSHotkeysUnavailable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux capability profile")
	}
	for _, wayland := range []bool{false, true} {
		for _, unavailable := range []bool{false, true} {
			a := apiUnitApp(t)
			a.cb = &asyncDesktopClipboard{testClipboard: a.cb.(*testClipboard), state: platform.DesktopStatus{Wayland: wayland, Manual: wayland && unavailable, PortalWorkflowUnavailable: unavailable}}
			before, err := json.Marshal(a.conf)
			if err != nil {
				t.Fatal(err)
			}
			st := a.settingsState()
			want := ""
			if unavailable {
				want = "Not available on ChromeOS: hotkeys cannot be triggered from Chrome or other host apps."
			}
			if st.HotkeysUnavailable != want {
				t.Fatalf("wayland=%v unavailable=%v: note=%q", wayland, unavailable, st.HotkeysUnavailable)
			}
			data, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["hotkeys_unavailable"]; present != unavailable {
				t.Fatalf("flag presence=%v, want %v", present, unavailable)
			}
			after, err := json.Marshal(a.conf)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("settings state changed saved configuration")
			}
		}
	}
}
