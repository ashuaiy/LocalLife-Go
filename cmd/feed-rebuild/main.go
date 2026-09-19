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
			fmt.Println("usage: go run ./cmd/feed-rebuild -user-id ID (pause publishing and follow changes while rebuilding)")
			return
		}
		logger.Error("feed rebuild failed", "error", err.Error())
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("feed-rebuild", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	userID := flags.Uint64("user-id", 0, "reader whose feed should be rebuilt")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *userID == 0 {
		return errors.New("usage: feed-rebuild -user-id ID")
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
	exists, err := repository.NewUser(deps.DB).Exists(ctx, *userID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("unknown user")
	}
	feed := service.NewFeed(repository.NewFollow(deps.DB), repository.NewBlog(deps.DB), cache.NewFeed(deps.Redis))
	n, err := feed.Rebuild(ctx, *userID)
	if err != nil {
		return err
	}
	logger.Info("feed rebuilt", "user_id", *userID, "blogs", n)
	return nil
}
