# cmd2api — notes for agents

Go reverse proxy: OpenAI `/v1/chat/completions` + Anthropic `/v1/messages` → Command Code `POST /alpha/generate`. Cheap Command Code plans have no official OpenAI Provider API.

Root [`README.md`](../README.md) is for humans (Docker). This file is for agents changing the code.

## Layout

```
cmd/cmd2api/              entrypoint
internal/config/          flags + env
internal/cc/              upstream client, fingerprint, NDJSON stream
internal/convert/         OpenAI / Anthropic ↔ CC wire format
internal/server/          HTTP + access logs
internal/models/          fallback catalog
internal/id/              uuid helper (no google/uuid)
readme-for-agent/         this directory
```

Zero third-party Go modules. Do not add dependencies unless asked.

## Commands

Dev shell is devenv (`devenv.nix`). Canonical tasks:

```bash
devenv tasks run app:test    # go test ./...
devenv tasks run app:fmt     # gofumpt -w .
devenv tasks run app:run     # go run ./cmd/cmd2api
devenv tasks run app:build   # bin/cmd2api
```

`devenv tasks run app:test` may print a trailing `{}` (runner JSON). That is not a failure. Prefer `go test ./...` if the task output is empty.

Docker Hub often times out in CN. Build with:

```bash
docker compose build \
  --build-arg GO_IMAGE=docker.m.daocloud.io/library/golang:1.24-alpine \
  --build-arg RUN_IMAGE=docker.m.daocloud.io/library/alpine:3.20 \
  --build-arg GOPROXY=https://goproxy.cn,direct
```

Default listen: container `0.0.0.0:8787`, local binary `127.0.0.1:8787`.

## Secrets

- Never commit `.env`, traces, or a real `user_...` key.
- `.env.example` is the only tracked env file.
- Logs must mask keys (`user_xxxx…abcd`). Never log full prompts or Authorization headers.
- Do not write keys into README, tests, or commit messages.

## Protocol — do not "simplify"

Upstream is **not** OpenAI. Requests go to `https://api.commandcode.ai/alpha/generate` as CLI-shaped JSON.

Required request headers (from official `command-code` CLI):

- `Authorization: Bearer user_...`
- `User-Agent: cli`
- `x-cli-environment: production`
- `x-command-code-version`
- `x-session-id`
- `x-project-slug`
- `x-taste-learning: false`
- `x-co-flag: false`

Warmup (best-effort, errors swallowed today): `POST /alpha/fingerprint/record` and `POST /alpha/lifecycle-events`. Fingerprints are synthetic (format-compatible, not real hardware).

Wire body: `{ config, memory, taste, skills, permissionMode, threadId, params }`. Messages use CC parts: `text`, `reasoning`, `tool-call`, `tool-result`, `image`. `params.stream` is always `true` upstream.

If the client sends no system prompt, send `params.system = " "` (space). Omitting `system` lets upstream inject ~7.5k CLI default prompt. This is undocumented; do not remove without an explicit user decision. If the client **does** send a system prompt (pi does), forward it verbatim — do not wrap with Command Code identity text.

Upstream stream is NDJSON (`text-delta`, `reasoning-delta`, `tool-call`, `finish`, …), not SSE. Translate out to OpenAI SSE or Anthropic SSE.

Function calling is protocol-level only. Command Code's own tools/mods/harness do not run here; the client (pi) executes tools.

## What this is not

- Not a fork of `MAXeaglet/commandcode-proxy`. Do not vendor that file. Protocol knowledge came from the public `command-code` npm CLI plus community proxies; keep our Go code small.
- Not Command Code mods. Mods cannot expose the subscription as an HTTP API.
- Missing vs the mature Node proxy (do not pretend they exist): client-disconnect abort of upstream, stream idle watchdog, SSE keepalive, `/v1/responses`, dynamic CLI version, `traceparent`, inflight cap.

## Tests

`internal/convert` and `internal/server` have unit tests with a fake upstream. After protocol or HTTP changes, run `go test ./...`. Do not hit `api.commandcode.ai` from tests.

## Git

Commit and push only when the user asks. Do not force-push `main`.
