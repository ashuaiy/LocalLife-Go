package handler

import (
	"context"
	"github.com/ashuaiy/local-life-go/internal/middleware"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"net/url"
)

type OrderService interface {
	Place(context.Context, uint64, uint64) (service.OrderView, error)
	Detail(context.Context, uint64, uint64) (service.OrderView, error)
	List(context.Context, uint64, uint64, int, int) (service.OrderPage, error)
}
type Order struct {
	service OrderService
	auth    middleware.Authenticator
}

func NewOrder(s OrderService, auth middleware.Authenticator) *Order {
	return &Order{service: s, auth: auth}
}
func (o *Order) Register(h *server.Hertz) {
	g := h.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) }, middleware.RequireAuth(o.auth))
	g.POST("/vouchers/:id/seckill", o.place)
	g.GET("/orders", o.list)
	g.GET("/orders/:id", o.detail)
}
func (o *Order) place(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil || len(c.Request.Body()) != 0 {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := o.service.Place(ctx, middleware.UserID(ctx), id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (o *Order) detail(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := o.service.Detail(ctx, middleware.UserID(ctx), id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (o *Order) list(ctx context.Context, c *app.RequestContext) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	voucher, voucherErr := shopQueryNumber(q, "voucher_id", 0, ^uint64(0))
	page, pageErr := shopQueryNumber(q, "page", 1, 1000)
	size, sizeErr := shopQueryNumber(q, "page_size", 10, 50)
	if voucherErr != nil || pageErr != nil || sizeErr != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := o.service.List(ctx, middleware.UserID(ctx), voucher, int(page), int(size))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
