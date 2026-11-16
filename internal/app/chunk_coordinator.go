package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

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

// chunkTask is one llama-server request. A task with paras set is a group of
// consecutive small paragraphs (joined by a blank line in text) that starts at
// paraIndex; otherwise it is the whole of, or sub-chunk subIndex of, paragraph
// paraIndex.
type chunkTask struct {
	paraIndex int
	subIndex  int
	text      string
	paras     []string
}

type paraStructure struct {
	subCount   int
	lineBreaks bool
	separators []string
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
// When promptText <= budget, it returns a single task. Otherwise consecutive
// paragraphs that fit the budget together become one task (design §3 step 1), so
// the model sees neighbouring paragraphs and a text of many short paragraphs
// does not cost one request each; an oversized paragraph is split on its own.
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

	// group holds the paragraphs [groupStart, groupStart+len(group)) waiting to
	// be sent together; groupLen is their length including the separators.
	var group []string
	groupStart, groupLen := 0, 0
	flush := func() {
		switch len(group) {
		case 0:
		case 1:
			tasks = append(tasks, chunkTask{paraIndex: groupStart, text: group[0]})
		default:
			tasks = append(tasks, chunkTask{paraIndex: groupStart, text: strings.Join(group, "\n\n"), paras: group})
		}
		group, groupLen = nil, 0
	}

	for pIdx, para := range paras {
		paraRunes := len([]rune(para))
		if paraRunes <= budget {
			structure[pIdx] = paraStructure{subCount: 1}
			needed := paraRunes
			if len(group) > 0 {
				needed += 2 // the blank line between paragraphs
			}
			if groupLen+needed > budget {
				flush()
				needed = paraRunes
			}
			if len(group) == 0 {
				groupStart = pIdx
			}
			group = append(group, para)
			groupLen += needed
			continue
		}
		flush()
		// Preserve explicit line structure; cleanup has already removed only
		// the line breaks the user opted to treat as spurious.
		lineBreaks := strings.Contains(para, "\n")
		var subChunks []string
		if lineBreaks {
			subChunks = textstruct.ChunkLines(para, budget)
		} else {
			subChunks = textstruct.ChunkText(para, budget)
		}
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
		var separators []string
		if lineBreaks {
			cursor := 0
			for i, sub := range subChunks {
				cursor += len(sub)
				if i+1 < len(subChunks) {
					sep := ""
					if cursor < len(para) && para[cursor] == '\n' {
						sep = "\n"
						cursor++
					}
					separators = append(separators, sep)
				}
			}
		}
		structure[pIdx] = paraStructure{subCount: len(subChunks), lineBreaks: lineBreaks, separators: separators}
	}
	flush()

	return tasks, structure
}

// recombineTranslatedParagraphs reassembles translated subchunks into paragraphs and
// joins paragraphs with double newlines.
func recombineTranslatedParagraphs(results [][]string, structure []paraStructure) string {
	paras := make([]string, len(results))
	for i, subs := range results {
		if structure[i].lineBreaks {
			var out strings.Builder
			for j, sub := range subs {
				if j > 0 {
					out.WriteString(structure[i].separators[j-1])
				}
				out.WriteString(sub)
			}
			paras[i] = out.String()
		} else if len(subs) == 1 {
			paras[i] = strings.TrimSpace(subs[0])
		} else {
			paras[i] = textstruct.JoinSentences(subs)
		}
	}
	return strings.Join(paras, "\n\n")
}

// translatePrompt executes sequential chunk translations through llama-server,
// reporting progress to the UI thread, and applying all-or-nothing rollback on error.
// timeout applies to each llama-server request, not to the job: a long text on
// a slow engine is many requests that each answer in time (0 = no limit). ctx
// still cancels the whole job.
func (a *app) translatePrompt(ctx context.Context, g uint64, key string, promptText string, direction languages.Direction, maxTokens, budget int, timeout time.Duration) (string, error) {
	run := &chunkRun{endpoint: a.endpoint, key: key, direction: direction, maxTokens: maxTokens, timeout: timeout}
	return run.translateText(ctx, promptText, budget, func(index, total int) {
		a.reportProgress(translationProgress{generation: g, chunkIndex: index, totalChunks: total})
	})
}

// chunkRun is what every request of one translation job shares. It is used
// only by the job's goroutine.
type chunkRun struct {
	endpoint  string
	key       string
	direction languages.Direction
	maxTokens int
	timeout   time.Duration
}

// errRequestTimeout marks a request that ran into translation_timeout_seconds,
// as opposed to a deadline of the caller's context.
var errRequestTimeout = errors.New("llama-server did not answer within translation_timeout_seconds")

// translateText plans text into chunks, translates them in order and puts the
// translation back together. progress (may be nil) is told which chunk starts.
func (r *chunkRun) translateText(ctx context.Context, text string, budget int, progress func(index, total int)) (string, error) {
	tasks, structure := planChunks(text, budget)
	if len(tasks) == 0 {
		return "", nil
	}
	if len(tasks) == 1 && len(tasks[0].paras) == 0 {
		out, err := r.translateTask(ctx, tasks[0])
		if err != nil {
			return "", err
		}
		return out[0], nil
	}

	results := make([][]string, len(structure))
	for i := range structure {
		results[i] = make([]string, structure[i].subCount)
	}

	for taskIdx, task := range tasks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if progress != nil {
			progress(taskIdx+1, len(tasks))
		}
		translated, err := r.translateTask(ctx, task)
		if err != nil {
			if len(tasks) == 1 {
				return "", err
			}
			return "", &chunkError{
				chunkIndex:  taskIdx + 1,
				totalChunks: len(tasks),
				err:         err,
			}
		}
		if len(task.paras) > 0 {
			for i, t := range translated {
				results[task.paraIndex+i][0] = t
			}
		} else {
			results[task.paraIndex][task.subIndex] = translated[0]
		}
	}

	return recombineTranslatedParagraphs(results, structure), nil
}

// translate sends one request, with the per-request timeout.
func (r *chunkRun) translate(ctx context.Context, text string) (string, error) {
	reqCtx := ctx
	if r.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	out, err := llamaserver.Translate(reqCtx, r.endpoint, r.key, text, r.direction, r.maxTokens)
	if err != nil && ctx.Err() == nil && errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%w: %v", errRequestTimeout, err)
	}
	return out, err
}

// translateTask translates one task and returns one translation per paragraph
// of the task (a single element unless the task is a paragraph group).
func (r *chunkRun) translateTask(ctx context.Context, task chunkTask) ([]string, error) {
	translated, err := r.translate(ctx, task.text)
	if err != nil {
		return nil, err
	}
	if len(task.paras) == 0 {
		return []string{translated}, nil
	}
	// A group is only usable when the model kept the paragraphs apart: the
	// recombination and the bilingual output pair them one to one.
	if parts := textstruct.SplitParagraphs(translated); len(parts) == len(task.paras) {
		return parts, nil
	}
	// The model merged or split paragraphs. Translate this group again one
	// paragraph at a time, which is what every paragraph cost before grouping.
	log.Printf("the translation of a %d-paragraph chunk did not keep its paragraphs; translating them one by one", len(task.paras))
	out := make([]string, len(task.paras))
	for i, para := range task.paras {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sub, err := r.translateTask(ctx, chunkTask{text: para})
		if err != nil {
			return nil, err
		}
		out[i] = sub[0]
	}
	return out, nil
}

// translationFailureMessage is the tray notification text for a failed
// translation.
func translationFailureMessage(err error) string {
	where := ""
	var ce *chunkError
	if errors.As(err, &ce) {
		where = fmt.Sprintf(" at chunk %d of %d", ce.chunkIndex, ce.totalChunks)
	}
	switch {
	case errors.Is(err, llamaserver.ErrTruncated):
		if where == "" {
			return "The translation was cut off at the token limit, so no translation was put on the clipboard. Select less text, or raise max_tokens / context_tokens in config.json."
		}
		return "Translation failed" + where + ": the translation was cut off at the token limit. Select less text, or raise max_tokens / context_tokens in config.json."
	case errors.Is(err, errRequestTimeout):
		return "Translation timed out" + where + ", so no translation was put on the clipboard. Raise translation_timeout_seconds in config.json (the time allowed for one chunk), or select less text."
	case where != "":
		return "Translation failed" + where + ". See translator.log for details."
	}
	return "See translator.log for details."
}

func (a *app) reportProgress(p translationProgress) {
	select {
	case a.progress <- p:
	default:
	}
}
