package handler

import (
	"context"
	"math"
	"net/url"
	"strconv"

	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type GeoService interface {
	Nearby(context.Context, service.GeoQuery) ([]service.NearbyShop, error)
}
type Geo struct{ service GeoService }

func NewGeo(s GeoService) *Geo          { return &Geo{service: s} }
func (g *Geo) Register(h *server.Hertz) { h.GET("/api/v1/shops/nearby", g.nearby) }
func (g *Geo) nearby(ctx context.Context, c *app.RequestContext) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	typeID, typeErr := shopQueryNumber(q, "type_id", 0, ^uint64(0))
	lng, lngErr := geoQueryNumber(q, "longitude", 0, true, -180, 180)
	lat, latErr := geoQueryNumber(q, "latitude", 0, true, -85.05112878, 85.05112878)
	radius, radiusErr := geoQueryNumber(q, "radius_m", 5000, false, 1, 50000)
	limit, limitErr := shopQueryNumber(q, "limit", 10, 50)
	if typeID == 0 || typeErr != nil || lngErr != nil || latErr != nil || radiusErr != nil || limitErr != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	result, err := g.service.Nearby(ctx, service.GeoQuery{TypeID: typeID, Longitude: lng, Latitude: lat, RadiusMeters: radius, Limit: int(limit)})
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func geoQueryNumber(q url.Values, key string, fallback float64, required bool, min, max float64) (float64, error) {
	values, exists := q[key]
	if !exists && !required {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, apperror.New(apperror.Validation, nil)
	}
	n, err := strconv.ParseFloat(values[0], 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < min || n > max {
		return 0, apperror.New(apperror.Validation, nil)
	}
	return n, nil
}
