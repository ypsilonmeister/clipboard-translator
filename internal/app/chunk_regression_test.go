package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

func TestChunkStructuredBoundaries(t *testing.T) {
	for name, source := range map[string]string{
		"code":        strings.Repeat("    print('hello world')\n", 80),
		"list":        strings.Repeat("- a meaningful list item\n", 80),
		"long line":   strings.Repeat("x", 1300) + "\nnext line",
		"UTF-8 lines": strings.Repeat("    日本語のコメントです\n", 100),
		"CRLF lines":  strings.Repeat("    print('hello world')\r\n", 80),
	} {
		t.Run(name, func(t *testing.T) {
			tasks, structure := planChunks(source, 1200)
			if len(tasks) < 2 {
				t.Fatal("fixture must exceed the budget")
			}
			results := make([][]string, len(structure))
			for i, s := range structure {
				results[i] = make([]string, s.subCount)
			}
			for _, task := range tasks {
				results[task.paraIndex][task.subIndex] = task.text
			}
			got := recombineTranslatedParagraphs(results, structure)
			// The existing prompt splitter trims outer whitespace; chunk boundaries
			// must not remove or invent any additional structural separators.
			if got != strings.TrimSpace(source) {
				t.Fatalf("structured chunk boundaries changed: got %q want %q", got, strings.TrimSpace(source))
			}
		})
	}
}

func TestChunkWorkerUsesCapturedBudget(t *testing.T) {
	a := &app{conf: config.Config{MaxChars: 4000}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prompt := strings.Repeat("A", 700) + "\n\n" + strings.Repeat("B", 700)
	budget := chunkBudget(a.conf.MaxChars)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 100; i++ {
			a.translatePrompt(ctx, 1, "", prompt, languages.Direction{Target: "ja"}, 2048, budget, 0)
		}
	}()
	close(start)
	for i := 0; i < 100; i++ {
		a.conf.MaxChars = 2000 + i
	}
	wg.Wait()
}

func TestAuditLongLinePreservesWordBoundary(t *testing.T) {
	source := strings.Repeat("word ", 240) + "tail\nnext"
	tasks, structure := planChunks(source, 1200)
	results := make([][]string, len(structure))
	for i, s := range structure {
		results[i] = make([]string, s.subCount)
	}
	for _, task := range tasks {
		// Simulate responses preserving the input, then apply the
		// same trimming as llamaserver.Translate.
		results[task.paraIndex][task.subIndex] =
			strings.TrimSpace(task.text)
	}
	got := recombineTranslatedParagraphs(results, structure)
	t.Logf("tasks=%d output suffix=%q", len(tasks), got[len(got)-25:])
	if got != source {
		t.Fatal("whitespace at the intra-line chunk boundary was lost")
	}
}

func TestPlanAndRecombine_BoundaryMetadataCases(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "space on left side of intra-line boundary",
			source: strings.Repeat("word ", 240) + "tail\nnext",
		},
		{
			name:   "space on right side of intra-line boundary",
			source: strings.Repeat("a", 1200) + " tail\nnext",
		},
		{
			name:   "explicit newline boundaries before and after long lines",
			source: "head line\n" + strings.Repeat("word ", 240) + "tail\n" + strings.Repeat("x", 1300) + "\nend line",
		},
		{
			name:   "hard split inside token without whitespace (multi-line)",
			source: strings.Repeat("x", 1300) + "\nnext line",
		},
		{
			name:   "hard split inside token without whitespace (single-line)",
			source: strings.Repeat("x", 1300),
		},
		{
			name:   "CJK long line without whitespace (multi-line)",
			source: strings.Repeat("あ", 1300) + "\n次の行",
		},
		{
			name:   "CJK long line without whitespace (single-line)",
			source: strings.Repeat("あ", 1300),
		},
		{
			name:   "CJK long line with space on left of boundary",
			source: strings.Repeat("語 ", 600) + "尾\n次",
		},
		{
			name:   "CJK long line with space on right of boundary",
			source: strings.Repeat("語", 1200) + " 尾\n次",
		},
		{
			name:   "CJK sentences across chunks",
			source: strings.Repeat("これは日本語のテスト文です。", 90),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tasks, structure := planChunks(tc.source, 1200)
			if len(tasks) < 2 {
				t.Fatalf("expected multiple tasks for %q, got %d", tc.name, len(tasks))
			}
			results := make([][]string, len(structure))
			for i, s := range structure {
				results[i] = make([]string, s.subCount)
			}
			for _, task := range tasks {
				// Model llamaserver.Translate trimming each chunk response.
				results[task.paraIndex][task.subIndex] = strings.TrimSpace(task.text)
			}
			got := recombineTranslatedParagraphs(results, structure)
			if got != tc.source {
				t.Fatalf("recombined mismatch:\nGOT:  %q\nWANT: %q", got, tc.source)
			}
		})
	}
}

func stubBoundaryTransform(s string) string {
	r := strings.NewReplacer(
		"word", "WORD",
		"tail", "TAIL",
		"next", "NEXT",
		"head", "HEAD",
		"line", "LINE",
		"end", "END",
		"x", "X",
		"a", "A",
		"あ", "ア",
		"次の行", "ツギノギョウ",
		"語", "ゴ",
		"尾", "ビ",
		"次", "ジ",
		"テスト", "試験",
	)
	return r.Replace(s)
}

func TestChunkCoordinator_HTTPTrimmingBoundaryCases(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "space on left side of boundary",
			source: strings.Repeat("word ", 240) + "tail\nnext",
		},
		{
			name:   "space on right side of boundary",
			source: strings.Repeat("a", 1200) + " tail\nnext",
		},
		{
			name:   "explicit newline boundaries with long lines",
			source: "head line\n" + strings.Repeat("word ", 240) + "tail\n" + strings.Repeat("x", 1300) + "\nend line",
		},
		{
			name:   "hard split without whitespace (multi-line)",
			source: strings.Repeat("x", 1300) + "\nnext line",
		},
		{
			name:   "hard split without whitespace (single-line)",
			source: strings.Repeat("x", 1300),
		},
		{
			name:   "CJK long line without whitespace (multi-line)",
			source: strings.Repeat("あ", 1300) + "\n次の行",
		},
		{
			name:   "CJK long line without whitespace (single-line)",
			source: strings.Repeat("あ", 1300),
		},
		{
			name:   "CJK long line with space on left of boundary",
			source: strings.Repeat("語 ", 600) + "尾\n次",
		},
		{
			name:   "CJK long line with space on right of boundary",
			source: strings.Repeat("語", 1200) + " 尾\n次",
		},
		{
			name:   "CJK sentences across chunks",
			source: strings.Repeat("これは日本語のテスト文です。", 90),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newChunkServer(t, func(prompt string) (string, string) {
				// Return padded whitespace around the reply so llamaserver.Translate
				// performs real strings.TrimSpace on each HTTP response.
				return " \n  " + stubBoundaryTransform(prompt) + "  \n ", ""
			})
			a := profilesApp(t, config.Profile{Hotkey: "Ctrl+Alt+E", Languages: []string{"en", "ja"}})
			a.endpoint = srv.URL

			got, err := a.translatePrompt(context.Background(), 1, "", tc.source, languages.Direction{Source: "en", Target: "ja"}, 2048, 1200, time.Minute)
			if err != nil {
				t.Fatalf("translatePrompt failed: %v", err)
			}
			if len(srv.requests()) < 2 {
				t.Fatalf("expected >= 2 HTTP requests, got %d", len(srv.requests()))
			}
			want := stubBoundaryTransform(tc.source)
			if got != want {
				t.Fatalf("HTTP recombined mismatch:\nGOT:  %q\nWANT: %q", got, want)
			}
		})
	}
}

func TestChunkCoordinator_HTTPBilingualAndHistoryKeepOriginalText(t *testing.T) {
	srv := newChunkServer(t, func(prompt string) (string, string) {
		return "  " + strings.ToUpper(prompt) + "\n ", ""
	})
	a := profilesApp(t, config.Profile{
		Hotkey:    "Ctrl+Alt+E",
		Languages: []string{"en", "ja"},
		Output:    config.OutputBilingual,
	})
	a.endpoint = srv.URL

	p1 := strings.Repeat("word ", 240) + "tail\nnext"
	p2 := "second para"
	// Include outer whitespace on paragraphs to verify original text stays untouched.
	input := "  " + p1 + "\n\n  " + p2 + "  "

	got := runChunked(t, a, input)

	wantTranslatedP1 := strings.Repeat("WORD ", 240) + "TAIL\nNEXT"
	wantTranslatedP2 := "SECOND PARA"
	wantTranslated := wantTranslatedP1 + "\n\n" + wantTranslatedP2
	wantBilingual := "  " + p1 + "\n" + wantTranslatedP1 + "\n\n  " + p2 + "  \n" + wantTranslatedP2

	if got != wantBilingual {
		t.Fatalf("bilingual output mismatch:\nGOT:  %q\nWANT: %q", got, wantBilingual)
	}
	items := a.hist.Items()
	if len(items) != 1 {
		t.Fatalf("expected 1 history item, got %d: %+v", len(items), items)
	}
	if items[0].Source != input {
		t.Fatalf("history source modified:\nGOT:  %q\nWANT: %q", items[0].Source, input)
	}
	if items[0].Translated != wantTranslated {
		t.Fatalf("history translation mismatch:\nGOT:  %q\nWANT: %q", items[0].Translated, wantTranslated)
	}
}
