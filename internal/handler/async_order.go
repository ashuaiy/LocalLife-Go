package handler

import (
	"context"
	"github.com/ashuaiy/local-life-go/internal/middleware"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type AsyncOrderService interface {
	Accept(context.Context, uint64, uint64) (service.SeckillView, error)
	Result(context.Context, uint64, uint64) (service.SeckillView, error)
}
type AsyncOrder struct {
	service AsyncOrderService
	auth    middleware.Authenticator
}

func NewAsyncOrder(s AsyncOrderService, auth middleware.Authenticator) *AsyncOrder {
	return &AsyncOrder{service: s, auth: auth}
}
func (o *AsyncOrder) Register(h *server.Hertz) {
	g := h.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) }, middleware.RequireAuth(o.auth))
	g.POST("/vouchers/:id/seckill-async", o.accept)
	g.GET("/vouchers/:id/seckill-result", o.result)
}
func (o *AsyncOrder) accept(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil || len(c.Request.Body()) != 0 {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := o.service.Accept(ctx, middleware.UserID(ctx), id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
	c.SetStatusCode(202)
}
func (o *AsyncOrder) result(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := o.service.Result(ctx, middleware.UserID(ctx), id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
