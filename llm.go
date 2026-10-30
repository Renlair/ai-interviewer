package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

// newChatModel 创建 OpenAI 兼容接口的模型；jsonOut 要求模型只输出 JSON 对象。
func newChatModel(ctx context.Context, cfg config, temperature float32, jsonOut bool) (*openai.ChatModel, error) {
	c := &openai.ChatModelConfig{
		BaseURL:     cfg.BaseURL,
		APIKey:      cfg.APIKey,
		Model:       cfg.Model,
		Temperature: &temperature,
	}
	if jsonOut {
		c.ResponseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	}
	return openai.NewChatModel(ctx, c)
}

// pipe 把模型的流式输出原样写给前端。onFirst 在写出第一段内容前调用，可以用来设置响应头。
func pipe(w http.ResponseWriter, r *http.Request, sr *schema.StreamReader[*schema.Message], err error, onFirst func()) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	started := false
	start := func() {
		if !started && onFirst != nil {
			onFirst()
		}
		started = true
	}

	if err == nil {
		defer sr.Close()
		for {
			var m *schema.Message
			m, err = sr.Recv()
			if errors.Is(err, io.EOF) {
				err = nil
				break
			}
			if err != nil {
				break
			}
			if m.Content == "" {
				continue
			}
			start()
			if _, werr := io.WriteString(w, m.Content); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	// 客户端断开（用户打断）时 r.Context() 取消，上游请求随之中止，不算错误。
	if err != nil && r.Context().Err() == nil {
		log.Printf("llm: %v", err)
		start()
		io.WriteString(w, "\n\n[模型调用失败："+err.Error()+"]")
	}
}
