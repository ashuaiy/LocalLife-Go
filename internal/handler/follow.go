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

type FollowService interface {
	Set(context.Context, uint64, uint64, bool) (service.FollowResult, error)
	State(context.Context, uint64, uint64) (service.FollowResult, error)
	List(context.Context, uint64, int, int) (service.FollowingPage, error)
}
type Follow struct {
	service FollowService
	auth    middleware.Authenticator
}

func NewFollow(s FollowService, auth middleware.Authenticator) *Follow {
	return &Follow{service: s, auth: auth}
}
func (f *Follow) Register(h *server.Hertz) {
	group := h.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) }, middleware.RequireAuth(f.auth))
	group.PUT("/users/:id/follow", f.set(true))
	group.DELETE("/users/:id/follow", f.set(false))
	group.GET("/users/:id/follow", f.state)
	group.GET("/users/me/following", f.list)
}
func (f *Follow) set(following bool) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
		if err != nil {
			response.Fail(ctx, c, err)
			return
		}
		result, err := f.service.Set(ctx, middleware.UserID(ctx), id, following)
		if err != nil {
			response.Fail(ctx, c, err)
			return
		}
		response.Success(ctx, c, result)
	}
}
func (f *Follow) state(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := f.service.State(ctx, middleware.UserID(ctx), id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (f *Follow) list(ctx context.Context, c *app.RequestContext) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	page, pageErr := shopQueryNumber(q, "page", 1, 1000)
	size, sizeErr := shopQueryNumber(q, "page_size", 10, 50)
	if pageErr != nil || sizeErr != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := f.service.List(ctx, middleware.UserID(ctx), int(page), int(size))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
