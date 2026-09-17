package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/cloudwego/hertz/pkg/app/server"
)

// Serve binds before starting Hertz, propagates startup errors and drains on cancellation.
func Serve(ctx context.Context, cfg config.Config, logger *slog.Logger, checks map[string]Check) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return serveListener(ctx, cfg, logger, checks, ln)
}

type readyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

func serveListener(ctx context.Context, cfg config.Config, logger *slog.Logger, checks map[string]Check, ln net.Listener) error {
	defer ln.Close()
	listener := &readyListener{Listener: ln, ready: make(chan struct{})}
	h := NewServer(cfg, logger, checks, server.WithListener(listener))
	done := make(chan error, 1)
	go func() { done <- h.Run() }()
	// Initialization has no blocking user hooks. Wait until it either fails or
	// reaches Accept, then route cancellation through Shutdown in every case.
	// Closing only the listener here could leave already accepted requests alive.
	select {
	case err := <-done:
		return err
	case <-listener.ready:
		logger.Info("server started", "address", ln.Addr().String())
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		err := h.Shutdown(shutdownCtx)
		// Hertz suppresses a transport context deadline; preserve it for callers.
		err = errors.Join(err, shutdownCtx.Err())
		select {
		case runErr := <-done:
			return errors.Join(err, runErr)
		case <-shutdownCtx.Done():
			return errors.Join(err, shutdownCtx.Err())
		}
	}
}
