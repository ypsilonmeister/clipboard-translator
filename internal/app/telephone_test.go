package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestTelephoneMode_StopsAtFixedPoint(t *testing.T) {
	responses := []string{
		"I'm so busy I'd even borrow a cat's paws.",
		"猫の手でも借りたいほど忙しい。",
		"I'm so busy I'd even borrow a cat's paw.",
		"猫の手でも借りたいほど忙しい。", // Fixed point: matches step 2!
	}
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt32(&callCount, 1)) - 1
		var content string
		if idx < len(responses) {
			content = responses[idx]
		} else {
			content = fmt.Sprintf("unexpected call %d", idx)
		}
		time.Sleep(10 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{
		Hotkey:    "Ctrl+Alt+E",
		Action:    config.ActionTelephone,
		Languages: []string{"ja", "en"},
		Output:    config.OutputReplace,
	})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	input := "猫の手も借りたい"
	a.cb = &testClipboard{text: input}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return input, true, nil }

	a.startTranslation(0)

	deadline := time.Now().Add(5 * time.Second)
	for a.translating && time.Now().Before(deadline) {
		select {
		case p := <-a.progress:
			a.onProgress(p)
		case res := <-a.results:
			for {
				select {
				case p := <-a.progress:
					a.onProgress(p)
				default:
					goto drained1
				}
			}
		drained1:
			a.handleResult(res)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	if a.translating {
		t.Fatal("telephone mode translation timed out in test loop")
	}

	// Should stop at round 2 after 4 translation calls.
	if gotCalls := atomic.LoadInt32(&callCount); gotCalls != 4 {
		t.Fatalf("expected 4 server calls, got %d", gotCalls)
	}

	wantTranscript := strings.Join([]string{
		"[0] 猫の手も借りたい",
		"[1] I'm so busy I'd even borrow a cat's paws.",
		"[2] 猫の手でも借りたいほど忙しい。",
		"[3] I'm so busy I'd even borrow a cat's paw.",
		"[4] 猫の手でも借りたいほど忙しい。   ← stable after round 2 (no further change)",
	}, "\n")

	gotClipboard := a.cb.(*testClipboard).text
	if gotClipboard != wantTranscript {
		t.Fatalf("clipboard mismatch:\nGOT:\n%s\nWANT:\n%s", gotClipboard, wantTranscript)
	}

	// Verify exactly ONE history entry is created.
	items := a.hist.Items()
	if len(items) != 1 {
		t.Fatalf("expected 1 history item, got %d", len(items))
	}
	if items[0].Source != input {
		t.Errorf("history Source = %q, want %q", items[0].Source, input)
	}
	if items[0].Translated != wantTranscript {
		t.Errorf("history Translated = %q, want %q", items[0].Translated, wantTranscript)
	}

	// Verify tray status reported "round n of 10" progress
	fakeT := a.tray.(*fakeTray)
	var round1Seen, round2Seen bool
	for _, s := range fakeT.statuses {
		if strings.Contains(s, "round 1 of 10") {
			round1Seen = true
		}
		if strings.Contains(s, "round 2 of 10") {
			round2Seen = true
		}
	}
	if !round1Seen || !round2Seen {
		t.Errorf("tray statuses missing round progress: %v", fakeT.statuses)
	}
}

func TestTelephoneMode_FixedPointAtRound1(t *testing.T) {
	responses := []string{
		"Hello",
		"こんにちは", // Fixed point: matches step 0!
	}
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt32(&callCount, 1)) - 1
		content := responses[idx]
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{
		Hotkey:    "Ctrl+Alt+E",
		Action:    config.ActionTelephone,
		Languages: []string{"ja", "en"},
	})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	input := "こんにちは"
	a.cb = &testClipboard{text: input}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return input, true, nil }

	a.startTranslation(0)

	deadline := time.Now().Add(5 * time.Second)
	for a.translating && time.Now().Before(deadline) {
		select {
		case p := <-a.progress:
			a.onProgress(p)
		case res := <-a.results:
			a.handleResult(res)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	if a.translating {
		t.Fatal("translation timed out")
	}

	if gotCalls := atomic.LoadInt32(&callCount); gotCalls != 2 {
		t.Fatalf("expected 2 server calls, got %d", gotCalls)
	}

	wantTranscript := strings.Join([]string{
		"[0] こんにちは",
		"[1] Hello",
		"[2] こんにちは   ← stable after round 1 (no further change)",
	}, "\n")

	if got := a.cb.(*testClipboard).text; got != wantTranscript {
		t.Fatalf("clipboard mismatch:\nGOT:\n%s\nWANT:\n%s", got, wantTranscript)
	}
}

func TestTelephoneMode_StopsAt10RoundTrips(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		content := fmt.Sprintf("translation drift step %d", n)
		time.Sleep(10 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{
		Hotkey:    "Ctrl+Alt+E",
		Action:    config.ActionTelephone,
		Languages: []string{"ja", "en"},
	})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	input := "猫の手も借りたい"
	a.cb = &testClipboard{text: input}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return input, true, nil }

	a.startTranslation(0)

	deadline := time.Now().Add(10 * time.Second)
	for a.translating && time.Now().Before(deadline) {
		select {
		case p := <-a.progress:
			a.onProgress(p)
		case res := <-a.results:
			for {
				select {
				case p := <-a.progress:
					a.onProgress(p)
				default:
					goto drained2
				}
			}
		drained2:
			a.handleResult(res)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	if a.translating {
		t.Fatal("translation timed out")
	}

	// 10 round trips = exactly 20 calls
	if gotCalls := atomic.LoadInt32(&callCount); gotCalls != 20 {
		t.Fatalf("expected 20 server calls, got %d", gotCalls)
	}

	gotClipboard := a.cb.(*testClipboard).text
	lines := strings.Split(gotClipboard, "\n")
	if len(lines) != 21 {
		t.Fatalf("expected 21 transcript lines ([0]..[20]), got %d lines:\n%s", len(lines), gotClipboard)
	}
	if !strings.HasPrefix(lines[0], "[0] 猫の手も借りたい") {
		t.Errorf("line 0 mismatch: %s", lines[0])
	}
	for i := 1; i <= 20; i++ {
		prefix := fmt.Sprintf("[%d] ", i)
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("line %d does not start with %q: %s", i, prefix, lines[i])
		}
	}

	// Must NOT contain the stabilization note because it never stabilized
	if strings.Contains(gotClipboard, "← stable after") {
		t.Errorf("transcript should not contain stabilization note when not stabilized:\n%s", gotClipboard)
	}

	// Verify ONE history item
	if items := a.hist.Items(); len(items) != 1 {
		t.Fatalf("expected 1 history item, got %d", len(items))
	}

	// Verify tray progress reported rounds
	fakeT := a.tray.(*fakeTray)
	for r := 1; r <= 10; r++ {
		wantRound := fmt.Sprintf("round %d of 10", r)
		seen := false
		for _, s := range fakeT.statuses {
			if strings.Contains(s, wantRound) {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("missing tray status for %q", wantRound)
		}
	}
}

func TestTelephoneMode_CancelMidwayLeavesClipboardUntouched(t *testing.T) {
	gate := make(chan struct{})
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 2 {
			// Pause inside leg 2 of round 1
			<-gate
		}
		content := fmt.Sprintf("step %d", n)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{
		Hotkey:    "Ctrl+Alt+E",
		Action:    config.ActionTelephone,
		Languages: []string{"ja", "en"},
	})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	initialText := "猫の手も借りたい"
	a.cb = &testClipboard{text: initialText}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return initialText, true, nil }

	a.startTranslation(0)

	// Wait until server starts processing call 2
	for atomic.LoadInt32(&callCount) < 2 {
		time.Sleep(5 * time.Millisecond)
	}

	// Abort translation midway
	a.abortTranslation()

	// Release the server gate
	close(gate)

	// Pump any pending events
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case p := <-a.progress:
			a.onProgress(p)
		case res := <-a.results:
			a.handleResult(res)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Clipboard MUST remain untouched
	if got := a.cb.(*testClipboard).text; got != initialText {
		t.Fatalf("clipboard modified on cancelled run: got %q, want %q", got, initialText)
	}
	if a.cb.(*testClipboard).writes != 0 {
		t.Fatalf("clipboard was written %d times on cancelled run", a.cb.(*testClipboard).writes)
	}

	// History MUST remain empty
	if items := a.hist.Items(); len(items) != 0 {
		t.Fatalf("expected empty history on cancelled run, got %d items", len(items))
	}
}

func TestTelephoneMode_PerRoundTimeout(t *testing.T) {
	t.Run("per_round_timeout_allows_job_longer_than_single_round_timeout", func(t *testing.T) {
		// 3 rounds = 6 requests.
		// Each request takes 25ms.
		// Round duration: 2 * 25ms = 50ms.
		// Total job time: 6 * 25ms = 150ms.
		// Timeout is set to 80ms per round.
		// Since 50ms < 80ms, no round times out, even though the total job time (150ms) exceeds 80ms!
		var mu sync.Mutex
		callCount := 0

		responses := []string{
			"Step 1",
			"Step 2",
			"Step 3",
			"Step 4",
			"Step 5",
			"Step 4", // Fixed point: matches step 4 (round 2)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(25 * time.Millisecond)
			mu.Lock()
			idx := callCount
			callCount++
			mu.Unlock()
			content := "default"
			if idx < len(responses) {
				content = responses[idx]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
		}))
		defer srv.Close()

		a := profilesApp(t, config.Profile{
			Hotkey:    "Ctrl+Alt+E",
			Action:    config.ActionTelephone,
			Languages: []string{"ja", "en"},
		})
		a.endpoint = srv.URL
		// 80ms timeout per round trip:
		// TranslationTimeoutSec is in seconds, but we can test via translateTelephone directly or set TranslationTimeoutSec
		// In app.conf.TranslationTimeoutSec it's seconds, but let's test translateTelephone with 100ms
		ctx := context.Background()
		transcript, err := a.translateTelephone(ctx, 1, "", "猫の手も借りたい", languages.Direction{Source: "ja", Target: "en"}, 2048, 1200, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("translateTelephone failed: %v", err)
		}
		if !strings.Contains(transcript, "← stable after round 3") {
			t.Fatalf("expected stabilization at round 3, got transcript:\n%s", transcript)
		}
	})

	t.Run("exceeding_per_round_timeout_aborts_and_leaves_clipboard_untouched", func(t *testing.T) {
		// Request delays 120ms when timeout is 50ms.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(120 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "delayed"}}}})
		}))
		defer srv.Close()

		a := profilesApp(t, config.Profile{
			Hotkey:    "Ctrl+Alt+E",
			Action:    config.ActionTelephone,
			Languages: []string{"ja", "en"},
		})
		a.endpoint = srv.URL
		a.foreground = func() uintptr { return 1 }
		a.paste = func(uintptr) error { return nil }

		initialText := "猫の手も借りたい"
		a.cb = &testClipboard{text: initialText}
		a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return initialText, true, nil }

		ctx := context.Background()
		_, err := a.translateTelephone(ctx, 1, "", initialText, languages.Direction{Source: "ja", Target: "en"}, 2048, 1200, 50*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "translation_timeout_seconds") && !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("unexpected error: %v", err)
		}

		// Clipboard untouched
		if got := a.cb.(*testClipboard).text; got != initialText {
			t.Fatalf("clipboard modified on timeout: got %q, want %q", got, initialText)
		}
		if items := a.hist.Items(); len(items) != 0 {
			t.Fatalf("expected empty history on timeout, got %d items", len(items))
		}
	})
}
