ARG GO_IMAGE=golang:1.24-alpine
ARG RUN_IMAGE=alpine:3.20

FROM ${GO_IMAGE} AS builder
WORKDIR /src

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux

COPY go.mod ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags="-s -w" -o /out/command2api ./cmd/command2api

FROM ${RUN_IMAGE}
RUN apk --no-cache add ca-certificates tzdata wget \
 && adduser -D -H -u 65532 app
WORKDIR /app
COPY --from=builder /out/command2api /app/command2api

ENV HOST=0.0.0.0 \
    PORT=8787 \
    TZ=Asia/Shanghai

USER 65532
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8787/health >/dev/null || exit 1

ENTRYPOINT ["/app/command2api"]
