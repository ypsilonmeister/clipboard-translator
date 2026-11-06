package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartBackoff(t *testing.T) {
	want := map[int]time.Duration{
		0: time.Second, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		5: 16 * time.Second, 6: 30 * time.Second, 50: 30 * time.Second,
	}
	for attempt, d := range want {
		if got := restartBackoff(attempt); got != d {
			t.Errorf("restartBackoff(%d) = %v, want %v", attempt, got, d)
		}
	}
}

func TestRestartLimiter(t *testing.T) {
	r := restartLimiter{max: 3, window: 10 * time.Minute}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		if !r.Allow(t0.Add(time.Duration(i) * time.Minute)) {
			t.Fatalf("restart %d must be allowed", i)
		}
		if r.Attempt() != i {
			t.Fatalf("Attempt = %d, want %d", r.Attempt(), i)
		}
	}
	if r.Allow(t0.Add(4 * time.Minute)) {
		t.Fatal("a 4th restart within the window must be refused")
	}
	// At minute 12 the window is (2, 12]: the restarts at minutes 1 and 2 are out
	// (a restart exactly one window ago no longer counts), minute 3 is still in.
	if !r.Allow(t0.Add(12 * time.Minute)) {
		t.Fatal("old restarts must age out of the window")
	}
	if r.Attempt() != 2 {
		t.Fatalf("Attempt after aging out = %d, want 2 (minutes 3 and 12)", r.Attempt())
	}
}

func TestSupervisorExitedIsNilWhenStopped(t *testing.T) {
	var s supervisor
	if s.Exited() != nil {
		t.Fatal("Exited must be a nil channel when no server runs, so it never fires in a select")
	}
	s.Stop() // must be a harmless no-op
}

func TestMissingFilesAndPaths(t *testing.T) {
	dir := t.TempDir()
	conf := defaultConfig()
	p, err := resolvePaths(dir, conf)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.missing(); len(got) != 3 {
		t.Fatalf("empty folder: missing = %v, want model, template and server", got)
	}
	for _, f := range []string{p.model, p.template, p.server} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.missing(); len(got) != 0 {
		t.Fatalf("all present: missing = %v", got)
	}
	conf.ModelFile = filepath.Join("..", "outside.gguf")
	if _, err := resolvePaths(dir, conf); err == nil {
		t.Fatal("a path outside the app folder must be rejected")
	}
}
