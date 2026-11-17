package languages

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Recorded heuristics verdicts from the 2026-11-17 direction bake-off. Keep
// every previously correct case, including guards not in TestDirectionForPairs.
func TestDirectionForCorpus(t *testing.T) {
	f, err := os.Open("testdata/direction_corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type score struct{ before, after, total int }
	var overall, hard, cross, filenames score
	scanner := bufio.NewScanner(f)
	rows := 0
	for scanner.Scan() {
		rows++
		var tc struct {
			Input    string
			Pair     []string
			Expected string `json:"expected_source"`
			Baseline string `json:"baseline_source"`
			Hard     bool
		}
		if err := json.Unmarshal(scanner.Bytes(), &tc); err != nil {
			t.Fatalf("row %d: %v", rows, err)
		}
		got, ok := DirectionFor(tc.Pair, tc.Input)
		before := tc.Baseline == tc.Expected
		after := ok && got.Source == tc.Expected
		if before && !after {
			t.Errorf("row %d regressed: %q: got %+v, %v; want source %s", rows, tc.Input, got, ok, tc.Expected)
		}
		scores := []*score{&cross}
		if len(tc.Pair) == 2 && tc.Pair[0] == "en" && tc.Pair[1] == "ru" {
			scores = []*score{&overall}
			if tc.Hard {
				scores = append(scores, &hard)
			}
		}
		// The five hyphen/underscore cases from Probe 1 of the bake-off.
		switch tc.Input[strings.LastIndex(tc.Input, " ")+1:] {
		case "docker-compose.yml", "test_utils.py", "my-script.py", "run-tests.sh", "foo-bar.com":
			scores = append(scores, &filenames)
		}
		for _, s := range scores {
			s.total++
			if before {
				s.before++
			}
			if after {
				s.after++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows != 101 || overall.total != 97 || hard.total != 24 || cross.total != 4 ||
		overall.before != 88 || hard.before != 18 || cross.before != 4 || filenames.total != 5 || filenames.before != 0 {
		t.Fatalf("corpus/baseline changed: rows=%d, overall=%+v, hard=%+v, cross=%+v, filenames=%+v", rows, overall, hard, cross, filenames)
	}
	for name, s := range map[string]score{"en/ru": overall, "hard": hard, "cross-pair": cross, "filenames": filenames} {
		t.Logf("%s: before %d/%d, after %d/%d", name, s.before, s.total, s.after, s.total)
		if s.after < s.before {
			t.Errorf("%s fell below recorded baseline", name)
		}
	}
	if filenames.after != filenames.total {
		t.Errorf("hyphen/underscore filenames: want all %d cases correct", filenames.total)
	}
}
