package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	deps, err := platform.Open(startupCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := deps.Close(); err != nil {
			logger.Error("connection pool close failed")
		}
	}()
	auth := service.NewAuth(repository.NewUser(deps.DB), cache.NewAuth(deps.Redis), cfg.Auth)
	return app.Serve(ctx, cfg, logger, map[string]app.Check{
		"mysql": deps.SQL.PingContext,
		"redis": func(ctx context.Context) error { return deps.Redis.Ping(ctx).Err() },
	}, handler.NewAuth(auth).Register)
}
