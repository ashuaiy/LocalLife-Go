package cache

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"strconv"
	"time"
)

//go:embed lua/seckill_check.lua
var seckillCheckLua string

//go:embed lua/seckill_compensate.lua
var seckillCompensateLua string
var seckillCheck = redis.NewScript(seckillCheckLua)
var seckillCompensate = redis.NewScript(seckillCompensateLua)

func (s *Seckill) Check(ctx context.Context, seed model.SeckillSeed, pristine bool) error {
	flag := 0
	if pristine {
		flag = 1
	}
	n, err := seckillCheck.Run(ctx, s.redis, seckillKeys(seed.VoucherID), seed.Generation, seed.Stock, seed.BeginTime.UnixMilli(), seed.EndTime.UnixMilli(), flag).Int()
	if err != nil || n != 1 {
		return apperror.New(apperror.Dependency, err)
	}
	return nil
}
func (s *Seckill) ReserveForGeneration(ctx context.Context, voucherID, userID uint64, generation string) (model.SeckillEvent, error) {
	if voucherID == 0 || userID == 0 || !seckillGeneration.MatchString(generation) {
		return model.SeckillEvent{}, apperror.New(apperror.Validation, nil)
	}
	values, err := seckillReserve.Run(ctx, s.redis, seckillKeys(voucherID), strconv.FormatUint(voucherID, 10), strconv.FormatUint(userID, 10), generation).Slice()
	if err != nil {
		return model.SeckillEvent{}, apperror.New(apperror.Dependency, err)
	}
	return decodeSeckillReply(values, voucherID, userID)
}

func (s *Seckill) Read(ctx context.Context, id uint64, consumer string, count int64) ([]model.SeckillEvent, error) {
	streams, err := s.redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "orders", Consumer: consumer, Streams: []string{seckillKeys(id)[1], ">"}, Count: count, Block: -1}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return decodeSeckillMessages(id, streams[0].Messages)
}
func (s *Seckill) Claim(ctx context.Context, id uint64, consumer, start string, idle time.Duration, count int64) ([]model.SeckillEvent, string, error) {
	messages, next, err := s.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: seckillKeys(id)[1], Group: "orders", Consumer: consumer, MinIdle: idle, Start: start, Count: count}).Result()
	if err != nil {
		return nil, start, apperror.New(apperror.Dependency, err)
	}
	events, err := decodeSeckillMessages(id, messages)
	return events, next, err
}
func (s *Seckill) Ack(ctx context.Context, event model.SeckillEvent) error {
	if err := s.redis.XAck(ctx, seckillKeys(event.VoucherID)[1], "orders", event.ID).Err(); err != nil {
		return apperror.New(apperror.Dependency, err)
	}
	return nil
}
func (s *Seckill) Compensate(ctx context.Context, event model.SeckillEvent) error {
	n, err := seckillCompensate.Run(ctx, s.redis, seckillKeys(event.VoucherID), strconv.FormatUint(event.UserID, 10), event.ID, event.Generation).Int()
	if err != nil || n != 1 {
		return apperror.New(apperror.Dependency, err)
	}
	return nil
}

// Return valid deliveries even when another entry is malformed. Bad entries stay
// pending for inspection; they must not prevent unrelated orders from completing.
func decodeSeckillMessages(voucherID uint64, messages []redis.XMessage) ([]model.SeckillEvent, error) {
	events := make([]model.SeckillEvent, 0, len(messages))
	var faults []error
	for _, msg := range messages {
		user, ok := msg.Values["user_id"].(string)
		uid, err := strconv.ParseUint(user, 10, 64)
		if !ok || err != nil || uid == 0 || msg.Values["voucher_id"] != strconv.FormatUint(voucherID, 10) || len(msg.Values) != 4 {
			faults = append(faults, fmt.Errorf("malformed seckill event %s", msg.ID))
			continue
		}
		event, err := decodeSeckillReply([]any{"accepted", msg.ID, msg.Values["generation"], msg.Values["accepted_ms"]}, voucherID, uid)
		if err != nil {
			faults = append(faults, fmt.Errorf("malformed seckill event %s", msg.ID))
			continue
		}
		events = append(events, event)
	}
	return events, errors.Join(faults...)
}
