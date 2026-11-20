package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gr1ksdev/UnoKamikaze/internal/app"
	"github.com/gr1ksdev/UnoKamikaze/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("UnoKamikaze stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: config.LogLevel}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, cfg, logger)
}
