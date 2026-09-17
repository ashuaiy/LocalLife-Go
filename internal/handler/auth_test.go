package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/requestid"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type authStub struct {
	fail     error
	seenUser uint64
	calls    int
	t        *testing.T
}

func (a *authStub) check(ctx context.Context) {
	a.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		a.t.Error("missing request deadline")
	}
	if requestid.FromContext(ctx) == "" {
		a.t.Error("missing request id")
	}
	a.calls++
}
func (a *authStub) RequestCode(ctx context.Context, phone string) (service.CodeResult, error) {
	a.check(ctx)
	return service.CodeResult{ExpiresIn: 300, DevCode: "123456"}, a.fail
}
func (a *authStub) Login(ctx context.Context, phone, code string) (service.LoginResult, error) {
	a.check(ctx)
	return service.LoginResult{Token: "private-token", User: service.PublicUser{ID: 7}}, a.fail
}
func (a *authStub) Authenticate(ctx context.Context, token string) (uint64, error) {
	a.check(ctx)
	if a.fail != nil {
		return 0, a.fail
	}
	if token != "private-token" {
		return 0, apperror.New(apperror.Unauthorized, nil)
	}
	return 7, nil
}
func (a *authStub) Me(ctx context.Context, id uint64) (service.PublicUser, error) {
	a.check(ctx)
	a.seenUser = id
	return service.PublicUser{ID: id, Nickname: "local"}, a.fail
}
func (a *authStub) Logout(ctx context.Context, token string) error { a.check(ctx); return a.fail }

func TestAuthHTTPContractAndNoCredentialLogs(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	var logs bytes.Buffer
	h := appserver.NewServer(cfg, slog.New(slog.NewJSONHandler(&logs, nil)), nil)
	stub := &authStub{t: t}
	NewAuth(stub).Register(h)
	for _, tc := range []struct {
		method, path, body, auth string
		status                   int
	}{
		{"POST", "/api/v1/auth/code", `{"phone":"13800138000"}`, "", 200},
		{"POST", "/api/v1/auth/login", `{"phone":"13800138000","code":"123456"}`, "", 200},
		{"GET", "/api/v1/users/me", "", "Bearer private-token", 200},
		{"POST", "/api/v1/auth/logout", "", "Bearer private-token", 200},
		{"GET", "/api/v1/users/me", "", "", 401},
		{"GET", "/api/v1/users/me", "", "Basic private-token", 401},
		{"GET", "/api/v1/users/me", "", "Bearer wrong-token", 401},
		{"POST", "/api/v1/auth/login", `{broken`, "", 400},
		{"POST", "/api/v1/auth/login", `{"phone":"13800138000","code":"123456","admin":true}`, "", 400},
		{"POST", "/api/v1/auth/login", `{} {}`, "", 400},
	} {
		res := ut.PerformRequest(h.Engine, tc.method, tc.path, &ut.Body{Body: strings.NewReader(tc.body), Len: len(tc.body)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: tc.auth})
		if res.Code != tc.status {
			t.Fatalf("%s %s => %d %s", tc.method, tc.path, res.Code, res.Body)
		}
		if !json.Valid(res.Body.Bytes()) {
			t.Fatal("invalid JSON envelope")
		}
		if res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("auth response must not be cached")
		}
	}
	if stub.seenUser != 7 {
		t.Fatal("authenticated user context not propagated")
	}
	for _, secret := range []string{"private-token", "123456", "13800138000"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("credentials leaked to logs")
		}
	}
}

func TestAuthHTTPDependencyFailure(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
	stub := &authStub{t: t, fail: apperror.New(apperror.Dependency, errors.New("secret-error"))}
	NewAuth(stub).Register(h)
	res := ut.PerformRequest(h.Engine, "GET", "/api/v1/users/me", nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
	if res.Code != 503 || strings.Contains(res.Body.String(), "secret-error") {
		t.Fatalf("dependency error incorrectly mapped: %d %s", res.Code, res.Body)
	}
}
