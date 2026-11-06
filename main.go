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
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

type result struct {
	original, translated string
	generation           uint64
	err                  error
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
func pathWithin(base, p string) (string, error) {
	if filepath.IsAbs(p) {
		return "", errors.New("paths must be relative to app folder")
	}
	full := filepath.Clean(filepath.Join(base, filepath.FromSlash(p)))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path outside app folder")
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
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("empty choices")
	}
	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if out == "" {
		return "", errors.New("empty translation")
	}
	return out, nil
}

func main() {
	flag.Parse()
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "This prototype supports Windows only.")
		os.Exit(1)
	}
	dir, err := appDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logf, err := os.OpenFile(filepath.Join(dir, "translator.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer logf.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logf))
	log.SetFlags(log.Ldate | log.Ltime)
	conf, err := loadConfig(dir)
	if err != nil {
		log.Fatal("config: ", err)
	}

	// Prevent concurrent application instances.
	instance, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(conf.Port+1))
	if err != nil {
		log.Fatal("another instance or conflicting port is running: ", err)
	}
	defer instance.Close()
	model, err := pathWithin(dir, conf.ModelFile)
	if err != nil {
		log.Fatal(err)
	}
	template, err := pathWithin(dir, conf.TemplateFile)
	if err != nil {
		log.Fatal(err)
	}
	server, err := pathWithin(dir, conf.ServerFile)
	if err != nil {
		log.Fatal(err)
	}
	fallback, err := pathWithin(dir, conf.FallbackServerFile)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range []string{model, template, server} {
		if _, err := os.Stat(p); err != nil {
			log.Fatal("required file missing: ", p, " : ", err)
		}
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", conf.Port)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", conf.Port), 250*time.Millisecond)
	if err == nil {
		conn.Close()
		log.Fatal("server port is already occupied: ", conf.Port)
	}

	logOut, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer logOut.Close()
	var activeCmd *exec.Cmd
	var activeDone <-chan struct{}
	defer func() {
		if activeCmd != nil {
			_ = activeCmd.Process.Kill()
			<-activeDone // Only one goroutine calls Wait on each child.
		}
	}()
	start := func(binary string, gpuLayers int) error {
		args := []string{"-m", model, "-c", fmt.Sprint(conf.ContextTokens),
			"--host", "127.0.0.1", "--port", fmt.Sprint(conf.Port), "--alias", "plamo-3-translate",
			"--chat-template-file", template, "--no-prefill-assistant", "-ngl", fmt.Sprint(gpuLayers)}
		cmd := exec.Command(binary, args...)
		cmd.Dir = filepath.Dir(binary)
		cmd.Stdout, cmd.Stderr = logOut, logOut
		log.Printf("starting %s (gpu_layers=%d)", binary, gpuLayers)
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		startupCtx, cancel := context.WithTimeout(context.Background(), time.Duration(conf.StartupTimeoutSec)*time.Second)
		err := waitReady(startupCtx, endpoint, done)
		cancel()
		if err != nil {
			_ = cmd.Process.Kill()
			<-done
			return err
		}
		activeCmd, activeDone = cmd, done
		return nil
	}
	if err := start(server, conf.GPULayers); err != nil {
		log.Printf("preferred backend failed: %v; see server.log", err)
		if server == fallback {
			return
		}
		if _, statErr := os.Stat(fallback); statErr != nil {
			log.Printf("CPU fallback unavailable: %v", statErr)
			return
		}
		log.Print("falling back to CPU")
		if err := start(fallback, 0); err != nil {
			log.Printf("CPU fallback failed: %v; see server.log", err)
			return
		}
	}

	// Windows RegisterHotKey is thread-local; the same OS thread must poll its messages.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cb, err := newClipboard()
	if err != nil {
		log.Printf("clipboard initialization failed: %v", err)
		return
	}
	defer cb.Close()
	hotkey, err := newHotkey(conf.Hotkey)
	if err != nil {
		log.Printf("hotkey: %v", err)
		return
	}
	defer hotkey.Close()
	log.Printf("ready: select text and press %s (or translate existing clipboard)", conf.Hotkey)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	results := make(chan result, 16)
	ticker := time.NewTicker(time.Duration(conf.PollMS) * time.Millisecond)
	defer ticker.Stop()
	stopFile := filepath.Join(dir, "STOP")
	defer os.Remove(stopFile)
	var generation uint64
	var cancelCurrent context.CancelFunc
	for {
		select {
		case <-sig:
			log.Print("stopping")
			if cancelCurrent != nil {
				cancelCurrent()
			}
			return
		case <-ticker.C:
			if _, err := os.Stat(stopFile); err == nil {
				log.Print("stop requested")
				if cancelCurrent != nil {
					cancelCurrent()
				}
				return
			}
			pressed, err := hotkey.Pressed()
			if err != nil {
				log.Printf("hotkey error: %v", err)
				continue
			}
			if !pressed {
				continue
			}
			// A new request cancels older ones and invalidates their results.
			generation++
			if cancelCurrent != nil {
				cancelCurrent()
				cancelCurrent = nil
			}
			source, _, err := captureSelectedText(cb)
			if err != nil {
				log.Printf("cannot capture selection: %v", err)
				continue
			}
			if !shouldTranslate(source, conf.MaxChars) {
				log.Print("skipped: empty, unsupported, URL, or too long")
				continue
			}
			direction, _ := detectDirection(source)
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(conf.TranslationTimeoutSec)*time.Second)
			cancelCurrent = cancel
			g := generation
			log.Printf("translating %s -> %s", direction.Source, direction.Target)
			go func(src string, g uint64) {
				translated, err := translate(ctx, endpoint, src, direction, conf.MaxTokens)
				select {
				case results <- result{original: src, translated: translated, generation: g, err: err}:
				default:
				}
			}(source, g)
		case res := <-results:
			if res.generation != generation {
				continue
			}
			if res.err != nil {
				if !errors.Is(res.err, context.Canceled) {
					log.Printf("translation failed: %v", res.err)
				}
				continue
			}
			if res.translated == res.original {
				log.Print("translation unchanged; clipboard left intact")
				continue
			}
			// A sequence number is not a reliable test for a text change:
			// apps can update clipboard formats without changing the text.
			// Compare text AND replace under a single OpenClipboard lock.
			applied, err := applyTranslation(cb, res.original, res.translated)
			if err != nil {
				log.Printf("clipboard check/write failed: %v", err)
				continue
			}
			if !applied {
				log.Print("clipboard text changed during translation; result discarded (new clipboard preserved)")
				continue
			}
			log.Print("translation ready in clipboard (Ctrl+V to paste)")
		}
	}
}

// applyTranslation never replaces unrelated clipboard contents.
func applyTranslation(cb Clipboard, original, translated string) (bool, error) {
	if translated == original {
		return false, nil
	}
	return cb.ReplaceIfUnchanged(original, translated)
}
