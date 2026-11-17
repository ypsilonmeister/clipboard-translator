package app

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// TestInstanceCheck_TwoInstances verifies that the primary instance acquires the listener,
// and a second instance connects, matches the banner, triggers onPing on the primary,
// and returns (nil, nil) with no error.
func TestInstanceCheck_TwoInstances(t *testing.T) {
	port := freePort(t)

	pinged := make(chan struct{}, 1)
	inst1, err := checkInstance(port, func() {
		select {
		case pinged <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("first instance check failed: %v", err)
	}
	if inst1 == nil {
		t.Fatal("expected non-nil listener for first instance")
	}
	defer inst1.Close()

	inst2, err := checkInstance(port)
	if err != nil {
		t.Fatalf("second instance check failed: %v", err)
	}
	if inst2 != nil {
		t.Fatalf("expected nil listener for second instance, got %v", inst2)
	}

	select {
	case <-pinged:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first instance to receive ping")
	}

	// After the first instance closes, another instance can acquire the port.
	if err := inst1.Close(); err != nil {
		t.Fatalf("inst1.Close: %v", err)
	}

	inst3, err := checkInstance(port)
	if err != nil {
		t.Fatalf("third instance check after close failed: %v", err)
	}
	if inst3 == nil {
		t.Fatal("expected non-nil listener for third instance")
	}
	_ = inst3.Close()
}

// TestInstanceCheck_ForeignListener verifies that when an unrelated program listens
// on the instance port and sends arbitrary non-matching data, checkInstance returns
// a clear, actionable error for the user.
func TestInstanceCheck_ForeignListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\nForeign Service"))
			_ = conn.Close()
		}
	}()

	inst, err := checkInstance(port)
	if err == nil {
		if inst != nil {
			_ = inst.Close()
		}
		t.Fatal("expected error when foreign listener holds port, got nil")
	}

	want := fmt.Sprintf("Port %d is used by another program. Set a different `port` in config.json.", port)
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

// TestInstanceCheck_ForeignListenerClosesImmediately verifies handling of a foreign listener
// that immediately closes incoming connections without sending any data.
func TestInstanceCheck_ForeignListenerClosesImmediately(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	inst, err := checkInstance(port)
	if err == nil {
		if inst != nil {
			_ = inst.Close()
		}
		t.Fatal("expected error when foreign listener closes immediately, got nil")
	}

	want := fmt.Sprintf("Port %d is used by another program. Set a different `port` in config.json.", port)
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

// TestInstanceCheck_ForeignListenerHangs verifies that checkInstance does not hang indefinitely
// when a foreign listener accepts connections but never replies.
func TestInstanceCheck_ForeignListenerHangs(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	inst, err := checkInstance(port)
	if err == nil {
		if inst != nil {
			_ = inst.Close()
		}
		t.Fatal("expected error when foreign listener hangs, got nil")
	}

	want := fmt.Sprintf("Port %d is used by another program. Set a different `port` in config.json.", port)
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

// TestInstanceListener_SetOnPingPending verifies that if a ping arrives before
// setOnPing is called (e.g. during application startup), the callback fires as
// soon as setOnPing is set.
func TestInstanceListener_SetOnPingPending(t *testing.T) {
	port := freePort(t)

	inst, err := checkInstance(port)
	if err != nil {
		t.Fatalf("checkInstance: %v", err)
	}
	defer inst.Close()

	// Second instance connects before onPing is installed.
	matched, err := handshake(port)
	if err != nil || !matched {
		t.Fatalf("handshake before setOnPing: matched=%v, err=%v", matched, err)
	}

	fired := make(chan struct{}, 1)
	inst.setOnPing(func() {
		fired <- struct{}{}
	})

	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("pending ping did not fire when setOnPing was called")
	}
}

// TestApp_ShowAlreadyRunning verifies that calling ping/showAlreadyRunning causes the app
// to post the expected notification to the tray.
func TestApp_ShowAlreadyRunning(t *testing.T) {
	tray := &fakeTray{}
	a := newApp(t.TempDir(), config.Default(), config.Paths{}, tray, io.Discard)

	a.showAlreadyRunning()

	if tray.lastTitle() != "Clipboard Translator" {
		t.Errorf("tray title = %q, want %q", tray.lastTitle(), "Clipboard Translator")
	}
	wantNote := "Clipboard Translator is already running. Its icon is here in the tray."
	if tray.lastNote() != wantNote {
		t.Errorf("tray note = %q, want %q", tray.lastNote(), wantNote)
	}
	if tray.lastError() != false {
		t.Errorf("tray error = %v, want false", tray.lastError())
	}
}

type pingQuitTray struct {
	fakeTray
	wantNote string
}

func (t *pingQuitTray) Notify(title, msg string, isError bool) {
	t.fakeTray.Notify(title, msg, isError)
	if msg == t.wantNote {
		t.quit = true
	}
}

// TestApp_ServeHandlesPing verifies that a ping delivered to an app in its serve
// loop is processed and triggers the tray notification on the UI thread.
func TestApp_ServeHandlesPing(t *testing.T) {
	wantNote := "Clipboard Translator is already running. Its icon is here in the tray."
	tray := &pingQuitTray{wantNote: wantNote}
	a := newApp(t.TempDir(), config.Default(), config.Paths{}, tray, io.Discard)
	a.newHotkey = func(spec string) (platform.Hotkey, error) { return fakeHotkey{}, nil }

	// Send a ping to the app.
	a.ping()

	cb := &testClipboard{}
	done := make(chan struct{})
	go func() {
		a.serve(cb)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = os.WriteFile(filepath.Join(a.dir, "STOP"), nil, 0600)
		<-done
		t.Fatal("serve did not exit after ping notification")
	}

	if tray.lastNote() != wantNote {
		t.Fatalf("tray note = %q, want %q", tray.lastNote(), wantNote)
	}
}

// TestInstanceListener_AcceptLoopDoesNotReadData verifies that the instance listener
// sends the banner without reading any data from the incoming connection.
func TestInstanceListener_AcceptLoopDoesNotReadData(t *testing.T) {
	port := freePort(t)

	var pings atomic.Int32
	inst, err := checkInstance(port, func() {
		pings.Add(1)
	})
	if err != nil {
		t.Fatalf("checkInstance: %v", err)
	}
	defer inst.Close()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read banner from server without sending any data.
	buf := make([]byte, len(instanceBanner))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read banner: %v", err)
	}
	if string(buf) != instanceBanner {
		t.Fatalf("got banner %q, want %q", string(buf), instanceBanner)
	}

	// Wait briefly for ping callback to record.
	time.Sleep(50 * time.Millisecond)
	if got := pings.Load(); got != 1 {
		t.Fatalf("got %d pings, want 1", got)
	}
}
