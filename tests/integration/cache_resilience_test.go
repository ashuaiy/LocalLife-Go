package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

func TestShopStaleRefreshAndPublicationRealRedis(t *testing.T) {
	deps, ctx := authDependencies(t)
	_, shops := shopFixtures(t, deps, ctx)
	store := cache.NewShop(deps.Redis)
	row := shops[0]
	old := model.ShopCacheEntry{ID: row.ID, Value: &row, RefreshAfter: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Minute)}
	if ok, err := store.CompareAndSet(ctx, row.ID, "", old, time.Minute); err != nil || !ok {
		t.Fatalf("seed=%t %v", ok, err)
	}
	if err := deps.DB.WithContext(ctx).Model(&model.Shop{}).Where("id = ?", row.ID).Update("name", "后台刷新后").Error; err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	repo := &evidenceShopRepository{Shop: repository.NewShop(deps.DB), gate: gate, entered: make(chan struct{}, 1)}
	s := service.NewShop(repo, store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	for range 100 {
		got, err := s.Detail(ctx, row.ID)
		if err != nil || got.Name != row.Name {
			t.Fatalf("stale response=%+v err=%v", got, err)
		}
	}
	select {
	case <-repo.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if repo.reads.Load() != 1 {
		t.Fatalf("rebuild DB reads=%d", repo.reads.Load())
	}
	close(gate)
	deadline := time.Now().Add(2 * time.Second)
	var snapshot model.ShopSnapshot
	for {
		var err error
		snapshot, err = store.Load(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Entry != nil && snapshot.Entry.Value.Name == "后台刷新后" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not publish")
		}
		time.Sleep(time.Millisecond)
	}
	ttl, err := deps.Redis.PTTL(ctx, shopTestKey(row.ID)).Result()
	if err != nil || ttl < 34*time.Minute || ttl > 40*time.Minute || snapshot.Entry.ExpiresAt.Sub(snapshot.Entry.RefreshAfter) != 5*time.Minute {
		t.Fatalf("ttl=%v record=%+v err=%v", ttl, snapshot.Entry, err)
	}
	if err := store.Invalidate(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.CompareAndSet(ctx, row.ID, snapshot.Token, *snapshot.Entry, time.Minute); err != nil || ok {
		t.Fatalf("invalidated refresh was published: %t %v", ok, err)
	}
	if ok, err := store.CompareAndSet(ctx, row.ID, "", old, time.Minute); err != nil || !ok {
		t.Fatalf("first publisher=%t %v", ok, err)
	}
	if ok, err := store.CompareAndSet(ctx, row.ID, "", *snapshot.Entry, time.Minute); err != nil || ok {
		t.Fatalf("second publisher overwrote the first=%t %v", ok, err)
	}
	// A physically present but hard-expired record must never be served as stale.
	_ = store.Invalidate(ctx, row.ID)
	old.RefreshAfter = time.Now().Add(-time.Hour)
	old.ExpiresAt = time.Now().Add(-time.Second)
	if ok, err := store.CompareAndSet(ctx, row.ID, "", old, time.Minute); err != nil || !ok {
		t.Fatal(err)
	}
	got, err := s.Detail(ctx, row.ID)
	if err != nil || got.Name != "后台刷新后" {
		t.Fatalf("hard-expired=%+v err=%v", got, err)
	}
}

func TestShopDeletedRowRefreshesToNegative(t *testing.T) {
	deps, ctx := authDependencies(t)
	_, shops := shopFixtures(t, deps, ctx)
	row := shops[0]
	store := cache.NewShop(deps.Redis)
	old := model.ShopCacheEntry{ID: row.ID, Value: &row, RefreshAfter: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := store.CompareAndSet(ctx, row.ID, "", old, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := deps.DB.WithContext(ctx).Delete(&row).Error; err != nil {
		t.Fatal(err)
	}
	repo := &evidenceShopRepository{Shop: repository.NewShop(deps.DB)}
	s := service.NewShop(repo, store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if _, err := s.Detail(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := store.Load(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Entry != nil && snapshot.Entry.Value == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing row not cached")
		}
		time.Sleep(time.Millisecond)
	}
	_, err := s.Detail(ctx, row.ID)
	status, _, _ := apperror.Describe(err)
	if status != 404 || repo.reads.Load() != 1 {
		t.Fatalf("negative read=%v reads=%d", err, repo.reads.Load())
	}
	ttl, err := deps.Redis.PTTL(ctx, shopTestKey(row.ID)).Result()
	if err != nil || ttl < 29*time.Second || ttl > 45*time.Second {
		t.Fatalf("negative TTL=%v err=%v", ttl, err)
	}
}

func TestShopMaintenanceInvalidatesRealCache(t *testing.T) {
	deps, ctx := authDependencies(t)
	_, shops := shopFixtures(t, deps, ctx)
	repo := repository.NewShop(deps.DB)
	store := cache.NewShop(deps.Redis)
	s := service.NewShop(repo, store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if _, err := s.Detail(ctx, shops[0].ID); err != nil {
		t.Fatal(err)
	}
	name, address := "维护更新", ""
	editor := service.NewShopMaintenance(repo, store)
	got, err := editor.Update(ctx, shops[0].ID, model.ShopTextEdit{Name: &name, Address: &address})
	if err != nil || got.Name != name || got.Address != "" || got.Longitude != shops[0].Longitude || got.TypeID != shops[0].TypeID {
		t.Fatalf("update=%+v err=%v", got, err)
	}
	snapshot, err := store.Load(ctx, shops[0].ID)
	if err != nil || snapshot.Entry != nil {
		t.Fatalf("cache not invalidated: %+v %v", snapshot, err)
	}
	got, err = s.Detail(ctx, shops[0].ID)
	if err != nil || got.Name != name || got.Address != "" {
		t.Fatalf("refill=%+v err=%v", got, err)
	}
	if _, err := editor.Update(ctx, shops[0].ID, model.ShopTextEdit{Name: &name}); err != nil {
		t.Fatal("same-value update should succeed", err)
	}
	_, err = editor.Update(ctx, ^uint64(0), model.ShopTextEdit{Name: &name})
	status, _, _ := apperror.Describe(err)
	if status != 404 {
		t.Fatalf("missing update=%v", err)
	}
}
