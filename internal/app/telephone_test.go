package app

import (
	"context"
	"encoding/json"
	"errors"
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
	input := "猫の手も借りたい"
	responses := []string{
		"I'm so busy I'd even borrow a cat's paws.",
		"猫の手でも借りたいほど忙しい。",
		"I'm so busy I'd even borrow a cat's paw.",
		"猫の手でも借りたいほど忙しい。", // Fixed point: matches step 2!
	}
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type chatReq struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			ChatTemplateKwargs struct {
				SourceLang string `json:"source_lang"`
				TargetLang string `json:"target_lang"`
			} `json:"chat_template_kwargs"`
		}
		var req chatReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx := int(atomic.AddInt32(&callCount, 1)) - 1

		// Verify alternating directions and chained prompt text
		wantSourceLang := "ja"
		wantTargetLang := "en"
		wantPrompt := input
		if idx%2 == 1 {
			wantSourceLang = "en"
			wantTargetLang = "ja"
		}
		if idx > 0 {
			wantPrompt = responses[idx-1]
		}
		if req.ChatTemplateKwargs.SourceLang != wantSourceLang || req.ChatTemplateKwargs.TargetLang != wantTargetLang {
			t.Errorf("call %d: got direction %s -> %s, want %s -> %s",
				idx, req.ChatTemplateKwargs.SourceLang, req.ChatTemplateKwargs.TargetLang, wantSourceLang, wantTargetLang)
		}
		if len(req.Messages) == 0 || req.Messages[0].Content != wantPrompt {
			t.Errorf("call %d: got prompt %q, want %q", idx, req.Messages[0].Content, wantPrompt)
		}

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
	input := "こんにちは"
	responses := []string{
		"Hello",
		"こんにちは", // Fixed point: matches step 0!
	}
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type chatReq struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			ChatTemplateKwargs struct {
				SourceLang string `json:"source_lang"`
				TargetLang string `json:"target_lang"`
			} `json:"chat_template_kwargs"`
		}
		var req chatReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx := int(atomic.AddInt32(&callCount, 1)) - 1
		if idx >= len(responses) {
			http.Error(w, fmt.Sprintf("unexpected request %d", idx), http.StatusBadRequest)
			return
		}

		// Verify alternating directions and chained prompt text
		wantSourceLang := "ja"
		wantTargetLang := "en"
		wantPrompt := input
		if idx == 1 {
			wantSourceLang = "en"
			wantTargetLang = "ja"
			wantPrompt = responses[0]
		}
		if req.ChatTemplateKwargs.SourceLang != wantSourceLang || req.ChatTemplateKwargs.TargetLang != wantTargetLang {
			t.Errorf("call %d: got direction %s -> %s, want %s -> %s",
				idx, req.ChatTemplateKwargs.SourceLang, req.ChatTemplateKwargs.TargetLang, wantSourceLang, wantTargetLang)
		}
		if len(req.Messages) == 0 || req.Messages[0].Content != wantPrompt {
			t.Errorf("call %d: got prompt %q, want %q", idx, req.Messages[0].Content, wantPrompt)
		}

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
	input := "猫の手も借りたい"
	var callCount int32
	var prevText string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type chatReq struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			ChatTemplateKwargs struct {
				SourceLang string `json:"source_lang"`
				TargetLang string `json:"target_lang"`
			} `json:"chat_template_kwargs"`
		}
		var req chatReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx := int(atomic.AddInt32(&callCount, 1)) - 1

		// Verify alternating directions and chained prompt text
		wantSourceLang := "ja"
		wantTargetLang := "en"
		if idx%2 == 1 {
			wantSourceLang = "en"
			wantTargetLang = "ja"
		}
		if req.ChatTemplateKwargs.SourceLang != wantSourceLang || req.ChatTemplateKwargs.TargetLang != wantTargetLang {
			t.Errorf("call %d: got direction %s -> %s, want %s -> %s",
				idx, req.ChatTemplateKwargs.SourceLang, req.ChatTemplateKwargs.TargetLang, wantSourceLang, wantTargetLang)
		}

		mu.Lock()
		wantPrompt := input
		if idx > 0 {
			wantPrompt = prevText
		}
		if len(req.Messages) == 0 || req.Messages[0].Content != wantPrompt {
			t.Errorf("call %d: got prompt %q, want %q", idx, req.Messages[0].Content, wantPrompt)
		}

		content := fmt.Sprintf("translation drift step %d", idx+1)
		prevText = content
		mu.Unlock()

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
	var gateClosed sync.Once
	closeGate := func() { gateClosed.Do(func() { close(gate) }) }
	defer closeGate()
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

	// Wait until server starts processing call 2 with a bounded wait
	waitDeadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&callCount) < 2 {
		if time.Now().After(waitDeadline) {
			t.Fatal("timed out waiting for call 2")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Abort translation midway
	a.abortTranslation()

	// Release the server gate
	closeGate()

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
		// Timeout is set to 100ms per round.
		// Since 50ms < 100ms, no round times out, even though the total job time (150ms) exceeds 100ms!
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
		// 100ms timeout per round trip:
		ctx := context.Background()
		transcript, err := a.translateTelephone(ctx, 1, "", "猫の手も借りたい", languages.Direction{Source: "ja", Target: "en"}, 2048, 1200, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("translateTelephone failed: %v", err)
		}
		if !strings.Contains(transcript, "← stable after round 3") {
			t.Fatalf("expected stabilization at round 3, got transcript:\n%s", transcript)
		}
	})

	t.Run("shared_round_budget_kills_second_leg_and_preserves_clipboard", func(t *testing.T) {
		// Two legs each delaying 150ms, with a round budget of 250ms.
		// Leg 1 succeeds at 150ms (< 250ms).
		// Leg 2 has only 100ms remaining in the round budget, so it times out at 250ms.
		var callCount int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&callCount, 1)
			time.Sleep(150 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "response"}}}})
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
		dir := languages.Direction{Source: "ja", Target: "en"}
		translated, err := a.translateTelephone(ctx, 1, "", initialText, dir, 2048, 1200, 250*time.Millisecond)
		if err == nil {
			t.Fatal("expected shared round budget timeout error, got nil")
		}
		if !errors.Is(err, errTelephoneTimeout) && !errors.Is(err, errRequestTimeout) {
			t.Fatalf("expected timeout error wrapping errTelephoneTimeout/errRequestTimeout, got: %v", err)
		}

		// Exercise handleResult via the application result path to verify clipboard safety on timeout
		a.handleResult(result{
			original:   initialText,
			translated: translated,
			generation: a.generation,
			err:        err,
			direction:  dir,
			action:     config.ActionTelephone,
		})

		// Clipboard must remain untouched
		if got := a.cb.(*testClipboard).text; got != initialText {
			t.Fatalf("clipboard modified on timeout: got %q, want %q", got, initialText)
		}
		if a.cb.(*testClipboard).writes != 0 {
			t.Fatalf("clipboard was written %d times on timeout", a.cb.(*testClipboard).writes)
		}
		if items := a.hist.Items(); len(items) != 0 {
			t.Fatalf("expected empty history on timeout, got %d items", len(items))
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
		dir := languages.Direction{Source: "ja", Target: "en"}
		translated, err := a.translateTelephone(ctx, 1, "", initialText, dir, 2048, 1200, 50*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "translation_timeout_seconds") && !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("unexpected error: %v", err)
		}

		// Exercise application result path
		a.handleResult(result{
			original:   initialText,
			translated: translated,
			generation: a.generation,
			err:        err,
			direction:  dir,
			action:     config.ActionTelephone,
		})

		// Clipboard untouched
		if got := a.cb.(*testClipboard).text; got != initialText {
			t.Fatalf("clipboard modified on timeout: got %q, want %q", got, initialText)
		}
		if items := a.hist.Items(); len(items) != 0 {
			t.Fatalf("expected empty history on timeout, got %d items", len(items))
		}
	})
}

func TestTranslationFailureMessage_TelephoneRoundTimeout(t *testing.T) {
	msg := translationFailureMessage(errTelephoneTimeout)
	if !strings.Contains(msg, "one round trip") {
		t.Errorf("expected failure message to mention 'one round trip', got %q", msg)
	}
	if strings.Contains(msg, "one chunk") {
		t.Errorf("expected failure message NOT to mention 'one chunk' for telephone, got %q", msg)
	}

	chunkMsg := translationFailureMessage(errRequestTimeout)
	if !strings.Contains(chunkMsg, "one chunk") {
		t.Errorf("expected chunk failure message to mention 'one chunk', got %q", chunkMsg)
	}
}
