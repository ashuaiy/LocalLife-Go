package cache

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/redis/go-redis/v9"
)

type Shop struct{ redis *redis.Client }

func NewShop(client *redis.Client) *Shop { return &Shop{redis: client} }
func shopKey(id uint64) string           { return "locallife:shop:v2:" + strconv.FormatUint(id, 10) }

func (s *Shop) Load(ctx context.Context, id uint64) (model.ShopSnapshot, error) {
	data, err := s.redis.Get(ctx, shopKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return model.ShopSnapshot{}, nil
	}
	if err != nil {
		return model.ShopSnapshot{}, err
	}
	snapshot := model.ShopSnapshot{Token: string(data)}
	var row *model.ShopCacheEntry
	if err := json.Unmarshal(data, &row); err != nil {
		return snapshot, err
	}
	if !validShopEntry(row, id) {
		return snapshot, errors.New("invalid shop cache record")
	}
	snapshot.Entry = row
	return snapshot, nil
}

func validShopEntry(entry *model.ShopCacheEntry, id uint64) bool {
	return entry != nil && id > 0 && entry.ID == id && !entry.RefreshAfter.IsZero() && !entry.ExpiresAt.IsZero() && !entry.RefreshAfter.After(entry.ExpiresAt) && (entry.Value == nil || (entry.Value.ID == id && entry.Value.TypeID > 0))
}

var publishShop = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if (current or '') ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`)

func (s *Shop) CompareAndSet(ctx context.Context, id uint64, expected string, row model.ShopCacheEntry, ttl time.Duration) (bool, error) {
	if !validShopEntry(&row, id) || ttl < time.Millisecond {
		return false, errors.New("invalid shop cache record or TTL")
	}
	data, err := json.Marshal(row)
	if err != nil {
		return false, err
	}
	n, err := publishShop.Run(ctx, s.redis, []string{shopKey(id)}, expected, data, ttl.Milliseconds()).Int()
	return n == 1, err
}
func (s *Shop) Invalidate(ctx context.Context, id uint64) error {
	return s.redis.Del(ctx, shopKey(id)).Err()
}
