package service

import (
	"context"
	"math"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type GeoShops interface {
	ByIDs(context.Context, []uint64) ([]model.Shop, error)
	ForType(context.Context, uint64) ([]model.Shop, error)
}
type GeoIndex interface {
	Nearby(ctx context.Context, typeID uint64, longitude, latitude, radius float64, limit int) ([]model.GeoHit, error)
	Replace(context.Context, uint64, []model.Shop) error
}
type Geo struct {
	shops GeoShops
	index GeoIndex
}

func NewGeo(shops GeoShops, index GeoIndex) *Geo { return &Geo{shops: shops, index: index} }

type GeoQuery struct {
	TypeID                            uint64
	Longitude, Latitude, RadiusMeters float64
	Limit                             int
}
type NearbyShop struct {
	ShopView
	DistanceMeters float64 `json:"distance_m"`
}

func (g *Geo) Nearby(ctx context.Context, query GeoQuery) ([]NearbyShop, error) {
	if query.TypeID == 0 || !geoRange(query.Longitude, -180, 180) || !geoRange(query.Latitude, -85.05112878, 85.05112878) || !geoRange(query.RadiusMeters, 1, 50000) || query.Limit < 1 || query.Limit > 50 {
		return nil, apperror.New(apperror.Validation, nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hits, err := g.index.Nearby(ctx, query.TypeID, query.Longitude, query.Latitude, query.RadiusMeters, query.Limit)
	if err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	result := make([]NearbyShop, 0, len(hits))
	if len(hits) == 0 {
		return result, nil
	}
	ids := make([]uint64, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	rows, err := g.shops.ByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint64]model.Shop, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	// Preserve Redis distance order, but return only authoritative rows from the requested category.
	for _, hit := range hits {
		if row, ok := byID[hit.ID]; ok && row.TypeID == query.TypeID {
			result = append(result, NearbyShop{ShopView: shopView(row), DistanceMeters: hit.DistanceMeters})
		}
	}
	return result, nil
}
func (g *Geo) Rebuild(ctx context.Context, typeID uint64) (int, error) {
	if typeID == 0 {
		return 0, apperror.New(apperror.Validation, nil)
	}
	rows, err := g.shops.ForType(ctx, typeID)
	if err != nil {
		return 0, err
	}
	if err := g.index.Replace(ctx, typeID, rows); err != nil {
		return 0, apperror.New(apperror.Dependency, err)
	}
	return len(rows), nil
}
func geoRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}
