// Command seckill controls activity activation and the standalone order worker.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const usage = "usage: seckill -mode enable -voucher-id ID | seckill -mode worker"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(usage)
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		logger.Error("seckill command failed", "error", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("seckill", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.String("mode", "", "enable or worker")
	id := flags.Uint64("voucher-id", 0, "unused activity to activate")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*mode != "enable" && *mode != "worker") || (*mode == "enable" && *id == 0) || (*mode == "worker" && *id != 0) {
		return errors.New(usage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	deps, err := platform.Open(startup, cfg)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := deps.Close(); err != nil {
			logger.Error("connection pool close failed")
		}
	}()
	s := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), cache.NewSeckill(deps.Redis))
	if *mode == "enable" {
		operation, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := s.Enable(operation, *id); err != nil {
			return err
		}
		logger.Info("async seckill enabled", "voucher_id", *id)
		return nil
	}
	consumer := "worker-" + rand.Text()
	logger.Info("seckill worker started", "consumer", consumer)
	return s.RunWorker(ctx, consumer, logger)
}
