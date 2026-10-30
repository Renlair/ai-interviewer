package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/cloudwego/eino/schema"
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

	ctx := context.Background()
	evaluator, err := newChatModel(ctx, cfg, 0, true)
	if err != nil {
		log.Fatal(err)
	}
	chat, err := newChatModel(ctx, cfg, 0.7, false)
	if err != nil {
		log.Fatal(err)
	}
	interview, err := newInterviewGraph(ctx, evaluator, chat)
	if err != nil {
		log.Fatal(err)
	}

	web, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		t := &turn{Req: req}
		sr, err := interview.Stream(r.Context(), t)
		// 本轮阶段放在响应头里交给前端保存；第一段输出到达时 decide 已执行完。
		pipe(w, r, sr, err, func() {
			if t.State.Name == "" {
				return // decide 没执行（评估出错），前端保留原来的阶段
			}
			b, _ := json.Marshal(t.State)
			w.Header().Set("X-Interview-State", url.PathEscape(string(b)))
		})
	})
	mux.HandleFunc("POST /api/review", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		// 候选人一句话都没说时不调用模型。
		if !hasUserMessage(req.Messages) {
			return
		}
		sr, err := chat.Stream(r.Context(), reviewMessages(req))
		pipe(w, r, sr, err, nil)
	})

	log.Printf("listening on %s, model %s", addr, cfg.Model)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// request 是前端每次发来的完整状态：后端不存任何东西。
type request struct {
	Setup    setup             `json:"setup"`
	Messages []*schema.Message `json:"messages"`
	Minutes  int               `json:"minutes"` // 面试已进行的分钟数
	State    *interviewState   `json:"state"`   // 上一轮返回的阶段，开场时为空
}

type setup struct {
	Role   string `json:"role"`   // 面试方向，如 产品经理、技术架构师
	Level  string `json:"level"`  // 候选人阶段，如 校招、3-5 年
	Domain string `json:"domain"` // 业务领域，可为空
}

func decode(w http.ResponseWriter, r *http.Request) (request, bool) {
	var req request
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
