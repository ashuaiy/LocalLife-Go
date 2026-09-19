package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type FeedFollowers interface {
	Followers(context.Context, uint64, time.Time) ([]uint64, error)
}
type FeedBlogs interface {
	VisibleByIDs(context.Context, uint64, []uint64) ([]model.Blog, error)
	FeedSource(context.Context, uint64) ([]model.Blog, error)
}
type FeedStore interface {
	Push(context.Context, []uint64, model.FeedEntry) error
	Snapshot(context.Context, uint64) (string, error)
	Page(ctx context.Context, userID uint64, snapshot string, max, offset int64, limit int) ([]model.FeedEntry, error)
	Replace(context.Context, uint64, []model.FeedEntry) error
}
type Feed struct {
	followers FeedFollowers
	blogs     FeedBlogs
	store     FeedStore
}

func NewFeed(followers FeedFollowers, blogs FeedBlogs, store FeedStore) *Feed {
	return &Feed{followers: followers, blogs: blogs, store: store}
}

type FeedPage struct {
	Items      []BlogView `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}
type feedCursor struct {
	SnapshotID string `json:"snapshot_id"`
	Max        int64  `json:"max"`
	Offset     int64  `json:"offset"`
}

var snapshotPattern = regexp.MustCompile(`^[A-Z2-7]{26}$`)

const maxFeedScore int64 = 9007199254740991

func (f *Feed) Push(ctx context.Context, blog model.Blog) error {
	ids, err := f.followers.Followers(ctx, blog.UserID, blog.CreatedAt)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return feedError(f.store.Push(ctx, ids, model.FeedEntry{BlogID: blog.ID, Score: blog.CreatedAt.UnixMilli()}))
}
func (f *Feed) Read(ctx context.Context, userID uint64, cursor string, size int) (FeedPage, error) {
	if userID == 0 {
		return FeedPage{}, apperror.New(apperror.Unauthorized, nil)
	}
	if size < 1 || size > 50 {
		return FeedPage{}, apperror.New(apperror.Validation, nil)
	}
	position := feedCursor{Max: maxFeedScore}
	if cursor == "" {
		snapshot, err := f.store.Snapshot(ctx, userID)
		if err != nil {
			return FeedPage{}, feedError(err)
		}
		position.SnapshotID = snapshot
	} else {
		if len(cursor) > 256 {
			return FeedPage{}, apperror.New(apperror.Validation, nil)
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil {
			return FeedPage{}, apperror.New(apperror.Validation, nil)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var decoded struct {
			SnapshotID string `json:"snapshot_id"`
			Max        *int64 `json:"max"`
			Offset     *int64 `json:"offset"`
		}
		if err := decoder.Decode(&decoded); err != nil || decoded.Max == nil || decoded.Offset == nil {
			return FeedPage{}, apperror.New(apperror.Validation, nil)
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return FeedPage{}, apperror.New(apperror.Validation, nil)
		}
		position = feedCursor{SnapshotID: decoded.SnapshotID, Max: *decoded.Max, Offset: *decoded.Offset}
		if !snapshotPattern.MatchString(position.SnapshotID) || position.Max < 0 || position.Max > maxFeedScore || position.Offset < 0 {
			return FeedPage{}, apperror.New(apperror.Validation, nil)
		}
	}
	entries, err := f.store.Page(ctx, userID, position.SnapshotID, position.Max, position.Offset, size+1)
	if err != nil {
		return FeedPage{}, feedError(err)
	}
	result := FeedPage{Items: make([]BlogView, 0, size), HasMore: len(entries) > size}
	if result.HasMore {
		entries = entries[:size]
	}
	if len(entries) == 0 {
		return result, nil
	}
	ids := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.BlogID)
	}
	rows, err := f.blogs.VisibleByIDs(ctx, userID, ids)
	if err != nil {
		return FeedPage{}, err
	}
	byID := make(map[uint64]model.Blog, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for _, entry := range entries {
		if row, ok := byID[entry.BlogID]; ok {
			result.Items = append(result.Items, blogView(row))
		}
	}
	if result.HasMore {
		lastScore := entries[len(entries)-1].Score
		var consumed int64
		for i := len(entries) - 1; i >= 0 && entries[i].Score == lastScore; i-- {
			consumed++
		}
		if lastScore == position.Max {
			consumed += position.Offset
		}
		position.Max, position.Offset = lastScore, consumed
		raw, _ := json.Marshal(position)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}
func (f *Feed) Rebuild(ctx context.Context, userID uint64) (int, error) {
	if userID == 0 {
		return 0, apperror.New(apperror.Validation, nil)
	}
	rows, err := f.blogs.FeedSource(ctx, userID)
	if err != nil {
		return 0, err
	}
	entries := make([]model.FeedEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, model.FeedEntry{BlogID: row.ID, Score: row.CreatedAt.UnixMilli()})
	}
	if err := f.store.Replace(ctx, userID, entries); err != nil {
		return 0, feedError(err)
	}
	return len(entries), nil
}
func feedError(err error) error {
	if err == nil {
		return nil
	}
	var classified *apperror.Error
	if errors.As(err, &classified) {
		return err
	}
	return apperror.New(apperror.Dependency, err)
}
