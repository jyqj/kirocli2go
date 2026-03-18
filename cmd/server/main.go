package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"kirocli-go/internal/bootstrap"
	"kirocli-go/internal/config"
)

func main() {
	cfg := config.FromEnv()

	app, err := bootstrap.NewApp(cfg)
	if err != nil {
		log.Fatalf("bootstrap app: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		log.Fatalf("run app: %v", err)
	}
}
