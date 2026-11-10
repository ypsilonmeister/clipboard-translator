package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSleepPumpingHonorsQuitRequests(t *testing.T) {
	for _, kind := range []string{"STOP", "signal", "tray"} {
		t.Run(kind, func(t *testing.T) {
			tray := &fakeTray{}
			a := &app{dir: t.TempDir(), tray: tray, sig: make(chan os.Signal, 1)}
			switch kind {
			case "STOP":
				if err := os.WriteFile(filepath.Join(a.dir, "STOP"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "signal":
				a.sig <- syscall.SIGTERM
			case "tray":
				tray.quit = true
			}
			started := time.Now()
			if !a.sleepPumping(3 * time.Second) {
				t.Fatal("quit request ignored")
			}
			if elapsed := time.Since(started); elapsed >= 2*time.Second {
				t.Fatalf("quit delayed by %s", elapsed)
			}
			if !a.quitRequested() {
				t.Fatal("quit request was lost")
			}
		})
	}
}
