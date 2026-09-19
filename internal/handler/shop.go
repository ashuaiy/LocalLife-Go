package handler

import (
	"context"
	"net/url"
	"strconv"

	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type ShopService interface {
	Types(context.Context) ([]service.ShopTypeView, error)
	List(context.Context, uint64, int, int) (service.ShopPage, error)
	Detail(context.Context, uint64) (service.ShopView, error)
}
type Shop struct{ service ShopService }

func NewShop(s ShopService) *Shop { return &Shop{service: s} }
func (s *Shop) Register(h *server.Hertz) {
	group := h.Group("/api/v1")
	group.GET("/shop-types", s.types)
	group.GET("/shops", s.list)
	group.GET("/shops/:id", s.detail)
}

func (s *Shop) types(ctx context.Context, c *app.RequestContext) {
	items, err := s.service.Types(ctx)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, items)
}

func (s *Shop) list(ctx context.Context, c *app.RequestContext) {
	query, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	typeID, typeErr := shopQueryNumber(query, "type_id", 0, ^uint64(0))
	page, pageErr := shopQueryNumber(query, "page", 1, 1000)
	size, sizeErr := shopQueryNumber(query, "page_size", 10, 50)
	if typeErr != nil || pageErr != nil || sizeErr != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	items, err := s.service.List(ctx, typeID, int(page), int(size))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, items)
}

func (s *Shop) detail(ctx context.Context, c *app.RequestContext) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	item, err := s.service.Detail(ctx, id)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, item)
}

func shopQueryNumber(query url.Values, key string, fallback, max uint64) (uint64, error) {
	values, exists := query[key]
	if !exists {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, apperror.New(apperror.Validation, nil)
	}
	return shopPositiveNumber(values[0], max)
}

func shopPositiveNumber(value string, max uint64) (uint64, error) {
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, apperror.New(apperror.Validation, nil)
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == 0 || n > max {
		return 0, apperror.New(apperror.Validation, nil)
	}
	return n, nil
}
