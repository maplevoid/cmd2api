{ pkgs, ... }:
{
  packages = [
    pkgs.git
    pkgs.gofumpt
    pkgs.gotestsum
  ];

  languages.go.enable = true;

  # Canonical task names — use these instead of raw commands.
  tasks = {
    "app:run".exec = "go run ./cmd/command2api";
    "app:test".exec = "go test ./...";
    "app:fmt".exec = "gofumpt -w .";
    "app:build".exec = "mkdir -p bin && go build -o bin/command2api ./cmd/command2api";
  };
}
