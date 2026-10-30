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
	"strings"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/compose"
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
	mem, err := newMemory(ctx)
	if err != nil {
		log.Printf("不启用历史面试检索：%v", err) // 比如 Redis 没启动，面试照常进行
	}
	var memories retriever.Retriever
	if mem != nil {
		memories = mem.retriever
	}
	interview, err := newInterviewGraph(ctx, evaluator, chat, memories)
	if err != nil {
		log.Fatal(err)
	}
	docParser, err := newDocParser(ctx)
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
		if !idPattern.MatchString(req.User) || !idPattern.MatchString(req.Session) {
			req.User = "" // 没有合法的用户 / 场次 ID 就不检索历史
		}
		t := &turn{Req: req}
		var opts []compose.Option
		if req.User != "" {
			opts = append(opts, compose.WithRetrieverOption(searchFilter(req.User, req.Session)))
		}
		sr, err := interview.Stream(r.Context(), t, opts...)
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

	mux.HandleFunc("POST /api/parse", handleParse(docParser))
	mux.HandleFunc("POST /api/memory", handleMemory(mem))
	mux.HandleFunc("DELETE /api/memory", handleMemory(mem))

	log.Printf("listening on %s, model %s", addr, cfg.Model)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// request 是前端每次发来的完整状态：后端不存任何东西。
type request struct {
	Setup    setup             `json:"setup"`
	Messages []*schema.Message `json:"messages"`
	Minutes  int               `json:"minutes"` // 面试已进行的分钟数
	State    *interviewState   `json:"state"`   // 上一轮返回的阶段，开场时为空
	Profile  profile           `json:"profile"`
	User     string            `json:"user"`    // 浏览器生成的用户 ID，用来检索这位用户的历史面试
	Session  string            `json:"session"` // 本场面试 ID，检索时排除本场
}

// profile 是候选人提供的事实材料，全文放进提示词。
type profile struct {
	Resume string `json:"resume"` // 简历
	JD     string `json:"jd"`     // 目标职位描述
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
	req.Profile.Resume = clip(strings.TrimSpace(req.Profile.Resume), maxProfileRunes)
	req.Profile.JD = clip(strings.TrimSpace(req.Profile.JD), maxProfileRunes)
	return req, true
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
