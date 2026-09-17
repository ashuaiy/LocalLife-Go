package app

import (
	"context"
	"log/slog"
	"sort"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/middleware"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network/standard"
)

type Check func(context.Context) error

func NewServer(cfg config.Config, logger *slog.Logger, checks map[string]Check, extra ...hertzconfig.Option) *server.Hertz {
	opts := []hertzconfig.Option{
		server.WithHostPorts(cfg.HTTP.Addr), server.WithTransport(standard.NewTransporter),
		server.WithReadTimeout(cfg.HTTP.RequestTimeout), server.WithWriteTimeout(cfg.HTTP.RequestTimeout),
		server.WithIdleTimeout(cfg.HTTP.RequestTimeout), server.WithExitWaitTime(cfg.ShutdownTimeout),
		server.WithHandleMethodNotAllowed(true), server.WithDisablePrintRoute(true),
	}
	h := server.New(append(opts, extra...)...)
	h.Use(middleware.HTTP(logger, cfg.HTTP.RequestTimeout))
	h.GET("/healthz", func(ctx context.Context, c *app.RequestContext) {
		response.Success(ctx, c, map[string]string{"status": "alive"})
	})
	// Sort once for deterministic checks and stop on the first failure within one shared deadline.
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	h.GET("/readyz", func(ctx context.Context, c *app.RequestContext) {
		probeCtx, cancel := context.WithTimeout(ctx, cfg.DependencyTimeout)
		defer cancel()
		for _, name := range names {
			if err := checks[name](probeCtx); err != nil {
				response.Fail(ctx, c, apperror.New(apperror.Dependency, nil))
				return
			}
		}
		response.Success(ctx, c, map[string]string{"status": "ready"})
	})
	h.NoRoute(func(ctx context.Context, c *app.RequestContext) {
		response.Fail(ctx, c, apperror.New(apperror.NotFound, nil))
	})
	h.NoMethod(func(ctx context.Context, c *app.RequestContext) {
		response.Fail(ctx, c, apperror.New(apperror.MethodNotAllowed, nil))
	})
	return h
}
