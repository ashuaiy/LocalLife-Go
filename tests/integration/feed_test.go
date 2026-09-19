package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/redis/go-redis/v9"
)

func TestFollowAndFeedEqualScoresSnapshotAndRecovery(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, shops := blogFixtures(t, deps, ctx)
	author, reader := users[0].ID, users[1].ID
	prefix := fmt.Sprintf("locallife:feed:{%d}", reader)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deps.DB.WithContext(clean).Where("user_id = ?", reader).Delete(&model.Follow{}).Error; err != nil {
			t.Error(err)
		}
		var scan uint64
		for {
			keys, next, err := deps.Redis.Scan(clean, scan, prefix+"*", 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := deps.Redis.Del(clean, keys...).Err(); err != nil {
					t.Error(err)
				}
			}
			scan = next
			if scan == 0 {
				break
			}
		}
	})
	follows := repository.NewFollow(deps.DB)
	blogs := repository.NewBlog(deps.DB)
	store := cache.NewFeed(deps.Redis)
	follow := service.NewFollow(follows, repository.NewUser(deps.DB))
	feed := service.NewFeed(follows, blogs, store)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := follow.Set(ctx, reader, author, true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int64
	if err := deps.DB.WithContext(ctx).Model(&model.Follow{}).Where("user_id = ? AND follow_user_id = ?", reader, author).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate follows=%d err=%v", count, err)
	}
	page, err := follow.List(ctx, reader, 1, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != author {
		t.Fatalf("following=%+v err=%v", page, err)
	}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	if err := deps.DB.WithContext(ctx).Model(&model.Follow{}).Where("user_id = ?", reader).Update("created_at", at.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	var entries []model.Blog
	for i := range 9 {
		created := at
		if i >= 7 {
			created = at.Add(-time.Second)
		}
		row := model.Blog{UserID: author, ShopID: shops[0].ID, Title: fmt.Sprintf("feed-%d", i), Content: "content", CreatedAt: created}
		if err := deps.DB.WithContext(ctx).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		entries = append(entries, row)
		if err := feed.Push(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := feed.Push(ctx, entries[0]); err != nil {
		t.Fatal(err)
	}
	if n := deps.Redis.ZCard(ctx, prefix+":live").Val(); n != 9 {
		t.Fatalf("replayed fanout count=%d", n)
	}
	first, err := feed.Read(ctx, reader, "", 2)
	if err != nil || len(first.Items) != 2 || !first.HasMore {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// The exact same score arriving after the first page must not shift snapshot offsets.
	late := model.Blog{UserID: author, ShopID: shops[0].ID, Title: "late tied score", Content: "content", CreatedAt: at}
	if err := deps.DB.WithContext(ctx).Create(&late).Error; err != nil {
		t.Fatal(err)
	}
	if err := feed.Push(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, err := feed.Rebuild(ctx, reader); err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint64]bool)
	current := first
	for rounds := 0; ; rounds++ {
		if rounds > 20 {
			t.Fatal("cursor did not terminate")
		}
		for _, item := range current.Items {
			if seen[item.ID] || item.ID == late.ID {
				t.Fatalf("duplicate or new item entered snapshot: %d", item.ID)
			}
			seen[item.ID] = true
		}
		if !current.HasMore {
			break
		}
		current, err = feed.Read(ctx, reader, current.NextCursor, 2)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 9 {
		t.Fatalf("snapshot lost items: %d", len(seen))
	}
	for _, row := range entries {
		if !seen[row.ID] {
			t.Fatalf("missing tied item %d", row.ID)
		}
	}
	fresh, err := feed.Read(ctx, reader, "", 50)
	if err != nil || len(fresh.Items) != 10 {
		t.Fatalf("refresh count=%d err=%v", len(fresh.Items), err)
	}
	// A snapshot belongs to its authenticated reader, never to a cursor-supplied user.
	_, err = feed.Read(ctx, author, first.NextCursor, 2)
	status, _, _ := apperror.Describe(err)
	if status != 409 {
		t.Fatalf("other user's cursor accepted: %v", err)
	}
	var position struct {
		SnapshotID string `json:"snapshot_id"`
	}
	raw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err := json.Unmarshal(raw, &position); err != nil {
		t.Fatal(err)
	}
	meta := prefix + ":snapshot:" + position.SnapshotID + ":count"
	if ttl := deps.Redis.TTL(ctx, meta).Val(); ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("snapshot TTL=%v", ttl)
	}
	if err := deps.Redis.Del(ctx, meta).Err(); err != nil {
		t.Fatal(err)
	}
	_, err = feed.Read(ctx, reader, first.NextCursor, 2)
	status, _, _ = apperror.Describe(err)
	if status != 409 {
		t.Fatalf("expired snapshot accepted: %v", err)
	}
	if _, err := follow.Set(ctx, reader, author, false); err != nil {
		t.Fatal(err)
	}
	hidden, err := feed.Read(ctx, reader, "", 2)
	if err != nil || len(hidden.Items) != 0 || !hidden.HasMore {
		t.Fatalf("unfollow visibility/cursor=%+v err=%v", hidden, err)
	}
	if _, err := follow.Set(ctx, reader, author, true); err != nil {
		t.Fatal(err)
	}
	if n, err := feed.Rebuild(ctx, reader); err != nil || n != 0 {
		t.Fatalf("refollow must not restore pre-follow history: %d %v", n, err)
	}
	empty, err := feed.Read(ctx, reader, "", 10)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}

func TestFeedPublishFailurePreservesBlogForRebuild(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, shops := blogFixtures(t, deps, ctx)
	author, reader := users[0].ID, users[1].ID
	follows := repository.NewFollow(deps.DB)
	blogs := repository.NewBlog(deps.DB)
	if err := follows.Set(ctx, reader, author, true); err != nil {
		t.Fatal(err)
	}
	// Make the follow definitely older than the new publication, independent of wall-clock sub-millisecond rounding.
	if err := deps.DB.WithContext(ctx).Model(&model.Follow{}).Where("user_id = ?", reader).Update("created_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("locallife:feed:{%d}:live", reader)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = deps.Redis.Del(clean, key).Err()
		if err := deps.DB.WithContext(clean).Where("user_id = ?", reader).Delete(&model.Follow{}).Error; err != nil {
			t.Error(err)
		}
	})
	closed := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	_ = closed.Close()
	failedFeed := service.NewFeed(follows, blogs, cache.NewFeed(closed))
	publication := service.NewBlog(blogs, repository.NewShop(deps.DB), failedFeed)
	_, err := publication.Publish(ctx, author, service.BlogInput{ShopID: shops[0].ID, Title: "recoverable", Content: "persisted before fanout failure"})
	status, _, _ := apperror.Describe(err)
	if status != 503 {
		t.Fatalf("fanout failure=%v", err)
	}
	var saved []model.Blog
	if err := deps.DB.WithContext(ctx).Where("user_id = ?", author).Find(&saved).Error; err != nil || len(saved) != 1 {
		t.Fatalf("persisted rows=%d err=%v", len(saved), err)
	}
	store := cache.NewFeed(deps.Redis)
	feed := service.NewFeed(follows, blogs, store)
	if n, err := feed.Rebuild(ctx, reader); err != nil || n != 1 {
		t.Fatalf("rebuild=%d err=%v", n, err)
	}
	if score, err := deps.Redis.ZScore(ctx, key, fmt.Sprint(saved[0].ID)).Result(); err != nil || int64(score) != saved[0].CreatedAt.UnixMilli() {
		t.Fatalf("recovered entry score=%f err=%v", score, err)
	}
	// An incomplete replacement (duplicate IDs) must leave the prior live index untouched.
	duplicate := model.FeedEntry{BlogID: saved[0].ID, Score: saved[0].CreatedAt.UnixMilli()}
	if err := store.Replace(ctx, reader, []model.FeedEntry{duplicate, duplicate}); err == nil {
		t.Fatal("incomplete replacement accepted")
	}
	if n := deps.Redis.ZCard(ctx, key).Val(); n != 1 {
		t.Fatalf("failed replacement lost old feed: %d", n)
	}
}
