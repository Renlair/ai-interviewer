# AI 面试官

一个了解你的 AI 面试教练。当前是阶段一的最小版本：选方向（产品经理、架构师、技术负责人等）→ 文字面试（流式输出、可随时打断）→ 复盘报告。

## 运行

需要一个 OpenAI 兼容接口的 key（OpenAI、DeepSeek、通义、Kimi 等都可以）。

```bash
OPENAI_API_KEY=sk-... OPENAI_BASE_URL=https://api.deepseek.com/v1 OPENAI_MODEL=deepseek-chat go run .
```

或者用 Docker：

```bash
docker build -t ai-interviewer .
docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... -e OPENAI_BASE_URL=... -e OPENAI_MODEL=... ai-interviewer
```

打开 http://localhost:8080 。

| 环境变量 | 默认值 |
| --- | --- |
| `OPENAI_API_KEY` | 必填 |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | `gpt-4o-mini` |
| `ADDR` | `:8080` |
| `EMBEDDING_API_KEY` | 不填则不启用历史面试检索 |
| `EMBEDDING_BASE_URL` | `https://dashscope.aliyuncs.com/compatible-mode/v1`（阿里云百炼） |
| `EMBEDDING_MODEL` | `text-embedding-v4` |
| `EMBEDDING_DIM` | `1024`（建好索引后不要再改） |
| `REDIS_ADDR` | `localhost:6379` |

### 历史面试检索（可选）

面试官会参考你以前面试里的相关问答和复盘：不重复问过的问题，以前答得弱的点换个角度再考。需要一个向量模型的 key 和一个带搜索模块的 Redis：

```bash
docker run -d --name ai-interviewer-redis --restart unless-stopped -p 6379:6379 -v ai-interviewer-redis:/data redis/redis-stack-server:7.4.0-v3
```

没有配置或 Redis 没启动时，服务照常运行，只是不检索历史。

## 结构

- `main.go`：HTTP 服务。`/api/chat` 面试、`/api/review` 复盘、`/api/parse` 解析上传的简历 / 职位描述、`/api/memory` 写入或删除一场面试的检索索引。面试过程不存状态，前端每次发送完整对话、上一轮的阶段和简历 / 职位描述。
- `interview.go`：用 [Eino](https://github.com/cloudwego/eino) Graph 编排每一轮面试：检索历史面试 → 评估候选人的回答 → 按评估、轮数和时间决定阶段 → 面试官提问。阶段（了解候选人、经历深挖、情景题、反问、收尾）和每个阶段的时间、轮数上限也在这里。
- `memory.go`：历史面试的切块（每个一问一答一块、复盘按段落分块）、向量化和 Redis 索引 / 检索。
- `docs.go`：简历 / 职位描述解析，支持 PDF、Word 和纯文本。
- `prompt.go`：评估、面试官和复盘的提示词，以及各面试方向的考察重点。简历和职位描述全文放进提示词。
- `llm.go`：创建 OpenAI 兼容接口的模型，把流式输出转发给前端。
- `web/index.html`：整个前端，一个文件。简历、职位描述和历史记录存在浏览器的 localStorage 里。
