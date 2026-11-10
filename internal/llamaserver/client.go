package llamaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
)

// WaitReady polls the server's /health until it answers 200, the context ends
// or stopped is closed (the process died).
func WaitReady(ctx context.Context, endpoint string, stopped <-chan struct{}) error {
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

// ErrTruncated means the model stopped at a token limit (finish_reason
// "length"): the text is a partial translation and must not be used. That is
// either max_tokens or, when prompt and answer outgrow it, the server's
// context window (context_tokens), which llama-server reports the same way.
var ErrTruncated = errors.New("translation cut off at the token limit (finish_reason=length)")

// Translate asks the server at endpoint for a translation. apiKey is the
// server's per-start key (supervisor.apiKey); it is sent when not empty.
func Translate(ctx context.Context, endpoint, apiKey, source string, direction languages.Direction, maxTokens int) (string, error) {
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
		return "", ErrTruncated
	}
	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if out == "" {
		return "", errors.New("empty translation")
	}
	return out, nil
}
