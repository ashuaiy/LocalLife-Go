package cache

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/auth_issue.lua
var issueCodeLua string

//go:embed lua/auth_consume.lua
var consumeCodeLua string

var issueCode = redis.NewScript(issueCodeLua)
var consumeCode = redis.NewScript(consumeCodeLua)

type Auth struct{ redis *redis.Client }

func NewAuth(client *redis.Client) *Auth { return &Auth{redis: client} }

func codeKeys(phone string) (string, string) {
	prefix := fmt.Sprintf("locallife:auth:{%x}", sha256.Sum256([]byte(phone)))
	return prefix + ":code", prefix + ":cooldown"
}
func codeDigest(phone, code string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(phone+":"+code)))
}
func sessionKey(token string) string {
	return fmt.Sprintf("locallife:session:%x", sha256.Sum256([]byte(token)))
}

func (a *Auth) IssueCode(ctx context.Context, phone, code string, ttl, cooldown time.Duration, attempts int) (bool, error) {
	if ttl.Milliseconds() < 1 || cooldown.Milliseconds() < 1 || attempts < 1 {
		return false, errors.New("invalid code policy")
	}
	key, limitKey := codeKeys(phone)
	result, err := issueCode.Run(ctx, a.redis, []string{key, limitKey}, codeDigest(phone, code), ttl.Milliseconds(), cooldown.Milliseconds(), attempts).Int()
	return result == 1, err
}

func (a *Auth) ConsumeCode(ctx context.Context, phone, code string) (bool, error) {
	key, _ := codeKeys(phone)
	result, err := consumeCode.Run(ctx, a.redis, []string{key}, codeDigest(phone, code)).Int()
	return result == 1, err
}

func (a *Auth) SaveSession(ctx context.Context, token string, id uint64, ttl time.Duration) error {
	if ttl <= 0 || id == 0 {
		return errors.New("invalid session policy")
	}
	ok, err := a.redis.SetNX(ctx, sessionKey(token), strconv.FormatUint(id, 10), ttl).Result()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("session token collision")
	}
	return nil
}

func (a *Auth) SessionUser(ctx context.Context, token string) (uint64, bool, error) {
	value, err := a.redis.Get(ctx, sessionKey(token)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return 0, false, errors.New("invalid session record")
	}
	return id, true, nil
}

func (a *Auth) DeleteSession(ctx context.Context, token string) error {
	return a.redis.Del(ctx, sessionKey(token)).Err()
}
