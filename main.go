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
}

type translationDirection struct{ Source, Target string }

// Japanese script takes precedence in mixed Japanese/English text.
// Only the ja/en pair is supported. ASCII identifiers and URLs are not translated.
func detectDirection(s string) (translationDirection, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(strings.ToLower(s), "https://") || strings.HasPrefix(strings.ToLower(s), "http://") {
		return translationDirection{}, false
	}
	hasJapanese, hasLatin := false, false
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) || r >= 0xFF66 && r <= 0xFF9D {
			hasJapanese = true
		}
		if unicode.In(r, unicode.Latin) {
			hasLatin = true
		}
	}
	if hasJapanese {
		return translationDirection{Source: "ja", Target: "en"}, true
	}
	if hasLatin {
		return translationDirection{Source: "en", Target: "ja"}, true
	}
	return translationDirection{}, false
}

func shouldTranslate(s string, maxChars int) bool {
	if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > maxChars {
		return false
	}
	_, ok := detectDirection(s)
	return ok
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

func translate(ctx context.Context, endpoint, source string, direction translationDirection, maxTokens int) (string, error) {
	payload := map[string]any{
		"model":                "plamo-3-translate",
		"messages":             []map[string]string{{"role": "user", "content": source}},
		"temperature":          0,
		"max_tokens":           maxTokens,
		"stop":                 []string{"<|plamo:tag|>", "<|plamo:bos|>"},
		"chat_template_kwargs": map[string]string{"source_lang": direction.Source, "target_lang": direction.Target},
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

	conf, ok := ensureInstalled(dir, conf)
	if !ok {
		return
	}
	paths, err := resolvePaths(dir, conf)
	if err != nil {
		fatal(err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", conf.Port), 250*time.Millisecond)
	if err == nil {
		conn.Close()
		fatal("server port is already occupied: ", conf.Port)
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
func applyTranslation(cb Clipboard, original, translated string) (bool, error) {
	if translated == original {
		return false, nil
	}
	return cb.ReplaceIfUnchanged(original, translated)
}
