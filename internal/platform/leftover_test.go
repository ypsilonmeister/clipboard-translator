package platform

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

const testServerPath = "/Users/me/Library/Application Support/ClipboardTranslator/runtime/llama-server"

func TestChildRecordRoundTrip(t *testing.T) {
	want := childRecord{PID: 4321, Path: testServerPath}
	data, err := encodeChildRecord(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseChildRecord(data)
	if err != nil || got != want {
		t.Fatalf("parseChildRecord = %+v, %v; want %+v", got, err, want)
	}
	for _, bad := range []string{
		"", "garbage", "{}", `{"pid":0,"path":"/x/llama-server"}`, `{"pid":1,"path":"/x/llama-server"}`,
		`{"pid":-5,"path":"/x/llama-server"}`, `{"pid":42,"path":""}`, `{"pid":42,"path":"llama-server"}`,
	} {
		if r, err := parseChildRecord([]byte(bad)); err == nil {
			t.Errorf("parseChildRecord(%q) = %+v, want an error", bad, r)
		}
	}
}

func TestChildRecordPathIncludesPort(t *testing.T) {
	a, b := childRecordPath("dir", 18119), childRecordPath("dir", 18200)
	if a == b || !strings.Contains(a, "18119") {
		t.Fatalf("record paths %q and %q must differ by port", a, b)
	}
}

func TestIsLeftoverServer(t *testing.T) {
	rec := childRecord{PID: 4321, Path: testServerPath}
	const self = 999
	cases := []struct {
		name string
		rec  childRecord
		ps   string
		want bool
	}{
		{"orphaned server", rec, "    1 " + testServerPath + "\n", true},
		{"tab separated", rec, "1\t" + testServerPath, true},
		{"unclean recorded path", childRecord{PID: 4321, Path: "/Users/me/x/../Library/Application Support/ClipboardTranslator/runtime/llama-server"},
			"1 " + testServerPath, true},
		{"pid reused by another program", rec, "1 /usr/sbin/cupsd", false},
		{"user's own llama-server elsewhere", rec, "1 /opt/homebrew/bin/llama-server", false},
		{"bare name only", rec, "1 llama-server", false},
		{"still has a living parent", rec, "  812 " + testServerPath, false},
		{"empty ps output", rec, "", false},
		{"whitespace only", rec, "   \n", false},
		{"no path", rec, "1", false},
		{"garbage ppid", rec, "x " + testServerPath, false},
		{"this process", childRecord{PID: self, Path: testServerPath}, "1 " + testServerPath, false},
		{"pid 1", childRecord{PID: 1, Path: testServerPath}, "1 " + testServerPath, false},
		{"pid 0", childRecord{PID: 0, Path: testServerPath}, "1 " + testServerPath, false},
		{"relative record path", childRecord{PID: 4321, Path: "llama-server"}, "1 llama-server", false},
	}
	for _, c := range cases {
		if got := isLeftoverServer(c.rec, c.ps, self); got != c.want {
			t.Errorf("%s: isLeftoverServer(%+v, %q) = %v, want %v", c.name, c.rec, c.ps, got, c.want)
		}
	}
}

func TestCheckServerPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	go func() { // accept so the dial completes on every OS
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	err = checkServerPort(port)
	if err == nil {
		t.Fatal("a port something listens on must be reported")
	}
	if msg := err.Error(); !strings.Contains(msg, fmt.Sprint(port)) || !strings.Contains(msg, "left over") {
		t.Fatalf("message must name the port and the likely leftover server: %q", msg)
	}
	l.Close()
	if err := checkServerPort(freePort(t)); err != nil {
		t.Fatalf("a free port must not be reported: %v", err)
	}
}

func TestPortInUseMessage(t *testing.T) {
	for goos, hint := range map[string]string{
		"windows": "Task Manager", "darwin": "Activity Monitor", "linux": "ss -ltnp",
	} {
		msg := portInUseMessage(18119, goos)
		for _, want := range []string{"18119", "llama-server", "earlier run", hint, "config.json"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: message lacks %q: %q", goos, want, msg)
			}
		}
		if strings.Contains(msg, "pkill") {
			t.Errorf("%s: must not suggest killing every llama-server: %q", goos, msg)
		}
	}
}
