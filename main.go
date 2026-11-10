package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

type result struct {
	original, translated string
	generation           uint64
	err                  error
	direction            languages.Direction
	window               uintptr // foreground window when the hotkey was pressed
	fromSelection        bool    // the text was a fresh selection, not the old clipboard
	output               string
	capturedAt           time.Time
}

type skipReason int

const (
	skipNone skipReason = iota
	skipEmpty
	skipTooLong
	skipURL
	skipNoTranslatableText
)

func (r skipReason) String() string {
	switch r {
	case skipNone:
		return "none"
	case skipEmpty:
		return "empty"
	case skipTooLong:
		return "too long"
	case skipURL:
		return "URL"
	case skipNoTranslatableText:
		return "no translatable text"
	default:
		return "unknown"
	}
}

// check classifies why s cannot be translated by p, or returns skipNone and
// the languages.Direction.
func (p Profile) check(s string, maxChars int) (skipReason, languages.Direction) {
	if strings.TrimSpace(s) == "" {
		return skipEmpty, languages.Direction{}
	}
	if utf8.RuneCountInString(s) > maxChars {
		return skipTooLong, languages.Direction{}
	}
	if languages.IsURL(strings.TrimSpace(s)) {
		return skipURL, languages.Direction{}
	}
	if p.Target != "" {
		if strings.IndexFunc(s, unicode.IsLetter) < 0 {
			return skipNoTranslatableText, languages.Direction{}
		}
		return skipNone, languages.Direction{Target: p.Target}
	}
	dir, ok := languages.DirectionFor(p.Languages, s)
	if !ok {
		return skipNoTranslatableText, languages.Direction{}
	}
	return skipNone, dir
}

// skipReason returns why s cannot be translated by p, or skipNone if it can.
func (p Profile) skipReason(s string, maxChars int) skipReason {
	reason, _ := p.check(s, maxChars)
	return reason
}

// direction is a profile's direction for s, or false for empty, too long,
// URL-only or undetectable text (see languages.DirectionFor for pairs).
func (p Profile) direction(s string, maxChars int) (languages.Direction, bool) {
	reason, dir := p.check(s, maxChars)
	return dir, reason == skipNone
}

// skipMessage returns the user-facing message explaining why translation was skipped.
func skipMessage(reason skipReason, count, limit int, label string) string {
	switch reason {
	case skipEmpty:
		return "Nothing to translate: no text was selected or copied."
	case skipTooLong:
		return fmt.Sprintf("Nothing was translated: the selection is %d characters, over the %d-character limit. Select less text or raise the limit in Settings (Longest text).", count, limit)
	case skipURL:
		return "Nothing was translated: the selection is just a URL."
	case skipNoTranslatableText:
		if label != "" {
			return fmt.Sprintf("Nothing was translated: this hotkey translates %s.", label)
		}
		return "Nothing was translated: no translatable text found."
	default:
		return ""
	}
}

// skipStatus returns the tooltip status text for a skipped request.
func skipStatus(reason skipReason, label string) string {
	switch reason {
	case skipEmpty:
		return "Clipboard Translator - nothing to translate (empty selection)"
	case skipTooLong:
		return "Clipboard Translator - text is too long"
	case skipURL:
		return "Clipboard Translator - nothing to translate (URL only)"
	case skipNoTranslatableText:
		if strings.Contains(label, "↔") {
			return "Clipboard Translator - nothing to translate (not " + label + ")"
		}
		return "Clipboard Translator - nothing to translate (unsupported text)"
	default:
		return ""
	}
}

// label says what the profile does, for the tooltip and notifications.
func (p Profile) label() string {
	if p.Target != "" {
		return "to " + languages.Name(p.Target)
	}
	return languages.PairLabel(p.Languages)
}

func appDir() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// pathWithin resolves a config.json path to a file below base. The result is
// never base itself, and drive-relative ("C:foo") or rooted ("\foo", "/foo")
// paths are refused because Windows would resolve them against the current
// drive or directory instead of base.
func pathWithin(base, p string) (string, error) {
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

// ignoreWriteErrors lets log output go to stdout when there is one. In the
// GUI build (-H=windowsgui) stdout is invalid and every write fails, which
// would otherwise make io.MultiWriter stop before reaching the log file.
type ignoreWriteErrors struct{ w io.Writer }

func (i ignoreWriteErrors) Write(p []byte) (int, error) {
	_, _ = i.w.Write(p)
	return len(p), nil
}

// fatal logs the message, shows it to the user (message box in the GUI build)
// and exits.
func fatal(v ...any) {
	msg := fmt.Sprint(v...)
	log.Print(msg)
	platform.ShowError(msg)
	os.Exit(1)
}

func main() {
	flag.Parse()
	// Native desktop APIs must be initialized and serviced on this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := platform.PlatformRuntimeError(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Windows RegisterHotKey and the tray window are thread-local; the same OS
	// thread must create them and pump their messages.
	dir, err := appDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir, err = platform.WritableAppDir(dir)
	if err != nil {
		fatal("app data folder: ", err)
	}
	logPath := filepath.Join(dir, "translator.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		platform.ShowError(err.Error())
		os.Exit(1)
	}
	defer logf.Close()
	log.SetOutput(io.MultiWriter(logf, ignoreWriteErrors{os.Stdout}))
	log.SetFlags(log.Ldate | log.Ltime)
	conf, err := loadConfig(dir)
	if err != nil {
		fatal("config: ", err)
	}

	// Prevent concurrent application instances.
	instance, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(conf.Port+1))
	if err != nil {
		fatal("another instance or conflicting port is running: ", err)
	}
	defer instance.Close()
	// We are the only instance, so a STOP file is left over from an earlier run
	// (e.g. Stop.cmd after a crash). Without this the app would quit immediately.
	_ = os.Remove(filepath.Join(dir, "STOP"))
	// Likewise a recorded llama-server still running was left by a run that died
	// (macOS only; Windows and Linux stop it with the app).
	platform.StopLeftoverServer(platform.ChildRecordPath(dir, conf.Port))

	conf, ok := ensureInstalled(dir, conf)
	if !ok {
		return
	}
	paths, err := resolvePaths(dir, conf)
	if err != nil {
		fatal(err)
	}
	if err := platform.CheckServerPort(conf.Port); err != nil {
		fatal(err)
	}

	serverLog, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		fatal(err)
	}
	defer serverLog.Close()

	// The tray exists from here on so the user sees the app while the model loads.
	tray := platform.NewTray(logPath)
	defer tray.Close()
	newApp(dir, conf, paths, tray, serverLog).run()
}

// applyTranslation never replaces unrelated clipboard contents.
func applyTranslation(cb platform.Clipboard, original, replacement string) (bool, error) {
	if replacement == original {
		return false, nil
	}
	return cb.ReplaceIfUnchanged(original, replacement)
}

// formatBilingual joins the source and translation with a newline, trimming any
// trailing CR and LF characters from the source.
func formatBilingual(source, translated string) string {
	return strings.TrimRight(source, "\r\n") + "\n" + translated
}
