package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"time"
)

type Community struct {
	redis *redis.Client
	now   func() time.Time
}

func NewCommunity(client *redis.Client) *Community { return &Community{redis: client, now: time.Now} }

var signZone = time.FixedZone("Asia/Shanghai", 8*3600)

func (s *Community) Sign(ctx context.Context, id uint64, write bool) (service.SignState, error) {
	now := s.now().In(signZone)
	key := fmt.Sprintf("locallife:sign:%d:%s", id, now.Format("200601"))
	if write {
		if err := s.redis.SetBit(ctx, key, int64(now.Day()-1), 1).Err(); err != nil {
			return service.SignState{}, apperror.New(apperror.Dependency, err)
		}
	}
	bits, err := s.redis.BitField(ctx, key, "GET", fmt.Sprintf("u%d", now.Day()), 0).Result()
	if err != nil {
		return service.SignState{}, apperror.New(apperror.Dependency, err)
	}
	result := service.SignState{Date: now.Format("2006-01-02")}
	if len(bits) > 0 {
		for n := bits[0]; n&1 == 1; n >>= 1 {
			result.Streak++
		}
	}
	result.Signed = result.Streak > 0
	return result, nil
}
func (s *Community) NotifyLike(ctx context.Context, blogID, userID, authorID uint64, user model.Profile) error {
	data, err := json.Marshal(model.LikeMessage{BlogID: blogID, UserID: userID, Nickname: user.Nickname, Avatar: user.Avatar})
	if err != nil {
		return err
	}
	return s.redis.XAdd(ctx, &redis.XAddArgs{Stream: fmt.Sprintf("locallife:messages:%d", authorID), MaxLen: 1000, Values: map[string]any{"data": string(data)}}).Err()
}
func (s *Community) Messages(ctx context.Context, id uint64, cursor string) ([]model.LikeMessage, error) {
	rows, err := s.redis.XRangeN(ctx, fmt.Sprintf("locallife:messages:%d", id), "("+cursor, "+", 50).Result()
	if err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	result := make([]model.LikeMessage, 0, len(rows))
	for _, row := range rows {
		text, ok := row.Values["data"].(string)
		var item model.LikeMessage
		if !ok || json.Unmarshal([]byte(text), &item) != nil {
			return nil, apperror.New(apperror.Dependency, nil)
		}
		item.ID = row.ID
		result = append(result, item)
	}
	return result, nil
}
