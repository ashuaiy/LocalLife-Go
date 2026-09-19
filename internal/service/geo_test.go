package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type geoRepoStub struct {
	rows  []model.Shop
	ids   []uint64
	err   error
	calls int
}

func (r *geoRepoStub) ByIDs(_ context.Context, ids []uint64) ([]model.Shop, error) {
	r.calls++
	r.ids = ids
	return r.rows, r.err
}
func (r *geoRepoStub) ForType(context.Context, uint64) ([]model.Shop, error) {
	r.calls++
	return r.rows, r.err
}

type geoIndexStub struct {
	hits     []model.GeoHit
	err      error
	calls    int
	replaced []model.Shop
	typeID   uint64
}

func (s *geoIndexStub) Nearby(context.Context, uint64, float64, float64, float64, int) ([]model.GeoHit, error) {
	s.calls++
	return s.hits, s.err
}
func (s *geoIndexStub) Replace(_ context.Context, id uint64, rows []model.Shop) error {
	s.calls++
	s.typeID = id
	s.replaced = rows
	return s.err
}

func TestGeoHydrationPreservesDistanceOrderAndFiltersStaleMembers(t *testing.T) {
	repo := &geoRepoStub{rows: []model.Shop{{ID: 4, TypeID: 8, Name: "far"}, {ID: 1, TypeID: 8, Name: "near"}, {ID: 3, TypeID: 9, Name: "moved category"}}}
	index := &geoIndexStub{hits: []model.GeoHit{{ID: 1, DistanceMeters: 0}, {ID: 2, DistanceMeters: 10}, {ID: 3, DistanceMeters: 20}, {ID: 4, DistanceMeters: 100}}}
	g := NewGeo(repo, index)
	query := GeoQuery{TypeID: 8, Longitude: 121.47, Latitude: 31.23, RadiusMeters: 5000, Limit: 10}
	got, err := g.Nearby(context.Background(), query)
	if err != nil || len(got) != 2 || got[0].ID != 1 || got[1].ID != 4 || got[1].DistanceMeters != 100 || got[0].Name != "near" {
		t.Fatalf("nearby=%+v err=%v", got, err)
	}
	if len(repo.ids) != 4 {
		t.Fatalf("batch hydration IDs=%v", repo.ids)
	}
	index.hits = nil
	repo.calls = 0
	got, err = g.Nearby(context.Background(), query)
	if err != nil || got == nil || len(got) != 0 || repo.calls != 0 {
		t.Fatalf("empty nearby=%+v err=%v reads=%d", got, err, repo.calls)
	}
}

func TestGeoValidationAndFailures(t *testing.T) {
	valid := GeoQuery{TypeID: 8, Longitude: 121.47, Latitude: 31.23, RadiusMeters: 5000, Limit: 10}
	for _, change := range []func(*GeoQuery){
		func(q *GeoQuery) { q.TypeID = 0 }, func(q *GeoQuery) { q.Longitude = 181 }, func(q *GeoQuery) { q.Longitude = math.NaN() },
		func(q *GeoQuery) { q.Latitude = 86 }, func(q *GeoQuery) { q.Latitude = math.Inf(1) }, func(q *GeoQuery) { q.RadiusMeters = 0 },
		func(q *GeoQuery) { q.RadiusMeters = 50001 }, func(q *GeoQuery) { q.RadiusMeters = math.NaN() }, func(q *GeoQuery) { q.Limit = 0 }, func(q *GeoQuery) { q.Limit = 51 },
	} {
		q := valid
		change(&q)
		index := &geoIndexStub{}
		repo := &geoRepoStub{}
		_, err := NewGeo(repo, index).Nearby(context.Background(), q)
		expectStatus(t, err, 400)
		if index.calls != 0 || repo.calls != 0 {
			t.Fatal("invalid query reached dependencies")
		}
	}
	index := &geoIndexStub{err: errors.New("redis down")}
	repo := &geoRepoStub{}
	g := NewGeo(repo, index)
	_, err := g.Nearby(context.Background(), valid)
	expectStatus(t, err, 503)
	if repo.calls != 0 {
		t.Fatal("index failure must not return unsorted DB fallback")
	}
	index.err = nil
	index.hits = []model.GeoHit{{ID: 1, DistanceMeters: 10}}
	repo.err = apperror.New(apperror.Dependency, nil)
	_, err = g.Nearby(context.Background(), valid)
	expectStatus(t, err, 503)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = g.Nearby(ctx, valid)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestGeoRebuildFromAuthoritativeShops(t *testing.T) {
	repo := &geoRepoStub{rows: []model.Shop{{ID: 1, TypeID: 8}}}
	index := &geoIndexStub{}
	g := NewGeo(repo, index)
	n, err := g.Rebuild(context.Background(), 8)
	if err != nil || n != 1 || index.typeID != 8 || len(index.replaced) != 1 {
		t.Fatalf("rebuild=%d err=%v index=%+v", n, err, index)
	}
	_, err = g.Rebuild(context.Background(), 0)
	expectStatus(t, err, 400)
	repo.err = apperror.New(apperror.Dependency, nil)
	index.calls = 0
	_, err = g.Rebuild(context.Background(), 8)
	expectStatus(t, err, 503)
	if index.calls != 0 {
		t.Fatal("DB failure replaced existing index")
	}
	repo.err = nil
	index.err = errors.New("redis down")
	_, err = g.Rebuild(context.Background(), 8)
	expectStatus(t, err, 503)
}
