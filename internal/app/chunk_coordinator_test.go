package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestChunkBudgetClamping(t *testing.T) {
	cases := []struct {
		maxChars int
		want     int
	}{
		{4000, 1200},
		{2000, 1200},
		{1200, 1200},
		{800, 800},
		{500, 500},
		{0, 1200},
		{-10, 1200},
	}
	for _, tc := range cases {
		got := chunkBudget(tc.maxChars)
		if got != tc.want {
			t.Errorf("chunkBudget(%d) = %d, want %d", tc.maxChars, got, tc.want)
		}
	}
}

func TestPlanChunks_SingleChunkUnderBudget(t *testing.T) {
	text := "This is a single paragraph well under budget."
	tasks, structure := planChunks(text, 1200)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].text != text || tasks[0].paraIndex != 0 || tasks[0].subIndex != 0 {
		t.Errorf("task mismatch: %+v", tasks[0])
	}
	if len(structure) != 1 || structure[0].subCount != 1 {
		t.Errorf("structure mismatch: %+v", structure)
	}
}

func TestPlanChunks_MultiParagraphs(t *testing.T) {
	p1 := strings.Repeat("A", 700)
	p2 := strings.Repeat("B", 700)
	input := p1 + "\n\n" + p2

	tasks, structure := planChunks(input, 1200)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].text != p1 || tasks[0].paraIndex != 0 || tasks[0].subIndex != 0 {
		t.Errorf("task 0 mismatch: %+v", tasks[0])
	}
	if tasks[1].text != p2 || tasks[1].paraIndex != 1 || tasks[1].subIndex != 0 {
		t.Errorf("task 1 mismatch: %+v", tasks[1])
	}
	if len(structure) != 2 || structure[0].subCount != 1 || structure[1].subCount != 1 {
		t.Errorf("structure mismatch: %+v", structure)
	}
}

func TestPlanChunks_OversizedParagraph(t *testing.T) {
	s1 := strings.Repeat("A", 800) + "."
	s2 := strings.Repeat("B", 800) + "."
	para := s1 + " " + s2

	tasks, structure := planChunks(para, 1200)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks for oversized paragraph, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].paraIndex != 0 || tasks[0].subIndex != 0 || tasks[0].text != s1 {
		t.Errorf("task 0 mismatch: %+v", tasks[0])
	}
	if tasks[1].paraIndex != 0 || tasks[1].subIndex != 1 || tasks[1].text != s2 {
		t.Errorf("task 1 mismatch: %+v", tasks[1])
	}
	if len(structure) != 1 || structure[0].subCount != 2 {
		t.Errorf("structure mismatch: %+v", structure)
	}
}

func TestChunkCoordinator_SequentialExecutionAndProgress(t *testing.T) {
	var prompts []string
	var maxConcurrency int32
	var currentConcurrency int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&currentConcurrency, 1)
		defer atomic.AddInt32(&currentConcurrency, -1)
		for {
			old := atomic.LoadInt32(&maxConcurrency)
			if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrency, old, cur) {
				break
			}
		}

		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		if msgs, ok := m["messages"].([]any); ok && len(msgs) > 0 {
			if msgMap, ok := msgs[0].(map[string]any); ok {
				if c, ok := msgMap["content"].(string); ok {
					prompts = append(prompts, c)
				}
			}
		}
		time.Sleep(20 * time.Millisecond) // simulate brief processing
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "訳文"}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputReplace})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	p1 := strings.Repeat("A", 700)
	p2 := strings.Repeat("B", 700)
	p3 := strings.Repeat("C", 700)
	input := p1 + "\n\n" + p2 + "\n\n" + p3

	a.cb = &testClipboard{text: input}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return input, true, nil }

	a.startTranslation(0)

	// Pump progress messages and result
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

	// Verify concurrency was strictly 1 (sequential)
	if atomic.LoadInt32(&maxConcurrency) != 1 {
		t.Errorf("max concurrency was %d, expected 1 (strictly sequential)", maxConcurrency)
	}

	// Verify all 3 chunks were received in sequence
	if len(prompts) != 3 {
		t.Fatalf("expected 3 prompt calls, got %d: %v", len(prompts), prompts)
	}
	if prompts[0] != p1 || prompts[1] != p2 || prompts[2] != p3 {
		t.Errorf("prompts out of order: %+v", prompts)
	}

	// Verify tray status reported chunk progress
	fakeT := a.tray.(*fakeTray)
	var chunk1Seen, chunk2Seen, chunk3Seen bool
	for _, s := range fakeT.statuses {
		if strings.Contains(s, "translating chunk 1 of 3") {
			chunk1Seen = true
		}
		if strings.Contains(s, "translating chunk 2 of 3") {
			chunk2Seen = true
		}
		if strings.Contains(s, "translating chunk 3 of 3") {
			chunk3Seen = true
		}
	}
	if !chunk1Seen || !chunk2Seen || !chunk3Seen {
		t.Errorf("chunk progress status missing in statuses: %v", fakeT.statuses)
	}

	// Verify clipboard has joined translation
	wantReplaced := "訳文\n\n訳文\n\n訳文"
	if a.cb.(*testClipboard).text != wantReplaced {
		t.Errorf("clipboard mismatch:\nGOT:  %q\nWANT: %q", a.cb.(*testClipboard).text, wantReplaced)
	}
}

func TestChunkCoordinator_AllOrNothingRollbackOnError(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)
		if count == 2 {
			// Chunk 2 fails with error
			http.Error(w, "internal model error", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "訳文"}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputReplace})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	p1 := strings.Repeat("A", 700)
	p2 := strings.Repeat("B", 700)
	p3 := strings.Repeat("C", 700)
	input := p1 + "\n\n" + p2 + "\n\n" + p3

	originalClipboard := input
	a.cb = &testClipboard{text: originalClipboard}
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

	// Verify chunk 3 was never requested (aborted after chunk 2 error)
	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("callCount was %d, expected 2 (aborted immediately on chunk 2 failure)", callCount)
	}

	// Verify system clipboard remained untouched (All-or-Nothing Guarantee)
	if a.cb.(*testClipboard).text != originalClipboard {
		t.Errorf("clipboard was modified on error! got %q, want %q", a.cb.(*testClipboard).text, originalClipboard)
	}

	// Verify history remained empty (no partial/corrupted entries)
	if len(a.hist.Items()) != 0 {
		t.Errorf("history recorded partial result on error: %+v", a.hist.Items())
	}

	// Verify user notification clearly indicates chunk 2 of 3 failure
	fakeT := a.tray.(*fakeTray)
	if fakeT.lastTitle() != "Translation failed" {
		t.Errorf("notify title: got %q, want 'Translation failed'", fakeT.lastTitle())
	}
	if !strings.Contains(fakeT.lastNote(), "chunk 2 of 3") {
		t.Errorf("notify msg missing chunk context: got %q", fakeT.lastNote())
	}
}

func TestChunkCoordinator_BilingualInterleavedRecombination(t *testing.T) {
	callIdx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callIdx++
		content := fmt.Sprintf("訳文%d", callIdx)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputBilingual})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	p1 := strings.Repeat("A", 700)
	p2 := strings.Repeat("B", 700)
	input := p1 + "\n\n" + p2

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

	// Expected bilingual interleaved output (§3.2):
	// [Original P1]
	// [Translated P1]
	//
	// [Original P2]
	// [Translated P2]
	wantBilingual := p1 + "\n訳文1\n\n" + p2 + "\n訳文2"
	if a.cb.(*testClipboard).text != wantBilingual {
		t.Errorf("bilingual output mismatch:\nGOT:  %q\nWANT: %q", a.cb.(*testClipboard).text, wantBilingual)
	}

	// Verify in-memory history retains untouched raw input
	items := a.hist.Items()
	if len(items) != 1 || items[0].Source != input {
		t.Errorf("history source corrupted: got %+v, want %q", items, input)
	}
}

func TestChunkCoordinator_CancellationOnNewHotkey(t *testing.T) {
	chunk1Started := make(chan struct{})
	allowChunk1ToFinish := make(chan struct{})
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)
		if count == 1 {
			close(chunk1Started)
			<-allowChunk1ToFinish
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "訳文"}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputReplace})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	p1 := strings.Repeat("A", 700)
	p2 := strings.Repeat("B", 700)
	input := p1 + "\n\n" + p2

	a.cb = &testClipboard{text: input}
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return input, true, nil }

	a.startTranslation(0)

	// Wait until chunk 1 starts
	<-chunk1Started

	// Now user presses hotkey again before chunk 1 finishes
	a.startTranslation(0)
	close(allowChunk1ToFinish)

	// Pump results
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

	// The first translation was aborted, so chunk 2 from generation 1 was never requested
	// Only generation 2 finished
	if a.translating {
		t.Fatal("translation did not complete")
	}
}

func TestChunkCoordinator_OversizedParagraphReassembled(t *testing.T) {
	callIdx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callIdx++
		content := fmt.Sprintf("訳文%d。", callIdx)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputReplace})
	a.endpoint = srv.URL
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }

	s1 := strings.Repeat("A", 800) + "."
	s2 := strings.Repeat("B", 800) + "."
	input := s1 + " " + s2

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

	// Verify that sentences from the same paragraph are joined as a single paragraph, not with \n\n
	want := "訳文1。訳文2。"
	if a.cb.(*testClipboard).text != want {
		t.Errorf("oversized paragraph reassembly mismatch:\nGOT:  %q\nWANT: %q", a.cb.(*testClipboard).text, want)
	}
}
