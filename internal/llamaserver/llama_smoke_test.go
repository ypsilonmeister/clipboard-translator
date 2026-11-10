//go:build llamasmoke

package llamaserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

var reAscii3 = regexp.MustCompile(`[a-zA-Z]{3,}`)

func containsJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen on loopback: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestLlamaSmoke(t *testing.T) {
	smokeServer := os.Getenv("CT_SMOKE_SERVER")
	smokeModel := os.Getenv("CT_SMOKE_MODEL")
	smokeTemplate := os.Getenv("CT_SMOKE_TEMPLATE")

	if smokeServer == "" || smokeModel == "" || smokeTemplate == "" {
		t.Skip("skipping llama smoke test: CT_SMOKE_SERVER, CT_SMOKE_MODEL, or CT_SMOKE_TEMPLATE unset")
	}

	for name, path := range map[string]string{
		"CT_SMOKE_SERVER":   smokeServer,
		"CT_SMOKE_MODEL":    smokeModel,
		"CT_SMOKE_TEMPLATE": smokeTemplate,
	} {
		if info, err := os.Stat(path); err != nil {
			t.Fatalf("%s path %q does not exist: %v", name, path, err)
		} else if info.IsDir() {
			t.Fatalf("%s path %q is a directory, expected a file", name, path)
		}
	}

	port := freeLoopbackPort(t)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	var logBuf bytes.Buffer
	sup := &supervisor{
		model:          smokeModel,
		template:       smokeTemplate,
		contextTokens:  2048,
		port:           port,
		endpoint:       endpoint,
		logOut:         io.MultiWriter(os.Stderr, &logBuf),
		startupTimeout: 180 * time.Second,
		ui:             func(label string, elapsed time.Duration) bool { return false },
	}

	t.Logf("starting llama-server from %s (port %d)", smokeServer, port)
	if err := sup.start(smokeServer, 0, "smoke test"); err != nil {
		t.Fatalf("failed to start llama-server: %v\nServer log:\n%s", err, logBuf.String())
	}
	t.Cleanup(func() {
		if sup.Running() {
			if sup.cmd != nil && sup.cmd.Process != nil {
				_ = sup.cmd.Process.Kill()
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 1. Japanese to English
	jaInput := "明日の会議は10時からに変更になりました。"
	ja2en, err := translate(ctx, sup.endpoint, sup.apiKey, jaInput, languages.Direction{Source: "ja", Target: "en"}, 256)
	if errors.Is(err, errTruncated) {
		t.Fatalf("ja->en translation was truncated")
	} else if err != nil {
		t.Fatalf("ja->en translation failed: %v", err)
	}
	t.Logf("JA -> EN: %q", ja2en)
	if strings.TrimSpace(ja2en) == "" {
		t.Errorf("ja->en translation is empty")
	}
	if !reAscii3.MatchString(ja2en) {
		t.Errorf("ja->en translation must contain at least 3 ASCII letters in a row: %q", ja2en)
	}
	if containsJapanese(ja2en) {
		t.Errorf("ja->en translation must not contain Japanese characters: %q", ja2en)
	}

	// 2. English to Japanese
	enInput := "Could you take a look at the pull request when you get a chance?"
	en2ja, err := translate(ctx, sup.endpoint, sup.apiKey, enInput, languages.Direction{Source: "en", Target: "ja"}, 256)
	if errors.Is(err, errTruncated) {
		t.Fatalf("en->ja translation was truncated")
	} else if err != nil {
		t.Fatalf("en->ja translation failed: %v", err)
	}
	t.Logf("EN -> JA: %q", en2ja)
	if strings.TrimSpace(en2ja) == "" {
		t.Errorf("en->ja translation is empty")
	}
	if !containsJapanese(en2ja) {
		t.Errorf("en->ja translation must contain at least one Japanese character: %q", en2ja)
	}

	// 3. Target-only request (no source_lang)
	targetOnlyInput := "Could you take a look at the pull request when you get a chance?"
	targetOnly, err := translate(ctx, sup.endpoint, sup.apiKey, targetOnlyInput, languages.Direction{Target: "ja"}, 256)
	if errors.Is(err, errTruncated) {
		t.Fatalf("target-only translation was truncated")
	} else if err != nil {
		t.Fatalf("target-only request failed: %v", err)
	}
	t.Logf("Target only (-> JA): %q", targetOnly)
	if strings.TrimSpace(targetOnly) == "" {
		t.Errorf("target-only translation is empty")
	}
	if !containsJapanese(targetOnly) {
		t.Errorf("target-only translation must contain at least one Japanese character: %q", targetOnly)
	}

	// 4. Server rejects request without API key (HTTP 401)
	ctxKey, cancelKey := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelKey()
	_, errNoKey := translate(ctxKey, sup.endpoint, "", "Hello", languages.Direction{Source: "en", Target: "ja"}, 64)
	if errNoKey == nil {
		t.Error("expected HTTP 401 error without API key, got nil")
	} else if !strings.Contains(errNoKey.Error(), "401") {
		t.Errorf("expected HTTP 401 without API key, got: %v", errNoKey)
	} else {
		t.Logf("server correctly rejected request without API key: %v", errNoKey)
	}

	// 5. Stopping supervisor ends process
	stopped := make(chan struct{})
	go func() {
		sup.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Log("supervisor child exited successfully after Stop()")
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor child did not exit within 10s after Stop()")
	}

	if sup.Running() {
		t.Error("supervisor still reports Running() after Stop()")
	}

	// Append translations to GITHUB_STEP_SUMMARY if available
	if summaryPath := os.Getenv("GITHUB_STEP_SUMMARY"); summaryPath != "" {
		if f, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			defer f.Close()
			fmt.Fprintf(f, "- **JA -> EN:** %s\n- **EN -> JA:** %s\n- **Target-only -> JA:** %s\n\n", ja2en, en2ja, targetOnly)
		}
	}
	_ = filepath.Base(smokeServer) // keep filepath imported if needed
}
