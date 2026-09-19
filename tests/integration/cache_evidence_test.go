package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type evidenceShopRepository struct {
	*repository.Shop
	reads   atomic.Int64
	delay   time.Duration
	gate    <-chan struct{}
	entered chan struct{}
}

func (r *evidenceShopRepository) ByID(ctx context.Context, id uint64) (model.Shop, error) {
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
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return model.Shop{}, ctx.Err()
		}
	}
	return r.Shop.ByID(ctx, id)
}

// Frozen V1 reference: GET -> SELECT -> SET(30m), no negative cache or request coalescing.
// Its key namespace is separate from the current implementation.
func TestShopCacheComparisonEvidence(t *testing.T) {
	deps, ctx := authDependencies(t)
	_, shops := shopFixtures(t, deps, ctx)
	for _, variant := range []string{"v1_reference", "current"} {
		t.Run(variant, func(t *testing.T) {
			repo := &evidenceShopRepository{Shop: repository.NewShop(deps.DB)}
			current := service.NewShop(repo, cache.NewShop(deps.Redis))
			t.Cleanup(func() { _ = current.Close(context.Background()) })
			detail := func(id uint64) error { _, err := current.Detail(ctx, id); return err }
			legacyKey := func(id uint64) string { return fmt.Sprintf("locallife:test:cache-reference:%d", id) }
			t.Cleanup(func() { _ = deps.Redis.Del(context.Background(), legacyKey(shops[0].ID), legacyKey(shops[2].ID)).Err() })
			if variant == "v1_reference" {
				detail = func(id uint64) error {
					raw, err := deps.Redis.Get(ctx, legacyKey(id)).Bytes()
					if err == nil {
						var row model.Shop
						if json.Unmarshal(raw, &row) == nil && row.ID == id {
							return nil
						}
					}
					row, err := repo.ByID(ctx, id)
					if err != nil {
						return err
					}
					raw, err = json.Marshal(row)
					if err != nil {
						return err
					}
					_ = deps.Redis.Set(ctx, legacyKey(id), raw, 30*time.Minute).Err()
					return nil
				}
			}
			// Use a fixture ID known to be absent, never a global key shared with other test runs.
			missing := shops[2].ID
			if variant == "v1_reference" {
				if err := deps.DB.WithContext(ctx).Delete(&shops[2]).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 100 {
				err := detail(missing)
				status, _, _ := apperror.Describe(err)
				if status != 404 {
					t.Fatalf("missing: %v", err)
				}
			}
			negativeReads := repo.reads.Swap(0)
			// A controlled 50 ms database delay exposes simultaneous cold misses; not a latency benchmark.
			repo.delay = 50 * time.Millisecond
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 64 {
				wg.Go(func() {
					<-start
					if err := detail(shops[0].ID); err != nil {
						t.Error(err)
					}
				})
			}
			close(start)
			wg.Wait()
			coldReads := repo.reads.Swap(0)
			repo.delay = 0
			for range 100 {
				if err := detail(shops[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%s: repeated_missing_100_db_reads=%d concurrent_cold_64_db_reads=%d warm_100_db_reads=%d", variant, negativeReads, coldReads, repo.reads.Load())
			if variant == "current" && (negativeReads != 1 || coldReads != 1 || repo.reads.Load() != 0) {
				t.Fatal("cache regression: expected one negative fill, one cold fill and no warm database reads")
			}
		})
	}
}

func BenchmarkShopCacheHit(b *testing.B) {
	deps, ctx := authDependencies(b)
	_, shops := shopFixtures(b, deps, ctx)
	repo := &evidenceShopRepository{Shop: repository.NewShop(deps.DB)}
	s := service.NewShop(repo, cache.NewShop(deps.Redis))
	b.Cleanup(func() { _ = s.Close(context.Background()) })
	if _, err := s.Detail(ctx, shops[0].ID); err != nil {
		b.Fatal(err)
	}
	repo.reads.Store(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Detail(ctx, shops[0].ID); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(repo.reads.Load()), "db_reads")
	if repo.reads.Load() != 0 {
		b.Fatal("warm cache unexpectedly read MySQL")
	}
}
