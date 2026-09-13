package main

import (
	"log"

	"cmd2api/internal/config"
	"cmd2api/internal/server"
)

func main() {
	cfg := config.Load()
	if err := server.New(cfg).ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
