package handler

import (
	"context"
	"net/url"

	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type VoucherService interface {
	List(context.Context, uint64, int, int) (service.VoucherPage, error)
	Detail(context.Context, uint64) (service.VoucherView, error)
}
type Voucher struct{ service VoucherService }

func NewVoucher(s VoucherService) *Voucher { return &Voucher{service: s} }
func (v *Voucher) Register(h *server.Hertz) {
	group := h.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) })
	group.GET("/vouchers", v.list)
	group.GET("/vouchers/:id", v.detail)
}
func (v *Voucher) list(ctx context.Context, c *app.RequestContext) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	shopID, shopErr := shopQueryNumber(q, "shop_id", 0, ^uint64(0))
	page, pageErr := shopQueryNumber(q, "page", 1, 1000)
	size, sizeErr := shopQueryNumber(q, "page_size", 10, 50)
	if shopErr != nil || pageErr != nil || sizeErr != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := v.service.List(ctx, shopID, int(page), int(size))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (v *Voucher) detail(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := v.service.Detail(ctx, id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
