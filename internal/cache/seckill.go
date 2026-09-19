package cache

import (
	"context"
	_ "embed"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/redis/go-redis/v9"
)

type Seckill struct{ redis *redis.Client }

func NewSeckill(client *redis.Client) *Seckill { return &Seckill{redis: client} }

//go:embed lua/seckill_prepare.lua
var seckillPrepareLua string

//go:embed lua/seckill_reserve.lua
var seckillReserveLua string

//go:embed lua/seckill_receipt.lua
var seckillReceiptLua string

var seckillPrepare = redis.NewScript(seckillPrepareLua)
var seckillReserve = redis.NewScript(seckillReserveLua)
var seckillReceipt = redis.NewScript(seckillReceiptLua)
var seckillGeneration = regexp.MustCompile(`^[0-9a-f]{32}$`)
var seckillStreamID = regexp.MustCompile(`^[0-9]+-[0-9]+$`)

const luaExactInteger int64 = 9007199254740991

func seckillKeys(id uint64) []string {
	prefix := "locallife:seckill:{" + strconv.FormatUint(id, 10) + "}"
	return []string{prefix + ":state", prefix + ":events"}
}

// Prepare is create-only. The activation coordinator must persist/fence the
// generation in MySQL before exposing this snapshot to requests. It is not a
// recovery/replenishment operation and is deliberately not wired to HTTP.
func (s *Seckill) Prepare(ctx context.Context, seed model.SeckillSeed) error {
	begin, end := seed.BeginTime.UnixMilli(), seed.EndTime.UnixMilli()
	if seed.VoucherID == 0 || !seckillGeneration.MatchString(seed.Generation) || seed.Stock < 0 || seed.Stock > luaExactInteger || begin < 0 || end <= begin || end > luaExactInteger {
		return apperror.New(apperror.Validation, nil)
	}
	code, err := seckillPrepare.Run(ctx, s.redis, seckillKeys(seed.VoucherID), seed.Generation, seed.Stock, begin, end).Text()
	if err != nil {
		return apperror.New(apperror.Dependency, err)
	}
	if code == "ok" {
		return nil
	}
	if code == "conflict" {
		return apperror.New(apperror.Conflict, nil)
	}
	return apperror.New(apperror.Dependency, nil)
}

func (s *Seckill) Reserve(ctx context.Context, voucherID, userID uint64) (model.SeckillEvent, error) {
	if voucherID == 0 || userID == 0 {
		return model.SeckillEvent{}, apperror.New(apperror.Validation, nil)
	}
	values, err := seckillReserve.Run(ctx, s.redis, seckillKeys(voucherID), strconv.FormatUint(voucherID, 10), strconv.FormatUint(userID, 10)).Slice()
	if err != nil {
		return model.SeckillEvent{}, apperror.New(apperror.Dependency, err)
	}
	return decodeSeckillReply(values, voucherID, userID)
}

// Receipt recovers an accepted ticket after a lost response. It does not claim
// that the event has been persisted as an order; the final result lives in MySQL.
func (s *Seckill) Receipt(ctx context.Context, voucherID, userID uint64) (model.SeckillEvent, error) {
	if voucherID == 0 || userID == 0 {
		return model.SeckillEvent{}, apperror.New(apperror.Validation, nil)
	}
	values, err := seckillReceipt.Run(ctx, s.redis, seckillKeys(voucherID), strconv.FormatUint(voucherID, 10), strconv.FormatUint(userID, 10)).Slice()
	if err != nil {
		return model.SeckillEvent{}, apperror.New(apperror.Dependency, err)
	}
	return decodeSeckillReply(values, voucherID, userID)
}

func decodeSeckillReply(values []any, voucherID, userID uint64) (model.SeckillEvent, error) {
	bad := func() (model.SeckillEvent, error) {
		return model.SeckillEvent{}, apperror.New(apperror.Dependency, errors.New("invalid seckill reply"))
	}
	if len(values) == 0 {
		return bad()
	}
	code, ok := values[0].(string)
	if !ok {
		return bad()
	}
	if code != "accepted" {
		if len(values) != 1 {
			return bad()
		}
		switch apperror.Kind(code) {
		case apperror.ActivityNotStarted, apperror.ActivityEnded, apperror.SoldOut, apperror.AlreadyPurchased, apperror.NotFound:
			return model.SeckillEvent{}, apperror.New(apperror.Kind(code), nil)
		default:
			return bad()
		}
	}
	if len(values) != 4 {
		return bad()
	}
	id, idOK := values[1].(string)
	generation, generationOK := values[2].(string)
	stamp, stampOK := values[3].(string)
	ms, err := strconv.ParseInt(stamp, 10, 64)
	if !idOK || !generationOK || !stampOK || !seckillStreamID.MatchString(id) || !seckillGeneration.MatchString(generation) || err != nil || ms < 0 || ms > luaExactInteger {
		return bad()
	}
	return model.SeckillEvent{ID: id, VoucherID: voucherID, UserID: userID, Generation: generation, AcceptedAt: time.UnixMilli(ms).UTC()}, nil
}
