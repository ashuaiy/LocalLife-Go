package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

// Doubles isolate orchestration; SQL queries and Redis TTL/encoding use real dependencies in integration tests.
type shopRepoStub struct {
	rows          []model.Shop
	err           error
	reads         int
	typeID        uint64
	offset, limit int
}

func (r *shopRepoStub) Types(context.Context) ([]model.ShopType, error) { return nil, r.err }
func (r *shopRepoStub) List(_ context.Context, typeID uint64, offset, limit int) ([]model.Shop, error) {
	r.typeID, r.offset, r.limit = typeID, offset, limit
	return r.rows, r.err
}
func (r *shopRepoStub) ByID(context.Context, uint64) (model.Shop, error) {
	r.reads++
	if r.err != nil {
		return model.Shop{}, r.err
	}
	return r.rows[0], nil
}

type shopStoreStub struct {
	row            model.Shop
	found          bool
	getErr, setErr error
	writes         int
	ttl            time.Duration
	afterGet       func()
}

func (s *shopStoreStub) Load(ctx context.Context, id uint64) (model.ShopSnapshot, error) {
	row, found, err := s.Get(ctx, id)
	if !found {
		return model.ShopSnapshot{}, err
	}
	return model.ShopSnapshot{Entry: &model.ShopCacheEntry{ID: id, Value: &row, RefreshAfter: time.Now().Add(time.Hour), ExpiresAt: time.Now().Add(2 * time.Hour)}}, err
}
func (s *shopStoreStub) CompareAndSet(ctx context.Context, id uint64, token string, entry model.ShopCacheEntry, ttl time.Duration) (bool, error) {
	if entry.Value == nil {
		return false, nil
	}
	return true, s.Set(ctx, *entry.Value, ttl)
}
func (s *shopStoreStub) Invalidate(context.Context, uint64) error { return nil }

func (s *shopStoreStub) Get(context.Context, uint64) (model.Shop, bool, error) {
	if s.afterGet != nil {
		s.afterGet()
	}
	return s.row, s.found, s.getErr
}
func (s *shopStoreStub) Set(_ context.Context, row model.Shop, ttl time.Duration) error {
	s.row, s.ttl = row, ttl
	s.writes++
	return s.setErr
}

func TestShopDetailCacheAside(t *testing.T) {
	row := model.Shop{ID: 7, TypeID: 3, Name: "商户", Address: "测试路", Longitude: 121.47, Latitude: 31.23}
	for _, tc := range []struct {
		name           string
		hit            bool
		getErr, setErr error
		reads, writes  int
	}{
		{"hit", true, nil, nil, 0, 0},
		{"miss", false, nil, nil, 1, 1},
		{"redis read failure", false, errors.New("redis unavailable"), nil, 1, 1},
		{"redis write failure", false, nil, errors.New("redis unavailable"), 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &shopRepoStub{rows: []model.Shop{row}}
			store := &shopStoreStub{row: row, found: tc.hit, getErr: tc.getErr, setErr: tc.setErr}
			got, err := NewShop(repo, store).Detail(context.Background(), 7)
			if err != nil || got.ID != row.ID || got.Name != row.Name || got.Longitude != row.Longitude {
				t.Fatalf("detail=%+v err=%v", got, err)
			}
			if repo.reads != tc.reads || store.writes != tc.writes {
				t.Fatalf("reads=%d writes=%d", repo.reads, store.writes)
			}
			if tc.writes > 0 && (store.row.ID != row.ID || store.ttl < 34*time.Minute || store.ttl > 40*time.Minute) {
				t.Fatalf("cache fill=%+v ttl=%v", store.row, store.ttl)
			}
		})
	}
}

func TestShopDetailFailureAndCancellation(t *testing.T) {
	for _, kind := range []apperror.Kind{apperror.NotFound, apperror.Dependency} {
		repo := &shopRepoStub{err: apperror.New(kind, nil)}
		store := &shopStoreStub{}
		_, err := NewShop(repo, store).Detail(context.Background(), 7)
		status, _, _ := apperror.Describe(err)
		want, _, _ := apperror.Describe(repo.err)
		if status != want || store.writes != 0 {
			t.Fatalf("status=%d want=%d writes=%d", status, want, store.writes)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &shopRepoStub{}
	store := &shopStoreStub{afterGet: cancel}
	_, err := NewShop(repo, store).Detail(ctx, 7)
	if !errors.Is(err, context.Canceled) || repo.reads != 0 {
		t.Fatalf("canceled detail: err=%v reads=%d", err, repo.reads)
	}
	_, err = NewShop(repo, store).Detail(context.Background(), 0)
	expectStatus(t, err, 400)
}

func TestShopPaginationAndEmptyCollections(t *testing.T) {
	repo := &shopRepoStub{rows: []model.Shop{{ID: 3}, {ID: 4}, {ID: 5}}}
	s := NewShop(repo, &shopStoreStub{})
	got, err := s.List(context.Background(), 8, 2, 2)
	if err != nil || len(got.Items) != 2 || got.Items[0].ID != 3 || got.Items[1].ID != 4 || !got.HasMore || got.Page != 2 || got.PageSize != 2 {
		t.Fatalf("page=%+v err=%v", got, err)
	}
	if repo.typeID != 8 || repo.offset != 2 || repo.limit != 3 {
		t.Fatalf("query=%+v", repo)
	}
	for _, pair := range [][2]int{{0, 10}, {-1, 10}, {1001, 10}, {1, 0}, {1, 51}} {
		_, err := s.List(context.Background(), 0, pair[0], pair[1])
		expectStatus(t, err, 400)
	}
	repo.rows = nil
	got, err = s.List(context.Background(), 0, 1, 10)
	if err != nil || got.Items == nil || len(got.Items) != 0 || got.HasMore {
		t.Fatalf("empty page=%+v err=%v", got, err)
	}
	types, err := s.Types(context.Background())
	if err != nil || types == nil || len(types) != 0 {
		t.Fatalf("empty types=%+v err=%v", types, err)
	}
	repo.err = apperror.New(apperror.Dependency, nil)
	_, err = s.Types(context.Background())
	expectStatus(t, err, 503)
	_, err = s.List(context.Background(), 0, 1, 10)
	expectStatus(t, err, 503)
}
