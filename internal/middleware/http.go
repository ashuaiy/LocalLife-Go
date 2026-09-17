package middleware

import (
	"context"
	"crypto/rand"
	"log/slog"
	"time"

	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/requestid"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
)

// HTTP supplies request identity, cooperative deadlines, recovery and safe access logs.
// Handlers run synchronously: never let a goroutine retain Hertz's pooled RequestContext.
func HTTP(logger *slog.Logger, timeout time.Duration) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		id := string(c.Request.Header.Peek("X-Request-ID"))
		if !validID(id) {
			id = rand.Text()
		}
		c.Header("X-Request-ID", id)
		ctx = requestid.WithContext(ctx, id)
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		defer func() {
			if recover() != nil {
				c.Response.ResetBody()
				response.Fail(ctx, c, apperror.New(apperror.Internal, nil))
				c.Abort()
			}
			logger.InfoContext(ctx, "http request",
				"request_id", id, "method", string(c.Method()), "path", string(c.Path()),
				"latency_ms", float64(time.Since(start).Microseconds())/1000,
				"status", c.Response.StatusCode(), "error", response.ErrorCode(c))
		}()
		c.Next(ctx)
		if ctx.Err() != nil && c.Response.StatusCode() < 400 {
			// Hertz JSON rendering appends; replace any late successful body.
			c.Response.ResetBody()
			response.Fail(ctx, c, ctx.Err())
		}
	}
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}
