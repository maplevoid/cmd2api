# Command2Api

把 [Command Code](https://commandcode.ai) 订阅（含 $1 Go 套餐）转成 **OpenAI / Anthropic 兼容 HTTP API**，给 pi、OpenCode、Claude Code 或其他 agent 用。

Go 套餐没有官方 OpenAI Provider 接口。本代理把客户端请求转到 CLI 协议 `POST /alpha/generate`。

| 客户端 | 路径 | 上游 |
| --- | --- | --- |
| OpenAI SDK / OpenCode / pi | `POST /v1/chat/completions` | `POST /alpha/generate` |
| Anthropic SDK / Claude Code | `POST /v1/messages` | `POST /alpha/generate` |
| 任意 | `GET /v1/models` | `GET /provider/v1/models`（失败则用内置列表） |

支持流式、工具调用、图片、`reasoning_effort`。客户端自己的 system prompt 原样转发；没有 system 时发空格占位，避免上游注入 Command Code 默认提示词。

---

## Docker 启动

需要：Docker + Docker Compose，以及 Command Code Studio 的 API key（`user_...`）。

```bash
git clone <this-repo> Command2Api
cd Command2Api
cp .env.example .env
# 编辑 .env，填入 CC_API_KEY=user_...
docker compose up --build -d
curl http://127.0.0.1:8787/health
```

`.env` 示例：

```
CC_API_KEY=user_xxxxxxxx
```

拉 Docker Hub 超时（国内常见）时，用镜像源构建：

```bash
docker compose build \
  --build-arg GO_IMAGE=docker.m.daocloud.io/library/golang:1.24-alpine \
  --build-arg RUN_IMAGE=docker.m.daocloud.io/library/alpine:3.20 \
  --build-arg GOPROXY=https://goproxy.cn,direct
docker compose up -d
```

key 也可以不写进容器，每次请求带 `Authorization: Bearer user_...`。

停掉：

```bash
docker compose down
```

---

## 给其他 agent 用

默认地址：`http://127.0.0.1:8787/v1`

**pi**（`~/.pi/agent/models.json`）：

```json
{
  "providers": {
    "command2api": {
      "baseUrl": "http://127.0.0.1:8787/v1",
      "api": "openai-completions",
      "apiKey": "user_xxxxxxxx",
      "compat": { "supportsDeveloperRole": false },
      "models": [
        {
          "id": "xiaomi/mimo-v2.5",
          "name": "MiMo V2.5",
          "contextWindow": 1000000,
          "maxTokens": 64000,
          "reasoning": true
        }
      ]
    }
  }
}
```

会话里 `/model command2api / xiaomi/mimo-v2.5`。

**curl（OpenAI）：**

```bash
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer $CC_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"xiaomi/mimo-v2.5","messages":[{"role":"user","content":"hi"}]}'
```

**curl（Anthropic）：**

```bash
curl http://127.0.0.1:8787/v1/messages \
  -H "x-api-key: $CC_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"xiaomi/mimo-v2.5","max_tokens":256,"messages":[{"role":"user","content":"hi"}]}'
```

Go 套餐可用：`xiaomi/mimo-v2.5`、`deepseek/deepseek-v4-flash`、`Qwen/Qwen3.7-Flash`。Claude / GPT 需要更高档套餐。

---

## 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `HOST` | 容器内 `0.0.0.0` | 监听地址 |
| `PORT` | `8787` | 端口 |
| `CC_API_KEY` | 空 | 请求没带 key 时的兜底 |
| `CC_API_BASE` | `https://api.commandcode.ai` | 上游 |
| `CMD_ZDR=1` | 关 | 发送 `x-cmd-zdr: 1` |

---

## 本地开发（可选）

```bash
devenv shell
devenv tasks run app:test
devenv tasks run app:run
```
