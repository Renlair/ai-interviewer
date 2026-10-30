package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
)

//go:embed web
var webFS embed.FS

type config struct {
	BaseURL string
	APIKey  string
	Model   string
}

func main() {
	cfg := config{
		BaseURL: env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		Model:   env("OPENAI_MODEL", "gpt-4o-mini"),
	}
	if cfg.APIKey == "" {
		log.Fatal("请设置 OPENAI_API_KEY")
	}
	addr := env("ADDR", ":8080")

	web, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, cfg, interviewMessages)
	})
	mux.HandleFunc("POST /api/review", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, cfg, reviewMessages)
	})

	log.Printf("listening on %s, model %s", addr, cfg.Model)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// request 是前端每次发来的完整状态：后端不存任何东西。
type request struct {
	Setup    setup     `json:"setup"`
	Messages []message `json:"messages"`
	Minutes  int       `json:"minutes"` // 面试已进行的分钟数
}

type setup struct {
	Industry  string `json:"industry"`
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func handle(w http.ResponseWriter, r *http.Request, cfg config, build func(request) []message) {
	var req request
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	msgs := build(req)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// 客户端断开（用户打断）时 r.Context() 取消，上游请求随之中止。
	if err := stream(r.Context(), cfg, msgs, w); err != nil && r.Context().Err() == nil {
		log.Printf("llm: %v", err)
		w.Write([]byte("\n\n[模型调用失败：" + err.Error() + "]"))
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
