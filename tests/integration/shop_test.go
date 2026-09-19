package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/redis/go-redis/v9"
)

func shopFixtures(t testing.TB, deps *platform.Connections, ctx context.Context) ([]model.ShopType, []model.Shop) {
	t.Helper()
	types := []model.ShopType{{Name: "shop-test-A-" + rand.Text(), Icon: "a.svg", SortOrder: 2}, {Name: "shop-test-B-" + rand.Text(), SortOrder: 1}}
	if err := deps.DB.WithContext(ctx).Create(&types).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deps.DB.WithContext(clean).Where("type_id IN ?", []uint64{types[0].ID, types[1].ID}).Delete(&model.Shop{}).Error; err != nil {
			t.Error(err)
		}
		if err := deps.DB.WithContext(clean).Delete(&types).Error; err != nil {
			t.Error(err)
		}
	})
	rows := []model.Shop{
		{TypeID: types[0].ID, Name: "商户一", Address: "示例街 1 号", Longitude: 121.4737012, Latitude: 31.2304001},
		{TypeID: types[0].ID, Name: "商户二", Longitude: 121.48, Latitude: 31.23},
		{TypeID: types[0].ID, Name: "商户三", Longitude: 121.49, Latitude: 31.23},
		{TypeID: types[1].ID, Name: "其他分类商户", Longitude: 121.5, Latitude: 31.24},
	}
	if err := deps.DB.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, row := range rows {
			if err := deps.Redis.Del(clean, shopTestKey(row.ID)).Err(); err != nil {
				t.Error(err)
			}
		}
	})
	return types, rows
}
func shopTestKey(id uint64) string { return fmt.Sprintf("locallife:shop:v2:%d", id) }

func TestShopQueriesAndHTTP(t *testing.T) {
	deps, ctx := authDependencies(t)
	types, rows := shopFixtures(t, deps, ctx)
	repo := repository.NewShop(deps.DB)
	gotTypes, err := repo.Types(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var own []uint64
	for _, row := range gotTypes {
		if row.ID == types[0].ID || row.ID == types[1].ID {
			own = append(own, row.ID)
		}
	}
	if len(own) != 2 || own[0] != types[1].ID || own[1] != types[0].ID {
		t.Fatalf("type ordering=%v", own)
	}
	s := service.NewShop(repo, cache.NewShop(deps.Redis))
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	first, err := s.List(ctx, types[0].ID, 1, 2)
	if err != nil || len(first.Items) != 2 || !first.HasMore || first.Items[0].ID != rows[0].ID || first.Items[1].ID != rows[1].ID {
		t.Fatalf("page 1=%+v err=%v", first, err)
	}
	second, err := s.List(ctx, types[0].ID, 2, 2)
	if err != nil || len(second.Items) != 1 || second.HasMore || second.Items[0].ID != rows[2].ID {
		t.Fatalf("page 2=%+v err=%v", second, err)
	}
	empty, err := s.List(ctx, types[1].ID, 2, 10)
	if err != nil || len(empty.Items) != 0 || empty.Items == nil || empty.HasMore {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	all, err := s.List(ctx, 0, 1, 50)
	if err != nil || len(all.Items) < 4 {
		t.Fatalf("all types=%+v err=%v", all, err)
	}
	for i := 1; i < len(all.Items); i++ {
		if all.Items[i-1].ID >= all.Items[i].ID {
			t.Fatal("unstable shop ordering")
		}
	}
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewShop(s).Register(h)
	for _, path := range []string{"/api/v1/shop-types", fmt.Sprintf("/api/v1/shops?type_id=%d", types[0].ID), fmt.Sprintf("/api/v1/shops/%d", rows[0].ID)} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil)
		if res.Code != 200 {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
		var body struct {
			Code      string
			RequestID string `json:"request_id"`
			Data      json.RawMessage
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Code != "ok" || body.RequestID == "" || len(body.Data) == 0 {
			t.Fatalf("invalid envelope: %s err=%v", res.Body, err)
		}
	}
}

func TestShopCacheAsideRealDependencies(t *testing.T) {
	deps, ctx := authDependencies(t)
	_, rows := shopFixtures(t, deps, ctx)
	row := rows[0]
	store := cache.NewShop(deps.Redis)
	s := service.NewShop(repository.NewShop(deps.DB), store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if snapshot, err := store.Load(ctx, row.ID); err != nil || snapshot.Entry != nil {
		t.Fatalf("initial miss: snapshot=%+v err=%v", snapshot, err)
	}
	got, err := s.Detail(ctx, row.ID)
	if err != nil || got.Name != row.Name || got.Longitude != row.Longitude || got.Latitude != row.Latitude {
		t.Fatalf("DB detail=%+v err=%v", got, err)
	}
	ttl, err := deps.Redis.PTTL(ctx, shopTestKey(row.ID)).Result()
	if err != nil || ttl < 34*time.Minute || ttl > 40*time.Minute {
		t.Fatalf("fill TTL=%v err=%v", ttl, err)
	}
	if err := deps.DB.WithContext(ctx).Model(&model.Shop{}).Where("id = ?", row.ID).Update("name", "更新后商户").Error; err != nil {
		t.Fatal(err)
	}
	got, err = s.Detail(ctx, row.ID)
	if err != nil || got.Name != row.Name {
		t.Fatalf("cached detail=%+v err=%v", got, err)
	}
	if err := deps.Redis.Del(ctx, shopTestKey(row.ID)).Err(); err != nil {
		t.Fatal(err)
	}
	got, err = s.Detail(ctx, row.ID)
	if err != nil || got.Name != "更新后商户" {
		t.Fatalf("refill=%+v err=%v", got, err)
	}
	for _, bad := range []string{`{broken`, `null`, `{}`, fmt.Sprintf(`{"ID":%d,"TypeID":%d,"Name":"wrong shop"}`, rows[1].ID, row.TypeID)} {
		if err := deps.Redis.Set(ctx, shopTestKey(row.ID), bad, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		got, err = s.Detail(ctx, row.ID)
		if err != nil || got.Name != "更新后商户" {
			t.Fatalf("corrupt cache fallback=%+v err=%v", got, err)
		}
		cached, err := store.Load(ctx, row.ID)
		if err != nil || cached.Entry == nil || cached.Entry.Value == nil || cached.Entry.Value.Name != "更新后商户" {
			t.Fatalf("corrupt cache not repaired: %+v %v", cached, err)
		}
	}
	snapshot, err := store.Load(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(40 * time.Millisecond)
	if _, err := store.CompareAndSet(ctx, row.ID, snapshot.Token, model.ShopCacheEntry{ID: row.ID, Value: &row, RefreshAfter: expires, ExpiresAt: expires}, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := store.Load(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Entry == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, err = s.Detail(ctx, row.ID)
	if err != nil || got.Name != "更新后商户" {
		t.Fatalf("expired refill=%+v err=%v", got, err)
	}
	if _, err := store.CompareAndSet(ctx, row.ID, "", model.ShopCacheEntry{ID: row.ID, Value: &row, RefreshAfter: expires, ExpiresAt: expires}, 0); err == nil {
		t.Fatal("unbounded TTL accepted")
	}
	if err := deps.DB.WithContext(ctx).Delete(&rows[2]).Error; err != nil {
		t.Fatal(err)
	}
	_, err = s.Detail(ctx, rows[2].ID)
	status, _, _ := apperror.Describe(err)
	if status != 404 {
		t.Fatalf("missing shop: %v", err)
	}
	if n, err := deps.Redis.Exists(ctx, shopTestKey(rows[2].ID)).Result(); err != nil || n != 1 {
		t.Fatalf("missing shop should have a negative entry: %d %v", n, err)
	}
	closed := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	fallback := service.NewShop(repository.NewShop(deps.DB), cache.NewShop(closed))
	t.Cleanup(func() { _ = fallback.Close(context.Background()) })
	got, err = fallback.Detail(ctx, row.ID)
	if err != nil || got.Name != "更新后商户" {
		t.Fatalf("Redis unavailable fallback=%+v err=%v", got, err)
	}
}
