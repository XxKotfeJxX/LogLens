package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"loglens/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		slog.Error("loglens stopped", "error", err)
		os.Exit(1)
	}
}
