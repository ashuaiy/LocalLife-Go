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

type BlogService interface {
	Publish(context.Context, uint64, service.BlogInput) (service.BlogView, error)
	Detail(context.Context, uint64) (service.BlogView, error)
	List(context.Context, uint64, uint64, int, int) (service.BlogPage, error)
	Like(context.Context, uint64, uint64, bool) (service.LikeResult, error)
	LikeState(context.Context, uint64, uint64) (service.LikeResult, error)
}
type Blog struct {
	service BlogService
	auth    middleware.Authenticator
}

func NewBlog(s BlogService, auth middleware.Authenticator) *Blog {
	return &Blog{service: s, auth: auth}
}
func (b *Blog) Register(h *server.Hertz) {
	h.GET("/api/v1/blogs", b.list)
	h.GET("/api/v1/blogs/:id", b.detail)
	protected := h.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) }, middleware.RequireAuth(b.auth))
	protected.POST("/blogs", b.publish)
	protected.PUT("/blogs/:id/like", b.like(true))
	protected.DELETE("/blogs/:id/like", b.like(false))
	protected.GET("/blogs/:id/like", b.likeState)
}
func (b *Blog) publish(ctx context.Context, c *app.RequestContext) {
	var input service.BlogInput
	if err := decodeJSONLimit(c, &input, 64*1024); err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := b.service.Publish(ctx, middleware.UserID(ctx), input)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (b *Blog) detail(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := b.service.Detail(ctx, id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (b *Blog) list(ctx context.Context, c *app.RequestContext) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	shopID, shopErr := shopQueryNumber(q, "shop_id", 0, ^uint64(0))
	userID, userErr := shopQueryNumber(q, "user_id", 0, ^uint64(0))
	page, pageErr := shopQueryNumber(q, "page", 1, 1000)
	size, sizeErr := shopQueryNumber(q, "page_size", 10, 50)
	if shopErr != nil || userErr != nil || pageErr != nil || sizeErr != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := b.service.List(ctx, shopID, userID, int(page), int(size))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (b *Blog) like(liked bool) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
		if err != nil {
			response.Fail(ctx, c, err)
			return
		}
		result, err := b.service.Like(ctx, id, middleware.UserID(ctx), liked)
		if err != nil {
			response.Fail(ctx, c, err)
			return
		}
		response.Success(ctx, c, result)
	}
}
func (b *Blog) likeState(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := b.service.LikeState(ctx, id, middleware.UserID(ctx))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
