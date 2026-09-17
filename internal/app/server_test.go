package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/pkg/requestid"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHealthAndReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		fail       bool
		want       int
	}{
		{"live despite dependency failure", "/healthz", true, 200},
		{"ready", "/readyz", false, 200}, {"unready", "/readyz", true, 503},
		{"unknown route", "/missing", false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			check := func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("dependency call lacks deadline")
				}
				if requestid.FromContext(ctx) != "test-request" {
					t.Error("request id not propagated")
				}
				if tc.fail {
					return errors.New("secret-password")
				}
				return nil
			}
			h := NewServer(testConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)), map[string]Check{"mysql": check, "redis": check})
			res := ut.PerformRequest(h.Engine, "GET", tc.path+"?token=private-token", nil, ut.Header{Key: "X-Request-ID", Value: "test-request"})
			if res.Code != tc.want {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			var body struct {
				Code      string `json:"code"`
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code == "" || body.RequestID != "test-request" || res.Header().Get("X-Request-ID") != "test-request" {
				t.Fatalf("invalid response envelope: %s", res.Body)
			}
			if strings.Contains(logs.String()+res.Body.String(), "private-token") || strings.Contains(logs.String()+res.Body.String(), "secret-password") {
				t.Fatal("secret leaked in HTTP or log")
			}
			for _, field := range []string{"request_id", "method", "path", "latency_ms", "status", "error"} {
				if !strings.Contains(logs.String(), `"`+field+`"`) {
					t.Errorf("missing log field %s", field)
				}
			}
		})
	}
}

func TestReadinessUsesBoundedContext(t *testing.T) {
	cfg := testConfig(t)
	cfg.DependencyTimeout = 15 * time.Millisecond
	h := NewServer(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), map[string]Check{
		"redis": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	})
	start := time.Now()
	res := ut.PerformRequest(h.Engine, "GET", "/readyz", nil)
	if res.Code != 503 || time.Since(start) > time.Second {
		t.Fatalf("readiness did not terminate: %d", res.Code)
	}
}

func TestRecoveryAndRequestIDValidation(t *testing.T) {
	var logs bytes.Buffer
	h := NewServer(testConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)), nil)
	h.GET("/panic", func(context.Context, *app.RequestContext) { panic("secret-panic-value") })
	res := ut.PerformRequest(h.Engine, "GET", "/panic", nil, ut.Header{Key: "X-Request-ID", Value: strings.Repeat("x", 100)})
	if res.Code != 500 || res.Header().Get("X-Request-ID") == "" || len(res.Header().Get("X-Request-ID")) > 64 {
		t.Fatalf("recovery/id failed: %d %s", res.Code, res.Body)
	}
	if strings.Contains(logs.String()+res.Body.String(), "secret-panic-value") {
		t.Fatal("panic value leaked")
	}
}

func TestRequestTimeoutAndMethodNotAllowed(t *testing.T) {
	cfg := testConfig(t)
	cfg.HTTP.RequestTimeout = 10 * time.Millisecond
	h := NewServer(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
	h.GET("/slow", func(ctx context.Context, c *app.RequestContext) { <-ctx.Done() })
	res := ut.PerformRequest(h.Engine, "GET", "/slow", nil)
	if res.Code != 504 {
		t.Fatalf("expected 504, got %d", res.Code)
	}
	res = ut.PerformRequest(h.Engine, "POST", "/healthz", nil)
	if res.Code != 405 || !strings.Contains(res.Body.String(), "method_not_allowed") {
		t.Fatalf("expected JSON 405, got %d %s", res.Code, res.Body)
	}
}

func TestTimeoutReplacesLateSuccessWithOneJSONEnvelope(t *testing.T) {
	cfg := testConfig(t)
	cfg.HTTP.RequestTimeout = 10 * time.Millisecond
	h := NewServer(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
	h.GET("/late", func(ctx context.Context, c *app.RequestContext) {
		<-ctx.Done()
		response.Success(ctx, c, "stale-success-data")
	})
	res := ut.PerformRequest(h.Engine, "GET", "/late", nil)
	var body response.Envelope
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("timeout must produce exactly one JSON document: %v; body=%s", err, res.Body)
	}
	if res.Code != 504 || body.Code != "timeout" || body.Data != nil || strings.Contains(res.Body.String(), "stale-success-data") {
		t.Fatalf("timeout did not replace success: %d %s", res.Code, res.Body)
	}
}
