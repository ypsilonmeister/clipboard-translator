package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

const maxTelephoneRounds = 10

// translateTelephone executes round trips between the two languages up to 10
// times, stopping early at a fixed point (when a round trip produces the exact
// same text as two steps earlier). The timeout applies per round trip, not to
// the whole job.
func (a *app) translateTelephone(ctx context.Context, g uint64, key string, promptText string, dir languages.Direction, maxTokens, budget int, timeout time.Duration) (string, error) {
	dir1 := dir
	if dir1.Source == "" {
		dir1.Source = "ja"
	}
	if dir1.Target == "" {
		dir1.Target = "en"
	}
	dir2 := languages.Direction{Source: dir1.Target, Target: dir1.Source}

	steps := make([]string, 1, 2*maxTelephoneRounds+1)
	steps[0] = promptText
	current := promptText
	stabilizedRound := 0

	for round := 1; round <= maxTelephoneRounds; round++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		a.reportProgress(translationProgress{generation: g, telephoneRound: round, telephoneTotal: maxTelephoneRounds})

		roundCtx := ctx
		var roundCancel context.CancelFunc
		if timeout > 0 {
			roundCtx, roundCancel = context.WithTimeout(ctx, timeout)
		}

		t1, err := a.translatePrompt(roundCtx, g, key, current, dir1, maxTokens, budget, timeout)
		if err != nil {
			if roundCancel != nil {
				roundCancel()
			}
			if errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errRequestTimeout) {
				err = fmt.Errorf("%w: %w", errRequestTimeout, err)
			}
			return "", err
		}
		steps = append(steps, t1)
		current = t1

		t2, err := a.translatePrompt(roundCtx, g, key, current, dir2, maxTokens, budget, timeout)
		if roundCancel != nil {
			roundCancel()
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errRequestTimeout) {
				err = fmt.Errorf("%w: %w", errRequestTimeout, err)
			}
			return "", err
		}
		steps = append(steps, t2)
		current = t2

		if current == steps[2*round-2] {
			stabilizedRound = round
			break
		}
	}

	lines := make([]string, len(steps))
	for i, s := range steps {
		if stabilizedRound > 0 && i == len(steps)-1 {
			lines[i] = fmt.Sprintf("[%d] %s   ← stable after round %d (no further change)", i, s, stabilizedRound)
		} else {
			lines[i] = fmt.Sprintf("[%d] %s", i, s)
		}
	}
	return strings.Join(lines, "\n"), nil
}
