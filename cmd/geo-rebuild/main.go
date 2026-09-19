package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("usage: go run ./cmd/geo-rebuild [-type-id ID] (default: all current categories)")
			return
		}
		logger.Error("GEO rebuild failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("geo-rebuild", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	typeID := flags.Uint64("type-id", 0, "category to rebuild; 0 rebuilds all current categories")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: geo-rebuild [-type-id ID]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	startup, startupCancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	deps, err := platform.Open(startup, cfg)
	startupCancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := deps.Close(); err != nil {
			logger.Error("connection pool close failed")
		}
	}()
	repo := repository.NewShop(deps.DB)
	types, err := repo.Types(ctx)
	if err != nil {
		return err
	}
	geo := service.NewGeo(repo, cache.NewGeo(deps.Redis))
	matched := false
	for _, category := range types {
		if *typeID != 0 && category.ID != *typeID {
			continue
		}
		matched = true
		n, err := geo.Rebuild(ctx, category.ID)
		if err != nil {
			return fmt.Errorf("category %d: %w", category.ID, err)
		}
		logger.Info("GEO category rebuilt", "type_id", category.ID, "shops", n)
	}
	if *typeID != 0 && !matched {
		return errors.New("unknown shop type")
	}
	logger.Info("GEO rebuild completed")
	return nil
}
