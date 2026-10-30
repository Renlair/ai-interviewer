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

## 结构

- `main.go`：HTTP 服务，`/api/chat` 面试、`/api/review` 复盘。后端不存状态，前端每次发送完整对话。
- `llm.go`：调用 OpenAI 兼容接口并把流式输出转发给前端。
- `prompt.go`：面试官和复盘的提示词，各面试方向的考察重点和面试流程（了解候选人、经历深挖、情景题、反问、收尾）写在这里。
- `web/index.html`：整个前端，一个文件。
