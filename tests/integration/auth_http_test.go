package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestAuthHTTPWithRealDependencies(t *testing.T) {
	deps, _ := authDependencies(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.DevCodes = true
	cfg.Auth.CodeCooldown = 30 * time.Millisecond
	cfg.Auth.SessionTTL = time.Second
	auth := service.NewAuth(repository.NewUser(deps.DB), cache.NewAuth(deps.Redis), cfg.Auth)
	var logs bytes.Buffer
	h := appserver.NewServer(cfg, slog.New(slog.NewJSONHandler(&logs, nil)), nil)
	handler.NewAuth(auth).Register(h)
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("139%08d", n.Int64())
	t.Cleanup(func() {
		_ = deps.DB.WithContext(context.Background()).Exec("DELETE FROM users WHERE phone=?", phone).Error
	})

	request := func(method, path, body, token string, want int) []byte {
		t.Helper()
		res := ut.PerformRequest(h.Engine, method, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer " + token})
		if res.Code != want {
			t.Fatalf("%s %s: status=%d want=%d", method, path, res.Code, want)
		}
		if res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential response is cacheable")
		}
		var envelope struct {
			Data      json.RawMessage `json:"data"`
			RequestID string          `json:"request_id"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil || envelope.RequestID == "" {
			t.Fatal("invalid envelope")
		}
		return envelope.Data
	}
	getCode := func() string {
		t.Helper()
		var result service.CodeResult
		if err := json.Unmarshal(request("POST", "/api/v1/auth/code", fmt.Sprintf(`{"phone":%q}`, phone), "", 200), &result); err != nil {
			t.Fatal(err)
		}
		return result.DevCode
	}
	login := func(code string) service.LoginResult {
		t.Helper()
		var result service.LoginResult
		if err := json.Unmarshal(request("POST", "/api/v1/auth/login", fmt.Sprintf(`{"phone":%q,"code":%q}`, phone, code), "", 200), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	request("GET", "/api/v1/users/me", "", "", 401)
	code := getCode()
	wrong := "000000"
	if code == wrong {
		wrong = "999999"
	}
	request("POST", "/api/v1/auth/login", fmt.Sprintf(`{"phone":%q,"code":%q}`, phone, wrong), "", 401)
	first := login(code)
	if first.User.ID == 0 || first.TokenType != "Bearer" || first.ExpiresIn != 1 {
		t.Fatal("invalid login result")
	}
	request("POST", "/api/v1/auth/login", fmt.Sprintf(`{"phone":%q,"code":%q}`, phone, code), "", 401)
	profile := request("GET", "/api/v1/users/me", "", first.Token, 200)
	if strings.Contains(string(profile), "phone") || strings.Contains(string(profile), phone) {
		t.Fatal("profile exposes phone")
	}
	request("POST", "/api/v1/auth/logout", "", first.Token, 200)
	request("GET", "/api/v1/users/me", "", first.Token, 401)
	time.Sleep(50 * time.Millisecond)
	secondCode := getCode()
	second := login(secondCode)
	if second.User.ID != first.User.ID || second.Token == first.Token {
		t.Fatal("returning login must reuse user and rotate token")
	}
	time.Sleep(1100 * time.Millisecond)
	request("GET", "/api/v1/users/me", "", second.Token, 401)
	for _, secret := range []string{phone, code, secondCode, first.Token, second.Token} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("credential data leaked to access logs")
		}
	}
	if err := deps.Redis.Close(); err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/v1/users/me", "", second.Token, 503)
}
