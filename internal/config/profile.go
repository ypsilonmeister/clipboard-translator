package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

type SkipReason int

const (
	SkipNone SkipReason = iota
	SkipEmpty
	SkipTooLong
	SkipURL
	SkipNoTranslatableText
)

func (r SkipReason) String() string {
	switch r {
	case SkipNone:
		return "none"
	case SkipEmpty:
		return "empty"
	case SkipTooLong:
		return "too long"
	case SkipURL:
		return "URL"
	case SkipNoTranslatableText:
		return "no translatable text"
	default:
		return "unknown"
	}
}

// check classifies why s cannot be translated by p, or returns skipNone and
// the languages.Direction.
func (p Profile) Check(s string, maxChars int) (SkipReason, languages.Direction) {
	if strings.TrimSpace(s) == "" {
		return SkipEmpty, languages.Direction{}
	}
	if utf8.RuneCountInString(s) > maxChars {
		return SkipTooLong, languages.Direction{}
	}
	if languages.IsURL(strings.TrimSpace(s)) {
		return SkipURL, languages.Direction{}
	}
	if p.Target != "" {
		if strings.IndexFunc(s, unicode.IsLetter) < 0 {
			return SkipNoTranslatableText, languages.Direction{}
		}
		return SkipNone, languages.Direction{Target: p.Target}
	}
	dir, ok := languages.DirectionFor(p.Languages, s)
	if !ok {
		return SkipNoTranslatableText, languages.Direction{}
	}
	return SkipNone, dir
}

// SkipReason returns why s cannot be translated by p, or skipNone if it can.
func (p Profile) SkipReason(s string, maxChars int) SkipReason {
	reason, _ := p.Check(s, maxChars)
	return reason
}

// direction is a profile's direction for s, or false for empty, too long,
// URL-only or undetectable text (see languages.DirectionFor for pairs).
func (p Profile) Direction(s string, maxChars int) (languages.Direction, bool) {
	reason, dir := p.Check(s, maxChars)
	return dir, reason == SkipNone
}

// SkipMessage returns the user-facing message explaining why translation was skipped.
func SkipMessage(reason SkipReason, count, limit int, label string) string {
	switch reason {
	case SkipEmpty:
		return "Nothing to translate: no text was selected or copied."
	case SkipTooLong:
		return fmt.Sprintf("Nothing was translated: the selection is %d characters, over the %d-character limit. Select less text or raise the limit in Settings (Longest text).", count, limit)
	case SkipURL:
		return "Nothing was translated: the selection is just a URL."
	case SkipNoTranslatableText:
		if label != "" {
			return fmt.Sprintf("Nothing was translated: this hotkey translates %s.", label)
		}
		return "Nothing was translated: no translatable text found."
	default:
		return ""
	}
}

// SkipStatus returns the tooltip status text for a skipped request.
func SkipStatus(reason SkipReason, label string) string {
	switch reason {
	case SkipEmpty:
		return "Clipboard Translator - nothing to translate (empty selection)"
	case SkipTooLong:
		return "Clipboard Translator - text is too long"
	case SkipURL:
		return "Clipboard Translator - nothing to translate (URL only)"
	case SkipNoTranslatableText:
		if strings.Contains(label, "↔") {
			return "Clipboard Translator - nothing to translate (not " + label + ")"
		}
		return "Clipboard Translator - nothing to translate (unsupported text)"
	default:
		return ""
	}
}

// label says what the profile does, for the tooltip and notifications.
func (p Profile) Label() string {
	if p.Target != "" {
		return "to " + languages.Name(p.Target)
	}
	return languages.PairLabel(p.Languages)
}

// PathWithin resolves a config.json path to a file below base. The result is
// never base itself, and drive-relative ("C:foo") or rooted ("\foo", "/foo")
// paths are refused because Windows would resolve them against the current
// drive or directory instead of base.
func PathWithin(base, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is empty; use a path relative to the app folder")
	}
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return "", errors.New("paths must be relative to app folder")
	}
	full := filepath.Clean(filepath.Join(base, filepath.FromSlash(p)))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path outside app folder")
	}
	if rel == "." {
		return "", errors.New("path must name a file inside the app folder, not the folder itself")
	}
	return full, nil
}
