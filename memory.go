package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	embopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	redisindexer "github.com/cloudwego/eino-ext/components/indexer/redis"
	redisretriever "github.com/cloudwego/eino-ext/components/retriever/redis"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"
)

// 面试记忆：把历史面试记录切块、向量化后存进 Redis，面试时按候选人的回答检索相关片段。

const (
	memIndex  = "ai-interviewer:memory"
	memPrefix = "ai-interviewer:memory:" // 每块的 key：前缀 + 用户:场次:序号
	memTopK   = 3
)

type memory struct {
	rdb       *redis.Client
	indexer   *redisindexer.Indexer
	retriever *redisretriever.Retriever
}

// newMemory 在配置了向量模型时连接 Redis 并确保索引存在；没配置时返回 nil，面试照常进行，只是没有历史记忆。
func newMemory(ctx context.Context) (*memory, error) {
	key := os.Getenv("EMBEDDING_API_KEY")
	if key == "" {
		log.Print("未设置 EMBEDDING_API_KEY，不启用历史面试检索")
		return nil, nil
	}
	dim, err := strconv.Atoi(env("EMBEDDING_DIM", "1024"))
	if err != nil {
		return nil, fmt.Errorf("EMBEDDING_DIM: %w", err)
	}
	emb, err := embopenai.NewEmbedder(ctx, &embopenai.EmbeddingConfig{
		BaseURL:    env("EMBEDDING_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
		APIKey:     key,
		Model:      env("EMBEDDING_MODEL", "text-embedding-v4"),
		Dimensions: &dim,
		Timeout:    15 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	// FT.SEARCH 的结果要用 RESP2 解析，见 redis retriever 的说明。
	rdb := redis.NewClient(&redis.Options{Addr: env("REDIS_ADDR", "localhost:6379"), Protocol: 2, UnstableResp3: true})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("连接 Redis 失败：%w", err)
	}
	err = rdb.FTCreate(ctx, memIndex,
		&redis.FTCreateOptions{OnHash: true, Prefix: []any{memPrefix}},
		&redis.FieldSchema{FieldName: "user", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "session", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "vector_content", FieldType: redis.SearchFieldTypeVector, VectorArgs: &redis.FTVectorArgs{
			HNSWOptions: &redis.FTHNSWOptions{Type: "FLOAT32", Dim: dim, DistanceMetric: "COSINE"},
		}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return nil, fmt.Errorf("创建向量索引失败：%w", err)
	}

	idx, err := redisindexer.NewIndexer(ctx, &redisindexer.IndexerConfig{
		Client:    rdb,
		KeyPrefix: memPrefix,
		BatchSize: 10, // 百炼的向量接口一次最多 10 条
		Embedding: emb,
	})
	if err != nil {
		return nil, err
	}
	ret, err := redisretriever.NewRetriever(ctx, &redisretriever.RetrieverConfig{
		Client:       rdb,
		Index:        memIndex,
		VectorField:  "vector_content",
		ReturnFields: []string{"content", "kind", "role", "date"},
		TopK:         memTopK,
		Embedding:    emb,
		DocumentConverter: func(_ context.Context, d redis.Document) (*schema.Document, error) {
			meta := map[string]any{}
			for k, v := range d.Fields {
				if k != "content" {
					meta[k] = v
				}
			}
			return &schema.Document{ID: d.ID, Content: d.Fields["content"], MetaData: meta}, nil
		},
	})
	if err != nil {
		return nil, err
	}
	log.Printf("历史面试检索已启用：Redis %s", rdb.Options().Addr)
	return &memory{rdb: rdb, indexer: idx, retriever: ret}, nil
}

// savedSession 是前端历史记录里的一场面试。
type savedSession struct {
	ID       string            `json:"id"`
	At       int64             `json:"at"` // 毫秒时间戳
	Setup    setup             `json:"setup"`
	Messages []*schema.Message `json:"messages"`
	Report   string            `json:"report"`
}

// chunks 把一场面试切成检索单元：每个"面试官提问 + 候选人回答"一块，复盘报告按段落合并成几块。
func (s savedSession) chunks(user string) []*schema.Document {
	meta := func(kind string) map[string]any {
		return map[string]any{
			"user": user, "session": s.ID, "kind": kind, "role": s.Setup.Role,
			"date": time.UnixMilli(s.At).Format("2006-01-02"),
		}
	}
	var docs []*schema.Document
	add := func(kind, content string) {
		docs = append(docs, &schema.Document{
			ID:       fmt.Sprintf("%s:%s:%d", user, s.ID, len(docs)),
			Content:  clip(content, 1000),
			MetaData: meta(kind),
		})
	}

	question := ""
	for _, m := range transcript(s.Messages) {
		if m.Role == schema.Assistant {
			question = m.Content
			continue
		}
		add("qa", "面试官："+question+"\n候选人："+m.Content)
	}

	var para strings.Builder
	for _, p := range strings.Split(s.Report, "\n\n") {
		if para.Len() > 0 && len([]rune(para.String()+p)) > 400 {
			add("report", strings.TrimSpace(para.String()))
			para.Reset()
		}
		para.WriteString(strings.TrimSpace(p) + "\n")
	}
	if rest := strings.TrimSpace(para.String()); rest != "" {
		add("report", rest)
	}
	return docs[:min(len(docs), 60)]
}

// save 重建一场面试的索引：先删旧块再写入，所以同一场可以反复保存（比如补上复盘之后）。
func (m *memory) save(ctx context.Context, user string, s savedSession) (int, error) {
	if err := m.remove(ctx, user, s.ID); err != nil {
		return 0, err
	}
	docs := s.chunks(user)
	if len(docs) == 0 {
		return 0, nil
	}
	_, err := m.indexer.Store(ctx, docs)
	return len(docs), err
}

func (m *memory) remove(ctx context.Context, user, session string) error {
	iter := m.rdb.Scan(ctx, 0, memPrefix+user+":"+session+":*", 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil || len(keys) == 0 {
		return err
	}
	return m.rdb.Del(ctx, keys...).Err()
}

// searchFilter 限定只检索这位用户、且不包括正在进行的这一场。
func searchFilter(user, session string) retriever.Option {
	return redisretriever.WithFilterQuery(fmt.Sprintf("@user:{%s} -@session:{%s}", user, session))
}

// softRetriever 检索出错时只记日志，不让面试中断。
type softRetriever struct{ retriever.Retriever }

func (r softRetriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	docs, err := r.Retriever.Retrieve(ctx, query, opts...)
	if err != nil {
		log.Printf("retrieve: %v", err)
		return nil, nil
	}
	return docs, nil
}

// 用户和场次 ID 会拼进 Redis 的 key 和查询语句，只允许小写字母和数字。
var idPattern = regexp.MustCompile(`^[a-z0-9]{1,40}$`)

// handleMemory：POST 写入（或重建）一场面试的索引，DELETE 删除。
func handleMemory(m *memory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			http.Error(w, "未启用历史面试检索", http.StatusServiceUnavailable)
			return
		}
		user := r.URL.Query().Get("user")
		if !idPattern.MatchString(user) {
			http.Error(w, "bad user", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodDelete {
			session := r.URL.Query().Get("session")
			if !idPattern.MatchString(session) {
				http.Error(w, "bad session", http.StatusBadRequest)
				return
			}
			if err := m.remove(r.Context(), user, session); err != nil {
				log.Printf("memory remove: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		var s savedSession
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil || !idPattern.MatchString(s.ID) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		n, err := m.save(r.Context(), user, s)
		if err != nil {
			log.Printf("memory save: %v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"chunks": n})
	}
}
