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
	shop := service.NewShop(repository.NewShop(deps.DB), cache.NewShop(deps.Redis))
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := shop.Close(closeCtx); err != nil {
			logger.Error("shop cache shutdown failed")
		}
	}()
	geo := service.NewGeo(repository.NewShop(deps.DB), cache.NewGeo(deps.Redis))
	followRepo := repository.NewFollow(deps.DB)
	blogRepo := repository.NewBlog(deps.DB)
	feed := service.NewFeed(followRepo, blogRepo, cache.NewFeed(deps.Redis))
	follow := service.NewFollow(followRepo, repository.NewUser(deps.DB))
	communityRepo := repository.NewCommunity(deps.DB)
	communityStore := cache.NewCommunity(deps.Redis)
	community := service.NewCommunity(communityRepo, communityStore)
	media := service.NewMedia(cfg.UploadDir)
	blog := service.NewBlog(blogRepo, repository.NewShop(deps.DB), feed).WithNotifications(communityRepo, communityStore)
	voucher := service.NewVoucher(repository.NewVoucher(deps.DB))
	order := service.NewOrder(repository.NewOrder(deps.DB))
	asyncOrder := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), cache.NewSeckill(deps.Redis))
	return app.Serve(ctx, cfg, logger, map[string]app.Check{
		"mysql": deps.SQL.PingContext,
		"redis": func(ctx context.Context) error { return deps.Redis.Ping(ctx).Err() },
	}, handler.NewAuth(auth).Register, handler.NewShop(shop).Register, handler.NewGeo(geo).Register, handler.NewBlog(blog, auth).Register, handler.NewFollow(follow, auth).Register, handler.NewFeed(feed, auth).Register, handler.NewVoucher(voucher).Register, handler.NewOrder(order, auth).Register, handler.NewAsyncOrder(asyncOrder, auth).Register, handler.NewCommunity(community, media, auth).Register)
}
