package config

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"time"
)

const Version = "0.1.0"

type Config struct {
	Host        string
	Port        int
	APIBase     string
	APIKey      string
	ProjectSlug string
	ZDR         bool
	CLIVersion  string
	MaxBodyMB   int
	IdleTimeout time.Duration
}

func Load() Config {
	cfg := Config{
		Host:        env("HOST", "127.0.0.1"),
		Port:        envInt("PORT", 8787),
		APIBase:     strings.TrimRight(env("CC_API_BASE", "https://api.commandcode.ai"), "/"),
		APIKey:      firstEnv("CC_API_KEY", "COMMANDCODE_API_KEY", "CMD_API_KEY"),
		ProjectSlug: env("PROJECT_SLUG", "command2api"),
		ZDR:         env("CMD_ZDR", "") == "1",
		CLIVersion:  env("CC_VERSION", "1.53.1"),
		MaxBodyMB:   envInt("CC_MAX_BODY_MB", 32),
		IdleTimeout: time.Duration(envInt("CC_IDLE_TIMEOUT_SEC", 90)) * time.Second,
	}

	flag.StringVar(&cfg.Host, "host", cfg.Host, "bind address")
	flag.IntVar(&cfg.Port, "port", cfg.Port, "bind port")
	flag.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "fallback Command Code API key (user_...)")
	flag.StringVar(&cfg.APIBase, "api-base", cfg.APIBase, "Command Code API base URL")
	flag.Parse()
	return cfg
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
