package textstruct

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Exact, deduplicated repros and constructions from PR #219 review comments
// 6935769162, 6936344636 and 6937460196, plus probes of unreviewed 2df246b.
// The fixture also pins intentionally conservative width/table ambiguities.
func TestReview219Batch(t *testing.T) {
	data, err := os.ReadFile("testdata/review219.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Kind, Input, Lang string
		Want, Pieces          []string
		Budget                int
		Boundaries            []Boundary
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 96 {
		t.Fatalf("incomplete replay matrix: %d rows", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			var got []string
			switch tc.Kind {
			case "sentences":
				got = splitSentences(tc.Input)
			case "cleanup":
				got = []string{CleanSpuriousNewlines(tc.Input, tc.Lang)}
			case "chunk", "lines":
				var boundaries []Boundary
				if tc.Kind == "lines" {
					got, boundaries = ChunkLinesWithBoundaries(tc.Input, tc.Budget)
				} else {
					got, boundaries = ChunkParagraphWithBoundaries(tc.Input, tc.Budget)
				}
				if !reflect.DeepEqual(boundaries, tc.Boundaries) {
					t.Errorf("boundaries = %v, want %v", boundaries, tc.Boundaries)
				}
				if tc.Kind == "lines" {
					if untrimmed := JoinChunks(got, boundaries); untrimmed != tc.Input {
						t.Errorf("untrimmed roundtrip = %q, want %q", untrimmed, tc.Input)
					}
					got = []string{JoinChunks(trimAllChunks(got), boundaries)}
				}
			case "join":
				got = []string{JoinChunks(tc.Pieces, tc.Boundaries)}
			default:
				t.Fatalf("unknown replay operation %q", tc.Kind)
			}
			t.Logf("actual = %q", got)
			if !reflect.DeepEqual(got, tc.Want) {
				t.Errorf("input %q: got %q, want %q", tc.Input, got, tc.Want)
			}
		})
	}
}

func TestDeclarationSuffixesRemainCode(t *testing.T) {
	for _, line := range []string{
		"final TranslationResult[] results;",
		"volatile TranslationResult[] results;",
		"TranslationResult[] results /* cached */;",
		"List<string> names[10];",
		"List<string> names /* cached */;",
		"final List<string> names;",
	} {
		if !isCodeLine(line) {
			t.Errorf("declaration lost code preservation: %q", line)
		}
	}
}
