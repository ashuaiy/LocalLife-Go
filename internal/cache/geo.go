package cache

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/redis/go-redis/v9"
)

type Geo struct{ redis *redis.Client }

func NewGeo(client *redis.Client) *Geo { return &Geo{redis: client} }

//go:embed lua/geo_publish.lua
var geoPublishLua string
var geoPublish = redis.NewScript(geoPublishLua)

func geoKeys(typeID uint64) (string, string) {
	prefix := "locallife:geo:{" + strconv.FormatUint(typeID, 10) + "}"
	return prefix + ":index", prefix + ":ready"
}

func (g *Geo) Nearby(ctx context.Context, typeID uint64, lng, lat, radius float64, limit int) ([]model.GeoHit, error) {
	key, ready := geoKeys(typeID)
	// Read marker and index in one Redis transaction so a concurrent publication cannot split the snapshot.
	pipe := g.redis.TxPipeline()
	marker := pipe.Get(ctx, ready)
	exists := pipe.Exists(ctx, key)
	locations := pipe.GeoSearchLocation(ctx, key, &redis.GeoSearchLocationQuery{GeoSearchQuery: redis.GeoSearchQuery{Longitude: lng, Latitude: lat, Radius: radius, RadiusUnit: "m", Sort: "ASC", Count: limit}, WithDist: true})
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	count, err := strconv.ParseUint(marker.Val(), 10, 64)
	if err != nil || (count > 0 && exists.Val() != 1) || (count == 0 && exists.Val() != 0) {
		return nil, errors.New("GEO index is missing or inconsistent; rebuild required")
	}
	hits := make([]model.GeoHit, 0, len(locations.Val()))
	for _, location := range locations.Val() {
		id, err := strconv.ParseUint(location.Name, 10, 64)
		if err != nil || id == 0 || math.IsNaN(location.Dist) || math.IsInf(location.Dist, 0) || location.Dist < 0 {
			return nil, errors.New("invalid GEO member")
		}
		hits = append(hits, model.GeoHit{ID: id, DistanceMeters: location.Dist})
	}
	return hits, nil
}

func (g *Geo) Replace(ctx context.Context, typeID uint64, rows []model.Shop) error {
	if typeID == 0 {
		return errors.New("invalid GEO category")
	}
	for _, row := range rows {
		if row.ID == 0 || row.TypeID != typeID || !validGeoCoordinate(row.Longitude, -180, 180) || !validGeoCoordinate(row.Latitude, -85.05112878, 85.05112878) {
			return errors.New("shop has invalid GEO category, ID or coordinates")
		}
	}
	key, ready := geoKeys(typeID)
	temporary := key + ":build:" + rand.Text()
	// A crashed builder leaves only an expiring temporary key; cancellation also gets bounded cleanup.
	defer func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = g.redis.Del(clean, temporary).Err()
	}()
	for start := 0; start < len(rows); start += 500 {
		end := min(start+500, len(rows))
		points := make([]*redis.GeoLocation, 0, end-start)
		for _, row := range rows[start:end] {
			points = append(points, &redis.GeoLocation{Name: strconv.FormatUint(row.ID, 10), Longitude: row.Longitude, Latitude: row.Latitude})
		}
		if _, err := g.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.GeoAdd(ctx, temporary, points...)
			pipe.Expire(ctx, temporary, 10*time.Minute)
			return nil
		}); err != nil {
			return err
		}
	}
	return geoPublish.Run(ctx, g.redis, []string{key, temporary, ready}, len(rows)).Err()
}
func validGeoCoordinate(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}
