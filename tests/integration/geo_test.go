package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/redis/go-redis/v9"
)

func geoTestKeys(typeID uint64) (string, string) {
	prefix := fmt.Sprintf("locallife:geo:{%d}", typeID)
	return prefix + ":index", prefix + ":ready"
}

func TestGeoNearbyRealDependencies(t *testing.T) {
	deps, ctx := authDependencies(t)
	types, rows := shopFixtures(t, deps, ctx)
	key, ready := geoTestKeys(types[0].ID)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deps.Redis.Del(clean, key, ready).Err(); err != nil {
			t.Error(err)
		}
	})
	index := cache.NewGeo(deps.Redis)
	repo := repository.NewShop(deps.DB)
	g := service.NewGeo(repo, index)
	q := service.GeoQuery{TypeID: types[0].ID, Longitude: rows[0].Longitude, Latitude: rows[0].Latitude, RadiusMeters: 5000, Limit: 10}
	if _, err := g.Nearby(ctx, q); err == nil {
		t.Fatal("uninitialized index must not appear as empty result")
	}
	n, err := g.Rebuild(ctx, types[0].ID)
	if err != nil || n != 3 {
		t.Fatalf("rebuild=%d err=%v", n, err)
	}
	got, err := g.Nearby(ctx, q)
	if err != nil || len(got) != 3 || got[0].ID != rows[0].ID || got[1].ID != rows[1].ID || got[2].ID != rows[2].ID {
		t.Fatalf("nearby=%+v err=%v", got, err)
	}
	if got[0].DistanceMeters > 1 || got[1].DistanceMeters <= got[0].DistanceMeters || got[2].DistanceMeters <= got[1].DistanceMeters {
		t.Fatalf("distances=%+v", got)
	}
	q.RadiusMeters = 20
	got, err = g.Nearby(ctx, q)
	if err != nil || len(got) != 1 || got[0].ID != rows[0].ID {
		t.Fatalf("small radius=%+v err=%v", got, err)
	}
	q.RadiusMeters = 5000
	q.Limit = 2
	got, err = g.Nearby(ctx, q)
	if err != nil || len(got) != 2 {
		t.Fatalf("limit=%+v err=%v", got, err)
	}
	// Entity fields come from MySQL, not the position index or the detail cache.
	if err := deps.DB.WithContext(ctx).Model(&model.Shop{}).Where("id = ?", rows[0].ID).Update("name", "current DB name").Error; err != nil {
		t.Fatal(err)
	}
	if err := deps.DB.WithContext(ctx).Delete(&rows[1]).Error; err != nil {
		t.Fatal(err)
	}
	q.Limit = 10
	got, err = g.Nearby(ctx, q)
	if err != nil || len(got) != 2 || got[0].Name != "current DB name" || got[1].ID != rows[2].ID {
		t.Fatalf("stale index hydration=%+v err=%v", got, err)
	}
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewShop(service.NewShop(repo, cache.NewShop(deps.Redis))).Register(h)
	handler.NewGeo(g).Register(h)
	res := ut.PerformRequest(h.Engine, "GET", fmt.Sprintf("/api/v1/shops/nearby?type_id=%d&longitude=%f&latitude=%f", q.TypeID, q.Longitude, q.Latitude), nil)
	if res.Code != 200 || !json.Valid(res.Body.Bytes()) || !strings.Contains(res.Body.String(), `"distance_m":`) || !strings.Contains(res.Body.String(), `current DB name`) {
		t.Fatalf("HTTP %d response=%s", res.Code, res.Body)
	}
	if err := deps.Redis.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Nearby(ctx, q); err == nil {
		t.Fatal("evicted nonempty index must not appear as empty")
	}
	if err := index.Replace(ctx, q.TypeID, nil); err != nil {
		t.Fatal(err)
	}
	got, err = g.Nearby(ctx, q)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("initialized empty index=%+v err=%v", got, err)
	}
	if ttl := deps.Redis.TTL(ctx, ready).Val(); ttl != -1 {
		t.Fatalf("published marker TTL=%v", ttl)
	}
}

func TestGeoReplacementKeepsCompleteIndex(t *testing.T) {
	deps, ctx := authDependencies(t)
	types, _ := shopFixtures(t, deps, ctx)
	typeID := types[0].ID
	key, ready := geoTestKeys(typeID)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deps.Redis.Del(clean, key, ready).Err(); err != nil {
			t.Error(err)
		}
	})
	index := cache.NewGeo(deps.Redis)
	makeRows := func(base uint64) []model.Shop {
		rows := make([]model.Shop, 501)
		for i := range rows {
			rows[i] = model.Shop{ID: base + uint64(i), TypeID: typeID, Longitude: 121.47, Latitude: 31.23}
		}
		return rows
	}
	oldRows, newRows := makeRows(10000), makeRows(20000)
	if err := index.Replace(ctx, typeID, oldRows); err != nil {
		t.Fatal(err)
	}
	bad := append([]model.Shop(nil), newRows...)
	bad[500].Latitude = 90
	if err := index.Replace(ctx, typeID, bad); err == nil {
		t.Fatal("unsupported latitude accepted")
	}
	// Simulate Redis eviction between GEOADD batches: an incomplete temporary index must never publish.
	opts := *deps.Redis.Options()
	faultClient := redis.NewClient(&opts)
	t.Cleanup(func() { _ = faultClient.Close() })
	faultClient.AddHook(&evictGeoBuildHook{client: deps.Redis})
	if err := cache.NewGeo(faultClient).Replace(ctx, typeID, newRows); err == nil {
		t.Fatal("partially evicted temporary index was published")
	}
	hits, err := index.Nearby(ctx, typeID, 121.47, 31.23, 1000, 50)
	if err != nil || len(hits) != 50 || hits[0].ID >= 20000 {
		t.Fatalf("failed replacement changed old index: %v %v", hits, err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for range 10 {
				hits, err := index.Nearby(ctx, typeID, 121.47, 31.23, 1000, 50)
				if err != nil || len(hits) != 50 {
					t.Errorf("partial index count=%d err=%v", len(hits), err)
					return
				}
				generation := hits[0].ID / 10000
				for _, hit := range hits {
					if hit.ID/10000 != generation {
						t.Error("mixed generations")
						return
					}
				}
			}
		})
	}
	if err := index.Replace(ctx, typeID, newRows); err != nil {
		t.Error(err)
	}
	wg.Wait()
	// Empty/nonempty transitions change the marker as well as the key; readers must see one complete version.
	wg.Go(func() {
		for range 10 {
			if err := index.Replace(ctx, typeID, nil); err != nil {
				t.Error(err)
				return
			}
			if err := index.Replace(ctx, typeID, newRows); err != nil {
				t.Error(err)
				return
			}
		}
	})
	for range 30 {
		hits, err := index.Nearby(ctx, typeID, 121.47, 31.23, 1000, 50)
		if err != nil || (len(hits) != 0 && len(hits) != 50) {
			t.Errorf("empty/nonempty transition count=%d err=%v", len(hits), err)
		}
	}
	wg.Wait()
	if n := deps.Redis.ZCard(ctx, key).Val(); n != 501 {
		t.Fatalf("published index size=%d", n)
	}
	if ttl := deps.Redis.TTL(ctx, key).Val(); ttl != -1 {
		t.Fatalf("published index must not retain temp TTL: %v", ttl)
	}
	if err := deps.Redis.GeoAdd(ctx, key, &redis.GeoLocation{Name: "broken-member", Longitude: 121.47, Latitude: 31.23}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Nearby(ctx, typeID, 121.47, 31.23, 1000, 1000); err == nil {
		t.Fatal("malformed member accepted")
	}
}

type evictGeoBuildHook struct {
	client *redis.Client
	once   sync.Once
}

func (h *evictGeoBuildHook) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *evictGeoBuildHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *evictGeoBuildHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if err := next(ctx, cmds); err != nil {
			return err
		}
		for _, cmd := range cmds {
			if cmd.Name() != "geoadd" {
				continue
			}
			key, ok := cmd.Args()[1].(string)
			if !ok || !strings.Contains(key, ":build:") {
				continue
			}
			var cleanupErr error
			h.once.Do(func() { cleanupErr = h.client.Del(ctx, key).Err() })
			if cleanupErr != nil {
				return errors.New("fault injection cleanup failed")
			}
		}
		return nil
	}
}
