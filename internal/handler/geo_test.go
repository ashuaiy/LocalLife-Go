package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type geoHTTPStub struct {
	query service.GeoQuery
	err   error
	calls int
}

func (s *geoHTTPStub) Nearby(_ context.Context, q service.GeoQuery) ([]service.NearbyShop, error) {
	s.query = q
	s.calls++
	return []service.NearbyShop{{ShopView: service.ShopView{ID: 7, TypeID: q.TypeID}, DistanceMeters: 0}}, s.err
}
func TestGeoHTTPContract(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	s := &geoHTTPStub{}
	NewShop(&shopHTTPStub{t: t}).Register(h)
	NewGeo(s).Register(h)
	base := "/api/v1/shops/nearby?type_id=8&longitude=121.47&latitude=31.23"
	res := ut.PerformRequest(h.Engine, "GET", base, nil)
	if res.Code != 200 || s.query.TypeID != 8 || s.query.Longitude != 121.47 || s.query.Latitude != 31.23 || s.query.RadiusMeters != 5000 || s.query.Limit != 10 {
		t.Fatalf("default query=%+v HTTP=%d %s", s.query, res.Code, res.Body)
	}
	var body struct {
		Data []struct {
			ID             string
			DistanceMeters float64 `json:"distance_m"`
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].ID != "7" || body.Data[0].DistanceMeters != 0 {
		t.Fatalf("body=%s err=%v", res.Body, err)
	}
	res = ut.PerformRequest(h.Engine, "GET", base+"&radius_m=1000&limit=5", nil)
	if res.Code != 200 || s.query.RadiusMeters != 1000 || s.query.Limit != 5 {
		t.Fatalf("custom query=%+v HTTP=%d", s.query, res.Code)
	}
	before := s.calls
	for _, query := range []string{
		"type_id=8&latitude=31", "type_id=8&longitude=121", "longitude=121&latitude=31",
		"type_id=0&longitude=121&latitude=31", "type_id=8&longitude=NaN&latitude=31", "type_id=8&longitude=Inf&latitude=31",
		"type_id=8&longitude=181&latitude=31", "type_id=8&longitude=121&latitude=86", "type_id=8&longitude=121&latitude=",
		"type_id=8&longitude=121&latitude=31&radius_m=0", "type_id=8&longitude=121&latitude=31&radius_m=50001",
		"type_id=8&longitude=121&latitude=31&limit=51", "type_id=8&longitude=121&latitude=31&limit=0",
		"type_id=8&longitude=121&latitude=31&latitude=32", "type_id=8&longitude=121&latitude=31&radius_m=",
	} {
		res := ut.PerformRequest(h.Engine, "GET", "/api/v1/shops/nearby?"+query, nil)
		if res.Code != 400 {
			t.Fatalf("%s => %d %s", query, res.Code, res.Body)
		}
	}
	if s.calls != before {
		t.Fatal("invalid query reached service")
	}
	s.err = apperror.New(apperror.Dependency, nil)
	res = ut.PerformRequest(h.Engine, "GET", base, nil)
	if res.Code != 503 {
		t.Fatalf("dependency status=%d", res.Code)
	}
}
