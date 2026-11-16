package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
)

// sentences returns a paragraph of n distinct sentences of about 50 characters.
func sentences(tag string, n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("Sentence %s%02d %s.", tag, i, strings.Repeat("word ", 7)+"end")
	}
	return strings.Join(out, " ")
}

// truncatingReply answers like a model whose output passes max_tokens for any
// prompt above limit characters; shorter prompts get a word-for-word
// "translation", so the pieces of a split text add up to the whole.
func truncatingReply(limit int) func(string) (string, string) {
	return func(p string) (string, string) {
		if len([]rune(p)) > limit {
			return "cut", "length"
		}
		return strings.ReplaceAll(p, "Sentence", "Satz"), ""
	}
}

// Issue #195 acceptance: with finish_reason "length" above a size, a longer
// text is still translated completely and the bilingual pairing is unchanged.
func TestChunkCoordinator_SplitsAndRetriesOnTruncation(t *testing.T) {
	srv := newChunkServer(t, truncatingReply(400))
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputBilingual})
	a.endpoint = srv.URL
	p1, p2, p3 := sentences("a", 16), sentences("b", 3), sentences("c", 16) // ~900, ~170, ~900 chars
	input := p1 + "\n\n" + p2 + "\n\n" + p3

	got := runChunked(t, a, input)

	tr := func(s string) string { return strings.ReplaceAll(s, "Sentence", "Satz") }
	want := p1 + "\n" + tr(p1) + "\n\n" + p2 + "\n" + tr(p2) + "\n\n" + p3 + "\n" + tr(p3)
	if got != want {
		t.Errorf("bilingual output mismatch:\nGOT:  %q\nWANT: %q", got, want)
	}
	if items := a.hist.Items(); len(items) != 1 || items[0].Source != input {
		t.Errorf("history: %+v", items)
	}
}

// A text below the chunk budget is one request; it is split too when the
// answer is cut off.
func TestChunkCoordinator_SplitsUnchunkedTextOnTruncation(t *testing.T) {
	srv := newChunkServer(t, truncatingReply(200))
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	input := sentences("a", 12) // ~680 chars: 1 request, then halves, then quarters

	got := runChunked(t, a, input)

	if want := strings.ReplaceAll(input, "Sentence", "Satz"); got != want {
		t.Errorf("output mismatch:\nGOT:  %q\nWANT: %q", got, want)
	}
	reqs := srv.requests()
	if reqs[0] != input {
		t.Errorf("the first request must be the whole text, got %q", reqs[0])
	}
}

// A truncated paragraph group is split between its paragraphs, and the
// paragraphs still map one to one.
func TestChunkCoordinator_SplitsParagraphGroupOnTruncation(t *testing.T) {
	srv := newChunkServer(t, func(p string) (string, string) {
		if len([]rune(p)) > 700 {
			return "cut", "length"
		}
		return perParagraph(p), ""
	})
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}, Output: config.OutputBilingual})
	a.endpoint = srv.URL
	paras := shortParagraphs(30, 98)

	got := runChunked(t, a, strings.Join(paras, "\n\n"))

	want := make([]string, len(paras))
	for i, p := range paras {
		want[i] = p + "\nT:" + p
	}
	if got != strings.Join(want, "\n\n") {
		t.Errorf("bilingual output mismatch:\nGOT:  %q\nWANT: %q", got, strings.Join(want, "\n\n"))
	}
	// 3 groups (12, 12 and 6 paragraphs): the first is cut off and translated
	// again in two halves; the second is split before it is sent.
	if n := len(srv.requests()); n != 1+2+2+1 {
		t.Errorf("got %d requests, want 6", n)
	}
}

// A cut-off answer is paid for once per job, not once per chunk: after the
// first one, chunks of that size are split before they are sent.
func TestChunkCoordinator_TruncationIsLearnedWithinTheJob(t *testing.T) {
	const limit = 500
	srv := newChunkServer(t, truncatingReply(limit))
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	paras := []string{sentences("a", 16), sentences("b", 16), sentences("c", 16), sentences("d", 6)} // 3 x ~900, ~340
	input := strings.Join(paras, "\n\n")

	got, err := a.translatePrompt(context.Background(), 1, "", input, languages.Direction{Source: "en", Target: "hi"}, 2048, 1200, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(input, "Sentence", "Satz"); got != want {
		t.Errorf("output mismatch:\nGOT:  %q\nWANT: %q", got, want)
	}
	cutOff := 0
	for _, p := range srv.requests() {
		if len([]rune(p)) > limit {
			cutOff++
		}
	}
	if cutOff != 1 {
		t.Errorf("%d requests were cut off, want 1 (the first chunk only)", cutOff)
	}
	// The short last paragraph is below what was learned and is sent whole.
	if reqs := srv.requests(); reqs[len(reqs)-1] != paras[3] {
		t.Errorf("the last request = %q, want the whole last paragraph", reqs[len(reqs)-1])
	}

	// What one job learned does not carry over to the next one.
	before := len(srv.requests())
	if _, err := a.translatePrompt(context.Background(), 2, "", paras[0], languages.Direction{Source: "en", Target: "hi"}, 2048, 1200, time.Minute); err != nil {
		t.Fatal(err)
	}
	if first := srv.requests()[before]; first != paras[0] {
		t.Errorf("a new job must start with the whole chunk, got %q", first)
	}
}

// Line structure survives a split after truncation.
func TestChunkCoordinator_TruncationSplitKeepsLines(t *testing.T) {
	srv := newChunkServer(t, func(p string) (string, string) {
		if len([]rune(p)) > 300 {
			return "cut", "length"
		}
		return p, ""
	})
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("- list item %02d with some words", i)
	}
	input := strings.Join(lines, "\n") // ~620 chars, one paragraph

	got, err := a.translatePrompt(context.Background(), 1, "", input, languages.Direction{Source: "en", Target: "ja"}, 2048, 1200, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != input {
		t.Errorf("lines changed:\nGOT:  %q\nWANT: %q", got, input)
	}
}

// When splitting does not help, the job fails as before: bounded work, nothing
// on the clipboard, and the token-limit message.
func TestChunkCoordinator_TruncationSplitIsBounded(t *testing.T) {
	srv := newChunkServer(t, func(string) (string, string) { return "cut", "length" })
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	input := sentences("a", 16) + "\n\n" + sentences("b", 16)

	got := runChunked(t, a, input)

	if got != input {
		t.Errorf("clipboard changed on failure: %q", got)
	}
	// Chunk 1 only: the whole chunk, then its first part at each of the 3 depths.
	if n := len(srv.requests()); n != 1+maxTruncationSplits {
		t.Errorf("got %d requests, want %d", n, 1+maxTruncationSplits)
	}
	note := a.tray.(*fakeTray).lastNote()
	if !strings.Contains(note, "chunk 1 of 2") || !strings.Contains(note, "token limit") {
		t.Errorf("notification: %q", note)
	}
	if len(a.hist.Items()) != 0 {
		t.Errorf("history recorded a failed translation: %+v", a.hist.Items())
	}

	_, err := a.translatePrompt(context.Background(), 1, "", "short text", languages.Direction{Source: "en", Target: "ja"}, 2048, 1200, time.Minute)
	var ce *chunkError
	if !errors.Is(err, llamaserver.ErrTruncated) || errors.As(err, &ce) {
		t.Errorf("a text too short to split: err = %v, want the bare truncation error", err)
	}
}

func TestTargetBudget(t *testing.T) {
	for code := range targetChunkBudgets {
		if _, err := languages.Validate([]string{code, "en"}); err != nil {
			t.Errorf("targetChunkBudgets has %q, which is not a model language: %v", code, err)
		}
	}
	cases := []struct {
		budget int
		target string
		want   int
	}{
		{1200, "ja", 1200},
		{1200, "en", 1200},
		{1200, "hi", 1200},
		{1200, "my", 300},
		{1200, "km", 400},
		{1200, "ml", 450},
		{1200, "ta", 700},
		{200, "my", 200}, // max_chars below the table value wins
	}
	for _, tc := range cases {
		if got := targetBudget(tc.budget, tc.target); got != tc.want {
			t.Errorf("targetBudget(%d, %q) = %d, want %d", tc.budget, tc.target, got, tc.want)
		}
	}
}

// Into a token-heavy target the chunks are small from the start, so the usual
// case does not pay for a truncated request first.
func TestChunkCoordinator_SmallerChunksForTokenHeavyTarget(t *testing.T) {
	srv := newChunkServer(t, truncatingReply(300))
	a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"ja", "en"}})
	a.endpoint = srv.URL
	input := sentences("a", 20) // ~1,130 chars: one request for most targets

	got, err := a.translatePrompt(context.Background(), 1, "", input, languages.Direction{Source: "en", Target: "my"}, 2048, chunkBudget(4000), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(input, "Sentence", "Satz"); got != want {
		t.Errorf("output mismatch:\nGOT:  %q\nWANT: %q", got, want)
	}
	reqs := srv.requests()
	if len(reqs) < 4 {
		t.Errorf("got %d requests, want at least 4 chunks of up to 300 characters", len(reqs))
	}
	for i, p := range reqs {
		if n := len([]rune(p)); n > 300 {
			t.Errorf("request %d has %d characters; no request into my should exceed 300", i, n)
		}
	}
}
