package service

import (
	"context"
	"sync"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type Shops interface {
	Types(context.Context) ([]model.ShopType, error)
	List(ctx context.Context, typeID uint64, offset, limit int) ([]model.Shop, error)
	ByID(context.Context, uint64) (model.Shop, error)
}

type ShopStore interface {
	Load(context.Context, uint64) (model.ShopSnapshot, error)
	CompareAndSet(context.Context, uint64, string, model.ShopCacheEntry, time.Duration) (bool, error)
	Invalidate(context.Context, uint64) error
}

type Shop struct {
	shops      Shops
	store      ShopStore
	mu         sync.Mutex
	inflight   map[uint64]*shopRefresh
	maxRefresh int
	background context.Context
	stop       context.CancelFunc
	closed     bool
	drained    chan struct{}
}

type shopRefresh struct {
	done chan struct{}
	row  model.Shop
	err  error
}

func NewShop(shops Shops, store ShopStore) *Shop {
	ctx, cancel := context.WithCancel(context.Background())
	return &Shop{shops: shops, store: store, inflight: make(map[uint64]*shopRefresh), maxRefresh: 64, background: ctx, stop: cancel, drained: make(chan struct{})}
}

type ShopTypeView struct {
	ID   uint64 `json:"id,string"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}
type ShopView struct {
	ID        uint64  `json:"id,string"`
	TypeID    uint64  `json:"type_id,string"`
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}
type ShopPage struct {
	Items    []ShopView `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	HasMore  bool       `json:"has_more"`
}

func (s *Shop) Types(ctx context.Context) ([]ShopTypeView, error) {
	rows, err := s.shops.Types(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ShopTypeView, 0, len(rows))
	for _, row := range rows {
		items = append(items, ShopTypeView{ID: row.ID, Name: row.Name, Icon: row.Icon})
	}
	return items, nil
}

func (s *Shop) List(ctx context.Context, typeID uint64, page, pageSize int) (ShopPage, error) {
	if page < 1 || page > 1000 || pageSize < 1 || pageSize > 50 {
		return ShopPage{}, apperror.New(apperror.Validation, nil)
	}
	rows, err := s.shops.List(ctx, typeID, (page-1)*pageSize, pageSize+1)
	if err != nil {
		return ShopPage{}, err
	}
	result := ShopPage{Items: make([]ShopView, 0, pageSize), Page: page, PageSize: pageSize, HasMore: len(rows) > pageSize}
	if result.HasMore {
		rows = rows[:pageSize]
	}
	for _, row := range rows {
		result.Items = append(result.Items, shopView(row))
	}
	return result, nil
}

func (s *Shop) Detail(ctx context.Context, id uint64) (ShopView, error) {
	if id == 0 {
		return ShopView{}, apperror.New(apperror.Validation, nil)
	}
	if err := ctx.Err(); err != nil {
		return ShopView{}, err
	}
	snapshot, err := s.loadCache(ctx, id)
	if ctx.Err() != nil {
		return ShopView{}, ctx.Err()
	}
	now := time.Now()
	if err == nil && snapshot.Entry != nil && now.Before(snapshot.Entry.ExpiresAt) {
		entry := snapshot.Entry
		if entry.Value != nil {
			if !now.Before(entry.RefreshAfter) {
				_, _ = s.refresh(id)
			}
			return shopView(*entry.Value), nil
		}
		if now.Before(entry.RefreshAfter) {
			return ShopView{}, apperror.New(apperror.NotFound, nil)
		}
	}
	call, err := s.refresh(id)
	if err != nil {
		return ShopView{}, err
	}
	select {
	case <-ctx.Done():
		return ShopView{}, ctx.Err()
	case <-call.done:
		if ctx.Err() != nil {
			return ShopView{}, ctx.Err()
		}
		if call.err != nil {
			return ShopView{}, call.err
		}
		return shopView(call.row), nil
	}
}

func shopView(row model.Shop) ShopView {
	return ShopView{ID: row.ID, TypeID: row.TypeID, Name: row.Name, Address: row.Address, Longitude: row.Longitude, Latitude: row.Latitude}
}
