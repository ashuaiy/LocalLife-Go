package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type feedFollowerStub struct {
	ids    []uint64
	err    error
	author uint64
	at     time.Time
}

func (r *feedFollowerStub) Followers(_ context.Context, author uint64, at time.Time) ([]uint64, error) {
	r.author, r.at = author, at
	return r.ids, r.err
}

type feedBlogStub struct {
	rows []model.Blog
	err  error
	ids  []uint64
}

func (r *feedBlogStub) VisibleByIDs(_ context.Context, _ uint64, ids []uint64) ([]model.Blog, error) {
	r.ids = ids
	return r.rows, r.err
}
func (r *feedBlogStub) FeedSource(context.Context, uint64) ([]model.Blog, error) {
	return r.rows, r.err
}

type feedStoreStub struct {
	entries                  []model.FeedEntry
	err                      error
	snapshotCalls, pushCalls int
	max, offset              int64
	limit                    int
	recipients               []uint64
	pushed                   model.FeedEntry
	replaced                 []model.FeedEntry
}

const testSnapshot = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func (r *feedStoreStub) Push(_ context.Context, ids []uint64, entry model.FeedEntry) error {
	r.pushCalls++
	r.recipients, r.pushed = ids, entry
	return r.err
}
func (r *feedStoreStub) Snapshot(context.Context, uint64) (string, error) {
	r.snapshotCalls++
	return testSnapshot, r.err
}
func (r *feedStoreStub) Page(_ context.Context, _ uint64, _ string, max, offset int64, limit int) ([]model.FeedEntry, error) {
	r.max, r.offset, r.limit = max, offset, limit
	return r.entries, r.err
}
func (r *feedStoreStub) Replace(_ context.Context, _ uint64, rows []model.FeedEntry) error {
	r.replaced = rows
	return r.err
}
func testFeedCursor(t *testing.T, max, offset int64) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"snapshot_id": testSnapshot, "max": max, "offset": offset})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func TestFeedEqualScoreCursorAndHydration(t *testing.T) {
	store := &feedStoreStub{entries: []model.FeedEntry{{BlogID: 4, Score: 100}, {BlogID: 3, Score: 100}, {BlogID: 2, Score: 100}}}
	blogs := &feedBlogStub{rows: []model.Blog{{ID: 3}, {ID: 4}}}
	f := NewFeed(&feedFollowerStub{}, blogs, store)
	page, err := f.Read(context.Background(), 7, "", 2)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != 4 || !page.HasMore || page.NextCursor == "" || store.snapshotCalls != 1 || store.limit != 3 {
		t.Fatalf("first=%+v err=%v", page, err)
	}
	// Stay within a tied score across several pages; offset must accumulate, not reset to page size.
	store.entries = []model.FeedEntry{{BlogID: 2, Score: 100}, {BlogID: 1, Score: 100}, {BlogID: 9, Score: 90}}
	blogs.rows = []model.Blog{{ID: 1}, {ID: 2}}
	page, err = f.Read(context.Background(), 7, page.NextCursor, 2)
	if err != nil || store.max != 100 || store.offset != 2 || store.snapshotCalls != 1 {
		t.Fatalf("continuation=%+v store=%+v err=%v", page, store, err)
	}
	var decoded struct {
		Max    int64
		Offset int64
	}
	raw, _ := base64.RawURLEncoding.DecodeString(page.NextCursor)
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Max != 100 || decoded.Offset != 4 {
		t.Fatalf("same-score cursor=%s err=%v", raw, err)
	}
	store.entries = []model.FeedEntry{{BlogID: 9, Score: 90}, {BlogID: 8, Score: 80}, {BlogID: 7, Score: 80}}
	blogs.rows = nil
	page, err = f.Read(context.Background(), 7, page.NextCursor, 2)
	raw, _ = base64.RawURLEncoding.DecodeString(page.NextCursor)
	_ = json.Unmarshal(raw, &decoded)
	if err != nil || page.Items == nil || len(page.Items) != 0 || decoded.Max != 80 || decoded.Offset != 1 || !page.HasMore {
		t.Fatalf("filtered cursor=%s page=%+v err=%v", raw, page, err)
	}
	store.entries = nil
	page, err = f.Read(context.Background(), 7, page.NextCursor, 2)
	if err != nil || page.Items == nil || page.HasMore || page.NextCursor != "" {
		t.Fatalf("terminal=%+v err=%v", page, err)
	}
}
func TestFeedPushAndFailures(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	followers := &feedFollowerStub{ids: []uint64{7, 8}}
	blogs := &feedBlogStub{rows: []model.Blog{{ID: 9, CreatedAt: at}}}
	store := &feedStoreStub{}
	f := NewFeed(followers, blogs, store)
	if err := f.Push(context.Background(), model.Blog{ID: 9, UserID: 3, CreatedAt: at}); err != nil || store.pushed.BlogID != 9 || store.pushed.Score != at.UnixMilli() || len(store.recipients) != 2 || followers.author != 3 || !followers.at.Equal(at) {
		t.Fatalf("push=%+v err=%v", store, err)
	}
	followers.ids = nil
	before := store.pushCalls
	if err := f.Push(context.Background(), model.Blog{ID: 10, UserID: 3, CreatedAt: at}); err != nil || store.pushCalls != before {
		t.Fatal("empty followers pushed to Redis")
	}
	n, err := f.Rebuild(context.Background(), 7)
	if err != nil || n != 1 || len(store.replaced) != 1 || store.replaced[0].BlogID != 9 {
		t.Fatalf("rebuild=%d err=%v", n, err)
	}
	for _, cursor := range []string{"not-base64", "e30", testFeedCursor(t, 100, -1), testFeedCursor(t, 9007199254740992, 0)} {
		_, err := f.Read(context.Background(), 7, cursor, 10)
		expectStatus(t, err, 400)
	}
	_, err = f.Read(context.Background(), 0, "", 10)
	expectStatus(t, err, 401)
	_, err = f.Read(context.Background(), 7, "", 51)
	expectStatus(t, err, 400)
	store.err = errors.New("redis down")
	_, err = f.Read(context.Background(), 7, "", 10)
	expectStatus(t, err, 503)
	store.err = apperror.New(apperror.Conflict, nil)
	_, err = f.Read(context.Background(), 7, testFeedCursor(t, 100, 0), 10)
	expectStatus(t, err, 409)
}

func TestFeedRejectsIncompleteCursor(t *testing.T) {
	for _, fields := range []map[string]any{
		{},
		{"max": 100},
		{"offset": 0},
		{"max": nil, "offset": 0},
		{"max": 100, "offset": nil},
		{"max": nil, "offset": nil},
	} {
		fields["snapshot_id"] = testSnapshot
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(string(raw), func(t *testing.T) {
			store := &feedStoreStub{}
			f := NewFeed(&feedFollowerStub{}, &feedBlogStub{}, store)
			_, err := f.Read(context.Background(), 7, base64.RawURLEncoding.EncodeToString(raw), 10)
			expectStatus(t, err, 400)
			if store.snapshotCalls != 0 || store.limit != 0 {
				t.Fatal("invalid cursor reached the feed store")
			}
		})
	}
}
