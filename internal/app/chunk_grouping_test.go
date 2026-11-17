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
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/settings"
)

// chunkServer is a fake llama-server whose answer is computed from the prompt.
type chunkServer struct {
	*httptest.Server
	mu      sync.Mutex
	prompts []string
}

// newChunkServer answers each request with reply(prompt); finish is the
// finish_reason ("" = stop).
func newChunkServer(t *testing.T, reply func(prompt string) (content, finish string)) *chunkServer {
	t.Helper()
	s := &chunkServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		prompt := ""
		if len(m.Messages) > 0 {
			prompt = m.Messages[0].Content
		}
		s.mu.Lock()
		s.prompts = append(s.prompts, prompt)
		s.mu.Unlock()
		content, finish := reply(prompt)
		if finish == "" {
			finish = "stop"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": content}, "finish_reason": finish}}})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *chunkServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prompts...)
}

// perParagraph "translates" by prefixing every paragraph, keeping blank lines.
func perParagraph(prompt string) string {
	paras := strings.Split(prompt, "\n\n")
	for i, p := range paras {
		paras[i] = "T:" + p
	}
	return strings.Join(paras, "\n\n")
}

// shortParagraphs returns n distinct paragraphs of size runes each.
func shortParagraphs(n, size int) []string {
	paras := make([]string, n)
	for i := range paras {
		head := fmt.Sprintf("P%02d ", i)
		paras[i] = head + strings.Repeat("a", size-len(head))
	}
	return paras
}

// runChunked translates input through the hotkey path and returns the clipboard.
func runChunked(t *testing.T, a *app, input string) string {
	t.Helper()
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
			a.handleResult(res)
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	if a.translating {
		t.Fatal("translation did not finish")
	}
	return a.cb.(*testClipboard).text
}

func TestPlanChunks_GroupsSmallParagraphs(t *testing.T) {
	paras := shortParagraphs(30, 98) // 30*98 + 29*2 = 2998 chars
	tasks, structure := planChunks(strings.Join(paras, "\n\n"), 1200)
	if len(tasks) != 3 {
		t.Fatalf("30 short paragraphs: got %d tasks, want 3", len(tasks))
	}
	if len(structure) != 30 {
		t.Fatalf("structure has %d paragraphs, want 30", len(structure))
	}
	next := 0
	for i, task := range tasks {
		if n := len([]rune(task.text)); n > 1200 {
			t.Errorf("task %d has %d chars, over the budget", i, n)
		}
		if task.paraIndex != next {
			t.Errorf("task %d starts at paragraph %d, want %d", i, task.paraIndex, next)
		}
		if task.text != strings.Join(task.paras, "\n\n") {
			t.Errorf("task %d text is not its paragraphs joined by a blank line", i)
		}
		for j, p := range task.paras {
			if p != paras[next+j] {
				t.Errorf("task %d paragraph %d = %q, want %q", i, j, p, paras[next+j])
			}
		}
		next += len(task.paras)
	}
	if next != 30 {
		t.Errorf("tasks cover %d paragraphs, want 30", next)
	}
}

func TestPlanChunks_GroupsAroundOversizedParagraph(t *testing.T) {
	big := strings.Repeat("A", 800) + ". " + strings.Repeat("B", 800) + "."
	input := "one\n\ntwo\n\n" + big + "\n\nthree\n\nfour"
	tasks, structure := planChunks(input, 1200)
	var got []string
	for _, task := range tasks {
		got = append(got, fmt.Sprintf("%d/%d/%d", task.paraIndex, task.subIndex, len(task.paras)))
	}
	want := "0/0/2 2/0/0 2/1/0 3/0/2"
	if strings.Join(got, " ") != want {
		t.Fatalf("tasks (para/sub/group size) = %v, want %s", got, want)
	}
	if len(structure) != 5 || structure[2].subCount != 2 {
		t.Errorf("structure mismatch: %+v", structure)
	}
}

// A group is filled up to the budget exactly, counting the blank lines.
func TestPlanChunks_GroupBudgetBoundary(t *testing.T) {
	for _, tc := range []struct {
		second int // length of the second paragraph
		want   string
	}{
		{598, "0/2 2/0"},     // 600 + 2 + 598 = 1200: fits
		{599, "0/0 1/0 2/0"}, // 1201: one over; the third (700) fits with neither
	} {
		input := strings.Repeat("A", 600) + "\n\n" + strings.Repeat("B", tc.second) + "\n\n" + strings.Repeat("C", 700)
		tasks, _ := planChunks(input, 1200)
		var got []string
		for _, task := range tasks {
			got = append(got, fmt.Sprintf("%d/%d", task.paraIndex, len(task.paras)))
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("second paragraph of %d: tasks (para/group size) = %v, want %s", tc.second, got, tc.want)
		}
	}
}

// What the model does to the blank lines of a group decides between using the
// answer and translating the group again.
func TestTranslateTask_GroupAnswerShapes(t *testing.T) {
	paras := []string{"one", "two", "three"}
	for _, tc := range []struct {
		name         string
		answer       string
		wantRequests int
		want         []string
	}{
		{"kept", "T1\n\nT2\n\nT3", 1, []string{"T1", "T2", "T3"}},
		{"extra blank lines and CRLF", "T1\n\n\n\nT2\r\n\r\nT3\n", 1, []string{"T1", "T2", "T3"}},
		{"blank line with spaces", "T1\n  \nT2\n\nT3", 1, []string{"T1", "T2", "T3"}},
		{"merged into one", "T1 T2 T3", 4, []string{"T:one", "T:two", "T:three"}},
		{"single newlines only", "T1\nT2\nT3", 4, []string{"T:one", "T:two", "T:three"}},
		{"two merged", "T1 T2\n\nT3", 4, []string{"T:one", "T:two", "T:three"}},
		{"one split in two", "T1\n\nT2a\n\nT2b\n\nT3", 4, []string{"T:one", "T:two", "T:three"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newChunkServer(t, func(p string) (string, string) {
				if strings.Contains(p, "\n\n") {
					return tc.answer, ""
				}
				return "T:" + p, ""
			})
			r := &chunkRun{endpoint: srv.URL, direction: languages.Direction{Source: "en", Target: "ja"}, maxTokens: 2048}
			got, err := r.translateTask(context.Background(), chunkTask{text: strings.Join(paras, "\n\n"), paras: paras}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if n := len(srv.requests()); n != tc.wantRequests {
				t.Errorf("got %d requests, want %d", n, tc.wantRequests)
			}
		})
	}
}

// Issue #173 acceptance: about 3 requests for 30 short paragraphs, and the
// bilingual output still pairs paragraph with paragraph.
func TestChunkCoordinator_GroupedParagraphsBilingual(t *testing.T) {
	srv := newChunkServer(t, func(p string) (string, string) { return perParagraph(p), "" })
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputBilingual})
	a.endpoint = srv.URL
	paras := shortParagraphs(30, 98)

	got := runChunked(t, a, strings.Join(paras, "\n\n"))

	if n := len(srv.requests()); n != 3 {
		t.Errorf("got %d requests, want 3", n)
	}
	want := make([]string, len(paras))
	for i, p := range paras {
		want[i] = p + "\nT:" + p
	}
	if got != strings.Join(want, "\n\n") {
		t.Errorf("bilingual output mismatch:\nGOT:  %q\nWANT: %q", got, strings.Join(want, "\n\n"))
	}
}

// When the model does not keep the paragraphs of a group apart, only that
// group is translated again, one paragraph at a time.
func TestChunkCoordinator_GroupFallsBackWhenParagraphsMerge(t *testing.T) {
	paras := shortParagraphs(30, 98)
	srv := newChunkServer(t, func(p string) (string, string) {
		if strings.Contains(p, "\n\n") && strings.HasPrefix(p, paras[12]) {
			return strings.ReplaceAll(perParagraph(p), "\n\n", " "), "" // the second group comes back merged
		}
		return perParagraph(p), ""
	})
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputBilingual})
	a.endpoint = srv.URL

	got := runChunked(t, a, strings.Join(paras, "\n\n"))

	if n := len(srv.requests()); n != 3+12 {
		t.Errorf("got %d requests, want 15 (3 groups, 12 paragraphs of the merged group again)", n)
	}
	want := make([]string, len(paras))
	for i, p := range paras {
		want[i] = p + "\nT:" + p
	}
	if got != strings.Join(want, "\n\n") {
		t.Errorf("bilingual output mismatch:\nGOT:  %q\nWANT: %q", got, strings.Join(want, "\n\n"))
	}
}

// translation_timeout_seconds is a limit for each request: a job whose chunks
// together take longer than the limit still succeeds.
func TestChunkCoordinator_TimeoutIsPerChunk(t *testing.T) {
	const delay, timeout = 150 * time.Millisecond, 400 * time.Millisecond
	srv := newChunkServer(t, func(p string) (string, string) {
		time.Sleep(delay)
		return "訳文", ""
	})
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	input := strings.Join([]string{strings.Repeat("A", 700), strings.Repeat("B", 700), strings.Repeat("C", 700), strings.Repeat("D", 700)}, "\n\n")

	start := time.Now()
	got, err := a.translatePrompt(context.Background(), 1, "", input, languages.Direction{Source: "en", Target: "ja"}, 2048, 1200, timeout)
	if err != nil {
		t.Fatalf("4 chunks of %v with a %v limit per chunk: %v", delay, timeout, err)
	}
	if elapsed := time.Since(start); elapsed <= timeout {
		t.Fatalf("the job took %v, not longer than the limit %v: the test proves nothing", elapsed, timeout)
	}
	if got != "訳文\n\n訳文\n\n訳文\n\n訳文" {
		t.Errorf("got %q", got)
	}
}

func TestChunkCoordinator_ChunkTimeoutMessage(t *testing.T) {
	release := make(chan struct{})
	srv := newChunkServer(t, func(p string) (string, string) {
		if strings.HasPrefix(p, "B") {
			select { // bounded, so a broken timeout fails the test instead of hanging it
			case <-release:
			case <-time.After(5 * time.Second):
			}
		}
		return "訳文", ""
	})
	defer close(release)
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	input := strings.Repeat("A", 700) + "\n\n" + strings.Repeat("B", 700) + "\n\n" + strings.Repeat("C", 700)

	_, err := a.translatePrompt(context.Background(), a.generation, "", input, languages.Direction{Source: "en", Target: "ja"}, 2048, 1200, 100*time.Millisecond)
	if !errors.Is(err, errRequestTimeout) {
		t.Fatalf("err = %v, want the request timeout", err)
	}
	// The translation endpoint maps a timeout to 504 by this error.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded in the chain", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/translate", strings.NewReader(`{"text":"x"}`))
	req.Header.Set(translateTokenHeader, "tok")
	translateAPIHandler(newTranslateAPIAuth("tok", ""), func(settings.Request) settings.Reply { return settings.Reply{Err: err} }).ServeHTTP(rec, req)
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("translation endpoint answered %d for a chunk timeout, want 504", rec.Code)
	}
	if n := len(srv.requests()); n != 2 {
		t.Errorf("got %d requests, want 2 (the job stops at the chunk that timed out)", n)
	}

	a.handleResult(result{generation: a.generation, err: err})
	tray := a.tray.(*fakeTray)
	note := tray.lastNote()
	for _, want := range []string{"timed out", "chunk 2 of 3", "translation_timeout_seconds"} {
		if !strings.Contains(note, want) {
			t.Errorf("notification %q does not mention %q", note, want)
		}
	}
}

// A cancelled job is not reported as a timeout.
func TestChunkCoordinator_CancelIsNotATimeout(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := newChunkServer(t, func(p string) (string, string) {
		started <- struct{}{}
		<-release
		return "訳文", ""
	})
	defer close(release)
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := a.translatePrompt(ctx, 1, "", "hello", languages.Direction{Source: "en", Target: "ja"}, 2048, 1200, time.Minute)
	if !errors.Is(err, context.Canceled) || errors.Is(err, errRequestTimeout) {
		t.Fatalf("err = %v, want context.Canceled and not the request timeout", err)
	}
}
