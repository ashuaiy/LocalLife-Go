package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

// Small stateful doubles isolate orchestration; actual SQL/Redis behavior is covered by integration tests.
type authMemory struct {
	code, token string
	userID      uint64
	issued      bool
	err         error
}

func (s *authMemory) IssueCode(ctx context.Context, phone, code string, ttl, cooldown time.Duration, attempts int) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if s.issued {
		return false, nil
	}
	s.issued = true
	s.code = code
	return true, nil
}
func (s *authMemory) ConsumeCode(ctx context.Context, phone, code string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if s.code == "" || s.code != code {
		return false, nil
	}
	s.code = ""
	return true, nil
}
func (s *authMemory) SaveSession(ctx context.Context, token string, id uint64, ttl time.Duration) error {
	s.token = token
	s.userID = id
	return s.err
}
func (s *authMemory) SessionUser(ctx context.Context, token string) (uint64, bool, error) {
	return s.userID, s.token != "" && token == s.token, s.err
}
func (s *authMemory) DeleteSession(ctx context.Context, token string) error {
	s.token = ""
	return s.err
}

type userMemory struct {
	calls int
	err   error
}

func (u *userMemory) FindOrCreate(ctx context.Context, phone string) (model.User, error) {
	u.calls++
	return model.User{ID: 7, Phone: phone, Nickname: "local_user"}, u.err
}
func (u *userMemory) ByID(ctx context.Context, id uint64) (model.User, error) {
	return model.User{ID: id, Nickname: "local_user"}, u.err
}
func authOptions() config.Auth {
	return config.Auth{DevCodes: true, CodeTTL: 5 * time.Minute, CodeCooldown: time.Minute, SessionTTL: 30 * time.Minute, MaxCodeAttempts: 5}
}
func expectStatus(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	status, _, _ := apperror.Describe(err)
	if status != want {
		t.Fatalf("status=%d error=%v", status, err)
	}
}

func TestAuthLoginSessionAndLogout(t *testing.T) {
	ctx := context.Background()
	store := &authMemory{}
	users := &userMemory{}
	auth := NewAuth(users, store, authOptions())
	code, err := auth.RequestCode(ctx, "13800138000")
	if err != nil {
		t.Fatal(err)
	}
	if len(code.DevCode) != 6 || code.ExpiresIn != 300 {
		t.Fatal("invalid development code")
	}
	_, err = auth.RequestCode(ctx, "13800138000")
	expectStatus(t, err, 429)
	login, err := auth.Login(ctx, "13800138000", code.DevCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(login.Token) != 43 || login.TokenType != "Bearer" || login.ExpiresIn != 1800 || login.User.ID != 7 {
		t.Fatal("invalid login response")
	}
	id, err := auth.Authenticate(ctx, login.Token)
	if err != nil || id != 7 {
		t.Fatalf("session authentication: %d %v", id, err)
	}
	me, err := auth.Me(ctx, id)
	if err != nil || me.ID != 7 {
		t.Fatal("profile failed")
	}
	_, err = auth.Login(ctx, "13800138000", code.DevCode)
	expectStatus(t, err, 401)
	if users.calls != 1 {
		t.Fatal("invalid/replayed code reached user repository")
	}
	if err := auth.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	_, err = auth.Authenticate(ctx, login.Token)
	expectStatus(t, err, 401)
}

func TestAuthValidationAndDisabledCodeDelivery(t *testing.T) {
	opts := authOptions()
	opts.DevCodes = false
	auth := NewAuth(&userMemory{}, &authMemory{}, opts)
	_, err := auth.RequestCode(context.Background(), "13800138000")
	expectStatus(t, err, 503)
	for _, phone := range []string{"", "1380013800", "abc", "+8613800138000", " 13800138000"} {
		_, err := auth.RequestCode(context.Background(), phone)
		expectStatus(t, err, 400)
	}
	for _, code := range []string{"", "12345", "abcdef", "1234567"} {
		_, err := auth.Login(context.Background(), "13800138000", code)
		expectStatus(t, err, 400)
	}
	for _, token := range []string{"", "abc", strings.Repeat("!", 43)} {
		_, err := auth.Authenticate(context.Background(), token)
		expectStatus(t, err, 401)
	}
}

func TestAuthDependencyErrorsDoNotBecomeInvalidCredentials(t *testing.T) {
	store := &authMemory{err: errors.New("private-driver-error")}
	auth := NewAuth(&userMemory{}, store, authOptions())
	_, err := auth.RequestCode(context.Background(), "13800138000")
	expectStatus(t, err, 503)
	_, err = auth.Login(context.Background(), "13800138000", "123456")
	expectStatus(t, err, 503)
	if strings.Contains(err.Error(), "private") {
		t.Fatal("private error leaked")
	}
}
