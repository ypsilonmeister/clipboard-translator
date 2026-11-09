package main

import (
	"strings"
	"testing"
	"time"
)

func TestSameClipboardText(t *testing.T) {
	cases := []struct {
		name      string
		current   string
		expected  string
		wantMatch bool
	}{
		// Exact matches
		{"identical empty", "", "", true},
		{"identical word", "hello", "hello", true},
		{"identical multiline", "line1\nline2", "line1\nline2", true},

		// Tolerant matches: CRLF / CR / LF normalisation
		{"CRLF vs LF", "line1\r\nline2", "line1\nline2", true},
		{"LF vs CRLF", "line1\nline2", "line1\r\nline2", true},
		{"CR vs LF", "line1\rline2", "line1\nline2", true},
		{"mixed line endings", "a\r\nb\rc\n", "a\nb\nc\n", true},

		// Tolerant matches: Trailing whitespace & line breaks
		{"trailing spaces", "hello   ", "hello", true},
		{"trailing tabs", "hello\t\t", "hello", true},
		{"trailing LF", "hello\n\n", "hello", true},
		{"trailing CRLF", "hello\r\n", "hello", true},
		{"trailing mixed whitespace", "hello \t\r\n  \n", "hello", true},
		{"both have different trailing whitespace", "hello \r\n", "hello\t\n", true},
		{"empty vs trailing whitespace", "", "   \t\r\n", true},

		// Cases that MUST NOT match (protection of newer user copies)
		{"different text", "world", "hello", false},
		{"extra word at end", "hello world", "hello", false},
		{"extra word at start", "say hello", "hello", false},
		{"leading space in current", " hello", "hello", false},
		{"leading space in expected", "hello", " hello", false},
		{"different leading spaces", "  hello", " hello", false},
		{"leading line break", "\nhello", "hello", false},
		{"leading CRLF", "\r\nhello", "hello", false},
		{"inner space difference", "hello  world", "hello world", false},
		{"inner tab vs space", "hello\tworld", "hello world", false},
		{"inner newline vs space", "hello\nworld", "hello world", false},
		{"punctuation difference", "hello!", "hello", false},
		{"case difference", "Hello", "hello", false},
		{"empty vs non-empty", "", "hello", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sameClipboardText(tc.current, tc.expected)
			if got != tc.wantMatch {
				t.Fatalf("sameClipboardText(%q, %q) = %v, want %v", tc.current, tc.expected, got, tc.wantMatch)
			}
		})
	}
}

func TestClassifyClipboardDiff(t *testing.T) {
	cases := []struct {
		name       string
		expected   string
		current    string
		elapsed    time.Duration
		wantExpLen int
		wantCurLen int
		wantDiff   int
		wantWsOnly bool
		wantPrefix bool
	}{
		{
			name:       "identical",
			expected:   "Hello world",
			current:    "Hello world",
			elapsed:    120 * time.Millisecond,
			wantExpLen: 11,
			wantCurLen: 11,
			wantDiff:   -1,
			wantWsOnly: true,
			wantPrefix: true,
		},
		{
			name:       "whitespace only rewrite CRLF to LF",
			expected:   "Hello\r\n",
			current:    "Hello\n",
			elapsed:    50 * time.Millisecond,
			wantExpLen: 7,
			wantCurLen: 6,
			wantDiff:   5, // rune 5: '\r' vs '\n'
			wantWsOnly: true,
			wantPrefix: false,
		},
		{
			name:       "whitespace only trailing spaces trimmed",
			expected:   "Hello  ",
			current:    "Hello",
			elapsed:    75 * time.Millisecond,
			wantExpLen: 7,
			wantCurLen: 5,
			wantDiff:   5, // rune 5: ' ' vs end of string
			wantWsOnly: true,
			wantPrefix: true, // "Hello" is a prefix of "Hello  "
		},
		{
			name:       "expected is prefix of current",
			expected:   "Hello",
			current:    "Hello world",
			elapsed:    200 * time.Millisecond,
			wantExpLen: 5,
			wantCurLen: 11,
			wantDiff:   5,
			wantWsOnly: false,
			wantPrefix: true,
		},
		{
			name:       "current is prefix of expected",
			expected:   "Hello world",
			current:    "Hello",
			elapsed:    300 * time.Millisecond,
			wantExpLen: 11,
			wantCurLen: 5,
			wantDiff:   5,
			wantWsOnly: false,
			wantPrefix: true,
		},
		{
			name:       "different text",
			expected:   "apple",
			current:    "banana",
			elapsed:    10 * time.Millisecond,
			wantExpLen: 5,
			wantCurLen: 6,
			wantDiff:   0,
			wantWsOnly: false,
			wantPrefix: false,
		},
		{
			name:       "difference in middle",
			expected:   "cat",
			current:    "car",
			elapsed:    15 * time.Millisecond,
			wantExpLen: 3,
			wantCurLen: 3,
			wantDiff:   2,
			wantWsOnly: false,
			wantPrefix: false,
		},
		{
			name:       "Japanese multi-byte runes",
			expected:   "こんにちは",
			current:    "こんばんは",
			elapsed:    85 * time.Millisecond,
			wantExpLen: 5,
			wantCurLen: 5,
			wantDiff:   2, // こ(0), ん(1), に(2) vs ば(2)
			wantWsOnly: false,
			wantPrefix: false,
		},
		{
			name:       "Japanese prefix",
			expected:   "日本",
			current:    "日本語",
			elapsed:    90 * time.Millisecond,
			wantExpLen: 2,
			wantCurLen: 3,
			wantDiff:   2,
			wantWsOnly: false,
			wantPrefix: true,
		},
		{
			name:       "empty expected",
			expected:   "",
			current:    "abc",
			elapsed:    0,
			wantExpLen: 0,
			wantCurLen: 3,
			wantDiff:   0,
			wantWsOnly: false,
			wantPrefix: true,
		},
		{
			name:       "both empty",
			expected:   "",
			current:    "",
			elapsed:    0,
			wantExpLen: 0,
			wantCurLen: 0,
			wantDiff:   -1,
			wantWsOnly: true,
			wantPrefix: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diff := classifyClipboardDiff(tc.expected, tc.current, tc.elapsed)
			if diff.ExpectedRunes != tc.wantExpLen {
				t.Errorf("ExpectedRunes = %d, want %d", diff.ExpectedRunes, tc.wantExpLen)
			}
			if diff.CurrentRunes != tc.wantCurLen {
				t.Errorf("CurrentRunes = %d, want %d", diff.CurrentRunes, tc.wantCurLen)
			}
			if diff.FirstDiff != tc.wantDiff {
				t.Errorf("FirstDiff = %d, want %d", diff.FirstDiff, tc.wantDiff)
			}
			if diff.WhitespaceOnly != tc.wantWsOnly {
				t.Errorf("WhitespaceOnly = %v, want %v", diff.WhitespaceOnly, tc.wantWsOnly)
			}
			if diff.OneIsPrefix != tc.wantPrefix {
				t.Errorf("OneIsPrefix = %v, want %v", diff.OneIsPrefix, tc.wantPrefix)
			}
			if diff.Elapsed != tc.elapsed {
				t.Errorf("Elapsed = %v, want %v", diff.Elapsed, tc.elapsed)
			}

			// Verify formatted string representation
			str := diff.String()
			if !strings.Contains(str, "expected_len=") || !strings.Contains(str, "current_len=") ||
				!strings.Contains(str, "first_diff=") || !strings.Contains(str, "ws_only=") ||
				!strings.Contains(str, "prefix=") || !strings.Contains(str, "elapsed=") {
				t.Errorf("String() = %q missing expected diagnostic fields", str)
			}
			// NEVER contain clipboard text in the output string
			if tc.expected != "" && strings.Contains(str, tc.expected) {
				t.Errorf("String() leaked expected text: %q", str)
			}
			if tc.current != "" && strings.Contains(str, tc.current) {
				t.Errorf("String() leaked current text: %q", str)
			}
		})
	}
}
