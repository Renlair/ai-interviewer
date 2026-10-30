package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// stream 调用 OpenAI 兼容的 /chat/completions，把增量文本原样写给 w。
func stream(ctx context.Context, cfg config, msgs []message, w http.ResponseWriter) error {
	body, _ := json.Marshal(map[string]any{
		"model":    cfg.Model,
		"messages": msgs,
		"stream":   true,
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("%s: %s", resp.Status, b)
	}

	flusher, _ := w.(http.Flusher)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		if c := chunk.Choices[0].Delta.Content; c != "" {
			if _, err := io.WriteString(w, c); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	return sc.Err()
}
