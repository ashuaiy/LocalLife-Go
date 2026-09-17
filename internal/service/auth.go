package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type Users interface {
	FindOrCreate(context.Context, string) (model.User, error)
	ByID(context.Context, uint64) (model.User, error)
}

type AuthStore interface {
	IssueCode(context.Context, string, string, time.Duration, time.Duration, int) (bool, error)
	ConsumeCode(context.Context, string, string) (bool, error)
	SaveSession(context.Context, string, uint64, time.Duration) error
	SessionUser(context.Context, string) (uint64, bool, error)
	DeleteSession(context.Context, string) error
}

type Auth struct {
	users   Users
	store   AuthStore
	options config.Auth
}

func NewAuth(users Users, store AuthStore, options config.Auth) *Auth {
	return &Auth{users: users, store: store, options: options}
}

type PublicUser struct {
	ID       uint64 `json:"id,string"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}
type CodeResult struct {
	ExpiresIn int64  `json:"expires_in"`
	DevCode   string `json:"dev_code,omitempty"`
}
type LoginResult struct {
	Token     string     `json:"token"`
	TokenType string     `json:"token_type"`
	ExpiresIn int64      `json:"expires_in"`
	User      PublicUser `json:"user"`
}

var phonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var codePattern = regexp.MustCompile(`^[0-9]{6}$`)

func (a *Auth) RequestCode(ctx context.Context, phone string) (CodeResult, error) {
	if !phonePattern.MatchString(phone) {
		return CodeResult{}, apperror.New(apperror.Validation, nil)
	}
	// A real SMS delivery adapter is intentionally not configured yet. Fail closed.
	if !a.options.DevCodes {
		return CodeResult{}, apperror.New(apperror.Dependency, nil)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return CodeResult{}, apperror.New(apperror.Internal, err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	ok, err := a.store.IssueCode(ctx, phone, code, a.options.CodeTTL, a.options.CodeCooldown, a.options.MaxCodeAttempts)
	if err != nil {
		return CodeResult{}, apperror.New(apperror.Dependency, err)
	}
	if !ok {
		return CodeResult{}, apperror.New(apperror.RateLimited, nil)
	}
	return CodeResult{ExpiresIn: int64(a.options.CodeTTL.Seconds()), DevCode: code}, nil
}

func (a *Auth) Login(ctx context.Context, phone, code string) (LoginResult, error) {
	if !phonePattern.MatchString(phone) || !codePattern.MatchString(code) {
		return LoginResult{}, apperror.New(apperror.Validation, nil)
	}
	ok, err := a.store.ConsumeCode(ctx, phone, code)
	if err != nil {
		return LoginResult{}, apperror.New(apperror.Dependency, err)
	}
	if !ok {
		return LoginResult{}, apperror.New(apperror.Unauthorized, nil)
	}
	user, err := a.users.FindOrCreate(ctx, phone)
	if err != nil {
		return LoginResult{}, apperror.New(apperror.Dependency, err)
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return LoginResult{}, apperror.New(apperror.Internal, err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	if err := a.store.SaveSession(ctx, token, user.ID, a.options.SessionTTL); err != nil {
		return LoginResult{}, apperror.New(apperror.Dependency, err)
	}
	return LoginResult{Token: token, TokenType: "Bearer", ExpiresIn: int64(a.options.SessionTTL.Seconds()), User: publicUser(user)}, nil
}

func (a *Auth) Authenticate(ctx context.Context, token string) (uint64, error) {
	if !validToken(token) {
		return 0, apperror.New(apperror.Unauthorized, nil)
	}
	id, found, err := a.store.SessionUser(ctx, token)
	if err != nil {
		return 0, apperror.New(apperror.Dependency, err)
	}
	if !found {
		return 0, apperror.New(apperror.Unauthorized, nil)
	}
	return id, nil
}

func (a *Auth) Me(ctx context.Context, id uint64) (PublicUser, error) {
	user, err := a.users.ByID(ctx, id)
	if err != nil {
		return PublicUser{}, err
	}
	return publicUser(user), nil
}

func (a *Auth) Logout(ctx context.Context, token string) error {
	if !validToken(token) {
		return apperror.New(apperror.Unauthorized, nil)
	}
	if err := a.store.DeleteSession(ctx, token); err != nil {
		return apperror.New(apperror.Dependency, err)
	}
	return nil
}

func publicUser(user model.User) PublicUser {
	return PublicUser{ID: user.ID, Nickname: user.Nickname, Avatar: user.Avatar}
}
func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(decoded) == 32
}
