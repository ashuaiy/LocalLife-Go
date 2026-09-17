package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/migration"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) != 2 || os.Args[1] != "up" {
		logger.Error("usage: go run ./cmd/migrate up")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration invalid", "error", err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := migration.Up(ctx, cfg); err != nil {
		logger.Error("migration failed", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("database schema is current")
}
