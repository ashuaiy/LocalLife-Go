package handler

import (
	"context"
	"net/url"

	"github.com/ashuaiy/local-life-go/internal/middleware"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type FeedService interface {
	Read(context.Context, uint64, string, int) (service.FeedPage, error)
}
type Feed struct {
	service FeedService
	auth    middleware.Authenticator
}

func NewFeed(s FeedService, auth middleware.Authenticator) *Feed {
	return &Feed{service: s, auth: auth}
}
func (f *Feed) Register(h *server.Hertz) {
	h.GET("/api/v1/feed", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) }, middleware.RequireAuth(f.auth), f.read)
}
func (f *Feed) read(ctx context.Context, c *app.RequestContext) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	size, err := shopQueryNumber(q, "page_size", 10, 50)
	if err != nil || len(q["cursor"]) > 1 {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := f.service.Read(ctx, middleware.UserID(ctx), q.Get("cursor"), int(size))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
