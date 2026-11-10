package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/textstruct"
)

// defaultChunkBudget is the target chunk character count (~300-600 tokens),
// providing a massive safety margin below the 4096 context tokens ceiling.
const defaultChunkBudget = 1200

// chunkBudget returns the chunk target character limit clamped to maxChars if lower.
func chunkBudget(maxChars int) int {
	if maxChars > 0 && maxChars < defaultChunkBudget {
		return maxChars
	}
	return defaultChunkBudget
}

type chunkTask struct {
	paraIndex int
	subIndex  int
	text      string
}

type paraStructure struct {
	subCount int
}

type translationProgress struct {
	generation  uint64
	chunkIndex  int
	totalChunks int
}

type chunkError struct {
	chunkIndex  int
	totalChunks int
	err         error
}

func (e *chunkError) Error() string {
	return fmt.Sprintf("chunk %d of %d failed: %v", e.chunkIndex, e.totalChunks, e.err)
}

func (e *chunkError) Unwrap() error {
	return e.err
}

// planChunks splits promptText into sequential tasks and records paragraph structure.
// When promptText <= budget, it returns a single task.
func planChunks(promptText string, budget int) ([]chunkTask, []paraStructure) {
	if len([]rune(promptText)) <= budget {
		return []chunkTask{{paraIndex: 0, subIndex: 0, text: promptText}}, []paraStructure{{subCount: 1}}
	}

	paras := textstruct.SplitParagraphs(promptText)
	if len(paras) == 0 {
		return nil, nil
	}

	var tasks []chunkTask
	structure := make([]paraStructure, len(paras))

	for pIdx, para := range paras {
		paraRunes := len([]rune(para))
		if paraRunes <= budget {
			tasks = append(tasks, chunkTask{
				paraIndex: pIdx,
				subIndex:  0,
				text:      para,
			})
			structure[pIdx] = paraStructure{subCount: 1}
		} else {
			// Oversized paragraph: split into sentences or code lines
			subChunks := textstruct.ChunkText(para, budget)
			if len(subChunks) == 0 {
				subChunks = []string{para}
			}
			for sIdx, sub := range subChunks {
				tasks = append(tasks, chunkTask{
					paraIndex: pIdx,
					subIndex:  sIdx,
					text:      sub,
				})
			}
			structure[pIdx] = paraStructure{subCount: len(subChunks)}
		}
	}

	return tasks, structure
}

// recombineTranslatedParagraphs reassembles translated subchunks into paragraphs and
// joins paragraphs with double newlines.
func recombineTranslatedParagraphs(results [][]string, targetLang string) string {
	paras := make([]string, len(results))
	for i, subs := range results {
		if len(subs) == 1 {
			paras[i] = strings.TrimSpace(subs[0])
		} else {
			paras[i] = textstruct.JoinSentences(subs)
		}
	}
	return strings.Join(paras, "\n\n")
}

// translatePrompt executes sequential chunk translations through llama-server,
// reporting progress to the UI thread, and applying all-or-nothing rollback on error.
func (a *app) translatePrompt(ctx context.Context, g uint64, key string, promptText string, direction languages.Direction, maxTokens int) (string, error) {
	budget := chunkBudget(a.conf.MaxChars)
	tasks, structure := planChunks(promptText, budget)
	if len(tasks) == 0 {
		return "", nil
	}
	if len(tasks) == 1 {
		return llamaserver.Translate(ctx, a.endpoint, key, tasks[0].text, direction, maxTokens)
	}

	results := make([][]string, len(structure))
	for i := range structure {
		results[i] = make([]string, structure[i].subCount)
	}

	for taskIdx, task := range tasks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		a.reportProgress(translationProgress{
			generation:  g,
			chunkIndex:  taskIdx + 1,
			totalChunks: len(tasks),
		})
		translated, err := llamaserver.Translate(ctx, a.endpoint, key, task.text, direction, maxTokens)
		if err != nil {
			return "", &chunkError{
				chunkIndex:  taskIdx + 1,
				totalChunks: len(tasks),
				err:         err,
			}
		}
		results[task.paraIndex][task.subIndex] = translated
	}

	return recombineTranslatedParagraphs(results, direction.Target), nil
}

func (a *app) reportProgress(p translationProgress) {
	select {
	case a.progress <- p:
	default:
	}
}
