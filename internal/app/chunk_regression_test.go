package app

import (
	"context"
	"strings"
	"sync"
	"testing"

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
			a.translatePrompt(ctx, 1, "", prompt, languages.Direction{Target: "ja"}, 2048, budget)
		}
	}()
	close(start)
	for i := 0; i < 100; i++ {
		a.conf.MaxChars = 2000 + i
	}
	wg.Wait()
}
