package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type result struct {
	original, translated string
	generation           uint64
	err                  error
	direction            translationDirection
	window               uintptr // foreground window when the hotkey was pressed
	fromSelection        bool    // the text was a fresh selection, not the old clipboard
	output               string
}

// translationDirection is what to ask the model for. Source is empty for a
// target-only profile: the model then detects the source language itself.
type translationDirection struct{ Source, Target string }

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
// the translationDirection.
func (p Profile) check(s string, maxChars int) (skipReason, translationDirection) {
	if strings.TrimSpace(s) == "" {
		return skipEmpty, translationDirection{}
	}
	if utf8.RuneCountInString(s) > maxChars {
		return skipTooLong, translationDirection{}
	}
	if isURL(strings.TrimSpace(s)) {
		return skipURL, translationDirection{}
	}
	if p.Target != "" {
		if strings.IndexFunc(s, unicode.IsLetter) < 0 {
			return skipNoTranslatableText, translationDirection{}
		}
		return skipNone, translationDirection{Target: p.Target}
	}
	dir, ok := directionFor(p.Languages, s)
	if !ok {
		return skipNoTranslatableText, translationDirection{}
	}
	return skipNone, dir
}

// skipReason returns why s cannot be translated by p, or skipNone if it can.
func (p Profile) skipReason(s string, maxChars int) skipReason {
	reason, _ := p.check(s, maxChars)
	return reason
}

// direction is a profile's direction for s, or false for empty, too long,
// URL-only or undetectable text (see directionFor in languages.go for pairs).
func (p Profile) direction(s string, maxChars int) (translationDirection, bool) {
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
		return "to " + languageName(p.Target)
	}
	return pairLabel(p.Languages)
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

func waitReady(ctx context.Context, endpoint string, stopped <-chan struct{}) error {
	client := http.Client{Timeout: 800 * time.Millisecond}
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"/health", nil)
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-stopped:
			return errors.New("llama-server exited before becoming ready")
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// errTruncated means the model stopped at a token limit (finish_reason
// "length"): the text is a partial translation and must not be used. That is
// either max_tokens or, when prompt and answer outgrow it, the server's
// context window (context_tokens), which llama-server reports the same way.
var errTruncated = errors.New("translation cut off at the token limit (finish_reason=length)")

// translate asks the server at endpoint for a translation. apiKey is the
// server's per-start key (supervisor.apiKey); it is sent when not empty.
func translate(ctx context.Context, endpoint, apiKey, source string, direction translationDirection, maxTokens int) (string, error) {
	kwargs := map[string]string{"target_lang": direction.Target}
	// The template accepts a missing source_lang (the model detects it) but
	// rejects an empty one, so leave the key out for a target-only profile.
	if direction.Source != "" {
		kwargs["source_lang"] = direction.Source
	}
	payload := map[string]any{
		"model":                "plamo-3-translate",
		"messages":             []map[string]string{{"role": "user", "content": source}},
		"temperature":          0,
		"max_tokens":           maxTokens,
		"stop":                 []string{"<|plamo:tag|>", "<|plamo:bos|>"},
		"chat_template_kwargs": kwargs,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/chat/completions", strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// This is always a loopback-only llama.cpp server owned by this process.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			// Some servers omit it or send null; only "length" is rejected.
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("empty choices")
	}
	if parsed.Choices[0].FinishReason == "length" {
		return "", errTruncated
	}
	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if out == "" {
		return "", errors.New("empty translation")
	}
	return out, nil
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
	showError(msg)
	os.Exit(1)
}

func main() {
	flag.Parse()
	// Native desktop APIs must be initialized and serviced on this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := platformRuntimeError(); err != nil {
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
	dir, err = writableAppDir(dir)
	if err != nil {
		fatal("app data folder: ", err)
	}
	logPath := filepath.Join(dir, "translator.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		showError(err.Error())
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
	stopLeftoverServer(childRecordPath(dir, conf.Port))

	conf, ok := ensureInstalled(dir, conf)
	if !ok {
		return
	}
	paths, err := resolvePaths(dir, conf)
	if err != nil {
		fatal(err)
	}
	if err := checkServerPort(conf.Port); err != nil {
		fatal(err)
	}

	serverLog, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		fatal(err)
	}
	defer serverLog.Close()

	// The tray exists from here on so the user sees the app while the model loads.
	tray := newTray(logPath)
	defer tray.Close()
	newApp(dir, conf, paths, tray, serverLog).run()
}

// applyTranslation never replaces unrelated clipboard contents.
func applyTranslation(cb Clipboard, original, replacement string) (bool, error) {
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
