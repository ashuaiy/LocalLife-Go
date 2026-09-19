package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type resilientShopRepository struct {
	Shops
	reads    atomic.Int64
	row      model.Shop
	err      error
	gate     <-chan struct{}
	entered  chan struct{}
	finished chan struct{}
}

func (r *resilientShopRepository) ByID(ctx context.Context, id uint64) (model.Shop, error) {
	if r.finished != nil {
		defer func() { r.finished <- struct{}{} }()
	}
	r.reads.Add(1)
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return model.Shop{}, ctx.Err()
		}
	}
	return r.row, r.err
}

type memoryShopStore struct {
	mu            sync.Mutex
	data          map[uint64]model.ShopSnapshot
	version       int
	err, writeErr error
	ttl           time.Duration
}

func newMemoryShopStore() *memoryShopStore {
	return &memoryShopStore{data: make(map[uint64]model.ShopSnapshot)}
}
func (s *memoryShopStore) Load(_ context.Context, id uint64) (model.ShopSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[id], s.err
}
func (s *memoryShopStore) CompareAndSet(ctx context.Context, id uint64, expected string, entry model.ShopCacheEntry, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return false, s.writeErr
	}
	if s.data[id].Token != expected {
		return false, nil
	}
	s.version++
	s.data[id] = model.ShopSnapshot{Entry: &entry, Token: fmt.Sprint(s.version)}
	s.ttl = ttl
	return true, nil
}
func (s *memoryShopStore) Invalidate(_ context.Context, id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}
func testShopService(t *testing.T, r Shops, s ShopStore) *Shop {
	t.Helper()
	v := NewShop(r, s)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := v.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return v
}
func cacheTestEntry(id uint64, name string, refresh, expiry time.Time) model.ShopCacheEntry {
	return model.ShopCacheEntry{ID: id, Value: &model.Shop{ID: id, TypeID: 3, Name: name}, RefreshAfter: refresh, ExpiresAt: expiry}
}
func seedMemoryShop(s *memoryShopStore, entry model.ShopCacheEntry) {
	_, _ = s.CompareAndSet(context.Background(), entry.ID, "", entry, time.Until(entry.ExpiresAt))
}
func awaitShopCondition(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShopNegativeCacheAndTransientFailures(t *testing.T) {
	store := newMemoryShopStore()
	repo := &resilientShopRepository{err: apperror.New(apperror.NotFound, nil)}
	s := testShopService(t, repo, store)
	for range 2 {
		_, err := s.Detail(context.Background(), 7)
		expectStatus(t, err, 404)
	}
	snap, _ := store.Load(context.Background(), 7)
	if repo.reads.Load() != 1 || snap.Entry == nil || snap.Entry.Value != nil || store.ttl < 30*time.Second || store.ttl > 45*time.Second {
		t.Fatalf("reads=%d snapshot=%+v ttl=%s", repo.reads.Load(), snap, store.ttl)
	}
	failed := newMemoryShopStore()
	badRepo := &resilientShopRepository{err: apperror.New(apperror.Dependency, nil)}
	bad := testShopService(t, badRepo, failed)
	_, err := bad.Detail(context.Background(), 8)
	expectStatus(t, err, 503)
	snap, _ = failed.Load(context.Background(), 8)
	if snap.Entry != nil {
		t.Fatal("cached a database failure as missing")
	}
}
func TestShopColdSingleflightAndIndependentCancellation(t *testing.T) {
	gate := make(chan struct{})
	repo := &resilientShopRepository{row: model.Shop{ID: 7, TypeID: 3, Name: "fresh"}, gate: gate, entered: make(chan struct{}, 1)}
	store := newMemoryShopStore()
	s := testShopService(t, repo, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.Detail(ctx, 7); first <- err }()
	<-repo.entered
	second := make(chan error, 1)
	go func() {
		row, err := s.Detail(context.Background(), 7)
		if err == nil && row.Name != "fresh" {
			err = errors.New("bad row")
		}
		second <- err
	}()
	cancel()
	expectStatus(t, <-first, 408)
	close(gate)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if repo.reads.Load() != 1 {
		t.Fatalf("DB reads=%d", repo.reads.Load())
	}
	snap, _ := store.Load(context.Background(), 7)
	freshTTL := time.Until(snap.Entry.RefreshAfter)
	if freshTTL < 29*time.Minute || freshTTL > 35*time.Minute || snap.Entry.ExpiresAt.Sub(snap.Entry.RefreshAfter) != 5*time.Minute {
		t.Fatalf("record=%+v", snap.Entry)
	}
}
func TestShopStaleReadsRebuildOnceAndCASInvalidation(t *testing.T) {
	for _, invalidate := range []bool{false, true} {
		t.Run(fmt.Sprint(invalidate), func(t *testing.T) {
			gate := make(chan struct{})
			repo := &resilientShopRepository{row: model.Shop{ID: 7, TypeID: 3, Name: "new"}, gate: gate, entered: make(chan struct{}, 1)}
			store := newMemoryShopStore()
			seedMemoryShop(store, cacheTestEntry(7, "old", time.Now().Add(-time.Minute), time.Now().Add(time.Minute)))
			s := testShopService(t, repo, store)
			for range 100 {
				row, err := s.Detail(context.Background(), 7)
				if err != nil || row.Name != "old" {
					t.Fatalf("stale=%+v err=%v", row, err)
				}
			}
			<-repo.entered
			if repo.reads.Load() != 1 {
				t.Fatalf("refresh count=%d", repo.reads.Load())
			}
			if invalidate {
				_ = store.Invalidate(context.Background(), 7)
			}
			close(gate)
			awaitShopCondition(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.inflight) == 0 })
			snap, _ := store.Load(context.Background(), 7)
			if invalidate {
				if snap.Entry != nil {
					t.Fatal("old refresh resurrected invalidated cache")
				}
			} else if snap.Entry == nil || snap.Entry.Value.Name != "new" {
				t.Fatalf("not refreshed=%+v", snap)
			}
		})
	}
}
func TestShopRefreshFailureBackoffAndHardExpiry(t *testing.T) {
	store := newMemoryShopStore()
	expires := time.Now().Add(time.Minute)
	seedMemoryShop(store, cacheTestEntry(7, "old", time.Now().Add(-time.Minute), expires))
	repo := &resilientShopRepository{err: apperror.New(apperror.Dependency, nil)}
	s := testShopService(t, repo, store)
	row, err := s.Detail(context.Background(), 7)
	if err != nil || row.Name != "old" {
		t.Fatal("stale read failed")
	}
	awaitShopCondition(t, func() bool {
		snap, _ := store.Load(context.Background(), 7)
		return snap.Entry.RefreshAfter.After(time.Now())
	})
	for range 100 {
		_, err := s.Detail(context.Background(), 7)
		if err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := store.Load(context.Background(), 7)
	if repo.reads.Load() != 1 || !snap.Entry.ExpiresAt.Equal(expires) {
		t.Fatalf("reads=%d expiry=%v", repo.reads.Load(), snap.Entry.ExpiresAt)
	}
	_ = store.Invalidate(context.Background(), 7)
	seedMemoryShop(store, cacheTestEntry(7, "too old", time.Now().Add(-time.Hour), time.Now().Add(-time.Second)))
	_, err = s.Detail(context.Background(), 7)
	expectStatus(t, err, 503)
}
func TestShopRefreshTimeoutBackoff(t *testing.T) {
	store := newMemoryShopStore()
	expires := time.Now().Add(time.Minute)
	seedMemoryShop(store, cacheTestEntry(7, "old", time.Now().Add(-time.Minute), expires))
	repo := &resilientShopRepository{gate: make(chan struct{}), finished: make(chan struct{}, 1)}
	s := testShopService(t, repo, store)
	row, err := s.Detail(context.Background(), 7)
	if err != nil || row.Name != "old" {
		t.Fatalf("stale=%+v err=%v", row, err)
	}
	select {
	case <-repo.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("database read did not reach its deadline")
	}
	awaitShopCondition(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.inflight) == 0 })
	snap, _ := store.Load(context.Background(), 7)
	if !snap.Entry.RefreshAfter.After(time.Now()) || !snap.Entry.ExpiresAt.Equal(expires) {
		t.Fatalf("timeout must postpone retry without extending hard expiry: %+v", snap.Entry)
	}
	for range 100 {
		if _, err := s.Detail(context.Background(), 7); err != nil {
			t.Fatal(err)
		}
	}
	if repo.reads.Load() != 1 {
		t.Fatalf("timeout retried immediately: reads=%d", repo.reads.Load())
	}
}

func TestShopCacheFailureCapacityAndClose(t *testing.T) {
	store := newMemoryShopStore()
	store.err = errors.New("redis down")
	store.writeErr = store.err
	repo := &resilientShopRepository{row: model.Shop{ID: 7, TypeID: 3}}
	s := testShopService(t, repo, store)
	if _, err := s.Detail(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	blocking := &resilientShopRepository{row: repo.row, gate: gate, entered: make(chan struct{}, 1)}
	limited := testShopService(t, blocking, newMemoryShopStore())
	limited.maxRefresh = 1
	done := make(chan error, 1)
	go func() { _, err := limited.Detail(context.Background(), 7); done <- err }()
	<-blocking.entered
	_, err := limited.Detail(context.Background(), 8)
	expectStatus(t, err, 429)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := limited.Close(ctx); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, <-done, 408)
	_, err = limited.Detail(context.Background(), 9)
	expectStatus(t, err, 503)
}
