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
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
)

const usage = "usage: go run ./cmd/shop-update -id ID [-name NAME] [-address ADDRESS] (at least one field required)"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(usage)
			return
		}
		logger.Error("shop update failed", "error", err.Error())
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("shop-update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.Uint64("id", 0, "shop ID")
	name := flags.String("name", "", "new name")
	address := flags.String("address", "", "new address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var edit model.ShopTextEdit
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "name":
			edit.Name = name
		case "address":
			edit.Address = address
		}
	})
	if flags.NArg() != 0 || *id == 0 || (edit.Name == nil && edit.Address == nil) {
		return errors.New(usage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deps, err := platform.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer deps.Close()
	result, err := service.NewShopMaintenance(repository.NewShop(deps.DB), cache.NewShop(deps.Redis)).Update(ctx, *id, edit)
	if err != nil {
		if result.ID != 0 {
			return errors.New("database update committed but cache invalidation failed; retry the same edit after Redis recovers")
		}
		return err
	}
	logger.Info("shop updated and cache invalidated", "shop_id", result.ID)
	return nil
}
