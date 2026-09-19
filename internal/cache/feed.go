package cache

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/redis/go-redis/v9"
)

type Feed struct{ redis *redis.Client }

func NewFeed(client *redis.Client) *Feed { return &Feed{redis: client} }

//go:embed lua/feed_snapshot.lua
var feedSnapshotLua string

//go:embed lua/feed_replace.lua
var feedReplaceLua string
var feedSnapshot = redis.NewScript(feedSnapshotLua)
var feedReplace = redis.NewScript(feedReplaceLua)

func feedPrefix(userID uint64) string {
	return "locallife:feed:{" + strconv.FormatUint(userID, 10) + "}"
}
func validFeedEntry(entry model.FeedEntry) bool {
	return entry.BlogID > 0 && entry.Score >= 0 && entry.Score <= 9007199254740991
}
func (f *Feed) Push(ctx context.Context, ids []uint64, entry model.FeedEntry) error {
	if !validFeedEntry(entry) {
		return errors.New("invalid feed entry")
	}
	for start := 0; start < len(ids); start += 500 {
		pipe := f.redis.Pipeline()
		for _, id := range ids[start:min(start+500, len(ids))] {
			if id == 0 {
				return errors.New("invalid feed recipient")
			}
			// NX makes replay safe and preserves the original position.
			pipe.ZAddNX(ctx, feedPrefix(id)+":live", redis.Z{Score: float64(entry.Score), Member: strconv.FormatUint(entry.BlogID, 10)})
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
func (f *Feed) Snapshot(ctx context.Context, userID uint64) (string, error) {
	id := rand.Text()
	prefix := feedPrefix(userID)
	snapshot := prefix + ":snapshot:" + id
	if err := feedSnapshot.Run(ctx, f.redis, []string{prefix + ":live", snapshot, snapshot + ":count"}, 300).Err(); err != nil {
		return "", err
	}
	return id, nil
}
func (f *Feed) Page(ctx context.Context, userID uint64, snapshot string, max, offset int64, limit int) ([]model.FeedEntry, error) {
	key := feedPrefix(userID) + ":snapshot:" + snapshot
	pipe := f.redis.TxPipeline()
	marker := pipe.Get(ctx, key+":count")
	count := pipe.ZCard(ctx, key)
	rows := pipe.ZRevRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{Max: strconv.FormatInt(max, 10), Min: "-inf", Offset: offset, Count: int64(limit)})
	if _, err := pipe.Exec(ctx); err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, apperror.New(apperror.Conflict, nil)
		}
		return nil, err
	}
	expected, err := strconv.ParseInt(marker.Val(), 10, 64)
	if err != nil || expected < 0 {
		return nil, errors.New("invalid feed snapshot metadata")
	}
	if count.Val() != expected {
		return nil, apperror.New(apperror.Conflict, nil)
	}
	entries := make([]model.FeedEntry, 0, len(rows.Val()))
	for _, row := range rows.Val() {
		member, ok := row.Member.(string)
		if !ok {
			return nil, errors.New("invalid feed member")
		}
		id, err := strconv.ParseUint(member, 10, 64)
		if err != nil || id == 0 || math.IsNaN(row.Score) || math.IsInf(row.Score, 0) || row.Score < 0 || row.Score > 9007199254740991 || math.Trunc(row.Score) != row.Score {
			return nil, errors.New("invalid feed entry")
		}
		entries = append(entries, model.FeedEntry{BlogID: id, Score: int64(row.Score)})
	}
	return entries, nil
}
func (f *Feed) Replace(ctx context.Context, userID uint64, entries []model.FeedEntry) error {
	if userID == 0 {
		return errors.New("invalid feed user")
	}
	for _, entry := range entries {
		if !validFeedEntry(entry) {
			return errors.New("invalid feed entry")
		}
	}
	live := feedPrefix(userID) + ":live"
	temporary := live + ":build:" + rand.Text()
	defer func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = f.redis.Del(clean, temporary).Err()
	}()
	for start := 0; start < len(entries); start += 500 {
		members := make([]redis.Z, 0, min(500, len(entries)-start))
		for _, entry := range entries[start:min(start+500, len(entries))] {
			members = append(members, redis.Z{Score: float64(entry.Score), Member: strconv.FormatUint(entry.BlogID, 10)})
		}
		if _, err := f.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.ZAdd(ctx, temporary, members...)
			pipe.Expire(ctx, temporary, 10*time.Minute)
			return nil
		}); err != nil {
			return err
		}
	}
	return feedReplace.Run(ctx, f.redis, []string{live, temporary}, len(entries)).Err()
}
