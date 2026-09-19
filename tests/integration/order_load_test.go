package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/cloudwego/hertz/pkg/app/server"
)

// This is an opt-in closed-loop batch experiment, not a sustained capacity test.
// Keep V1 construction explicit so future V2 changes cannot silently replace the baseline.
func TestOrderHTTPLoadBaseline(t *testing.T) {
	if os.Getenv("RUN_LOAD") != "1" {
		t.Skip("set RUN_LOAD=1 and RUN_INTEGRATION=1; see docs/performance/seckill.md")
	}
	for _, concurrency := range []int{1, 16, 64} {
		for _, scenario := range []struct {
			name            string
			users, requests int
			stock           int64
		}{
			{"purchase", 300, 300, 300},
			{"oversubscribed", 200, 400, 100},
		} {
			t.Run(fmt.Sprintf("%s/c%d", scenario.name, concurrency), func(t *testing.T) {
				deps, ctx := authDependencies(t)
				users, vouchers := orderFixtures(t, deps, ctx, scenario.users, scenario.stock)
				cfg, err := config.Load()
				if err != nil {
					t.Fatal(err)
				}
				store := cache.NewAuth(deps.Redis)
				tokens := make([]string, len(users))
				t.Cleanup(func() {
					clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, token := range tokens {
						if token != "" {
							if err := store.DeleteSession(clean, token); err != nil {
								t.Error(err)
							}
						}
					}
				})
				for i, user := range users {
					raw := make([]byte, 32)
					if _, err := rand.Read(raw); err != nil {
						t.Fatal(err)
					}
					tokens[i] = base64.RawURLEncoding.EncodeToString(raw)
					if err := store.SaveSession(ctx, tokens[i], user.ID, 5*time.Minute); err != nil {
						t.Fatal(err)
					}
				}
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				listener := &loadReadyListener{Listener: ln, ready: make(chan struct{})}
				h := appserver.NewServer(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, server.WithListener(listener))
				handler.NewOrder(service.NewOrder(repository.NewOrder(deps.DB)), service.NewAuth(repository.NewUser(deps.DB), store, cfg.Auth)).Register(h)
				done := make(chan error, 1)
				go func() { done <- h.Run() }()
				select {
				case <-listener.ready:
				case err := <-done:
					_ = ln.Close()
					t.Fatalf("server startup: %v", err)
				case <-time.After(5 * time.Second):
					_ = ln.Close()
					t.Fatal("server startup timeout")
				}
				stopServer := sync.OnceFunc(func() {
					stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := h.Shutdown(stop); err != nil {
						t.Error(err)
					}
					_ = ln.Close()
					select {
					case err := <-done:
						if err != nil {
							t.Error(err)
						}
					case <-stop.Done():
						t.Error("server did not stop")
					}
				})
				t.Cleanup(stopServer)
				transport := &http.Transport{MaxConnsPerHost: concurrency, MaxIdleConnsPerHost: concurrency, MaxIdleConns: concurrency}
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
				t.Cleanup(transport.CloseIdleConnections)
				base := "http://" + ln.Addr().String()
				// Warm authenticated read paths without buying or changing the experiment's inventory.
				for i := range 20 {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/orders", nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+tokens[i%len(tokens)])
					res, err := client.Do(req)
					if err != nil {
						t.Fatal("warmup transport failed")
					}
					_, readErr := io.Copy(io.Discard, res.Body)
					_ = res.Body.Close()
					if res.StatusCode != 200 || readErr != nil {
						t.Fatalf("warmup status=%d read=%v", res.StatusCode, readErr)
					}
				}
				before := deps.SQL.Stats()
				url := fmt.Sprintf("%s/api/v1/vouchers/%d/seckill", base, vouchers[0].ID)
				loadCtx, cancelLoad := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancelLoad()
				samples, elapsed := runOrderLoad(loadCtx, scenario.requests, concurrency, func(i int) loadSample {
					owner := i % len(users)
					return loadOrderRequest(loadCtx, client, url, tokens[owner], users[owner].ID, vouchers[0].ID)
				})
				cancelLoad()
				after := deps.SQL.Stats()
				report := summarizeLoad(samples, elapsed)
				// Always retain completed HTTP measurements, including incomplete/failed batches,
				// before any database verification can fail. Unsent work is not counted as traffic.
				httpMeasurements, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("LOAD_HTTP_RESULT scenario=%s concurrency=%d planned=%d not_dispatched=%d report=%s", scenario.name, concurrency, scenario.requests, scenario.requests-len(samples), httpMeasurements)
				transport.CloseIdleConnections()
				stopServer()
				verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancelVerify()
				var rows []model.VoucherOrder
				if err := deps.DB.WithContext(verifyCtx).Where("voucher_id = ?", vouchers[0].ID).Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				var activity model.SeckillVoucher
				if err := deps.DB.WithContext(verifyCtx).First(&activity, "voucher_id = ?", vouchers[0].ID).Error; err != nil {
					t.Fatal(err)
				}
				owners := make(map[uint64]bool)
				orders := make(map[uint64]uint64)
				for _, row := range rows {
					if owners[row.UserID] {
						t.Fatalf("duplicate final user=%d", row.UserID)
					}
					owners[row.UserID] = true
					orders[row.ID] = row.UserID
				}
				seen := make(map[uint64]bool)
				for _, sample := range samples {
					if sample.code == "ok" {
						if seen[sample.orderID] || orders[sample.orderID] != sample.userID {
							t.Fatal("successful response does not match a unique committed order")
						}
						seen[sample.orderID] = true
					}
				}
				result := struct {
					Variant         string  `json:"variant"`
					Scenario        string  `json:"scenario"`
					Concurrency     int     `json:"concurrency"`
					InitialStock    int64   `json:"initial_stock"`
					FinalStock      int64   `json:"final_stock"`
					FinalOrders     int     `json:"final_orders"`
					DBPoolWaits     int64   `json:"db_pool_waits"`
					DBPoolWaitMS    float64 `json:"db_pool_wait_ms"`
					MySQLMaxOpen    int     `json:"mysql_max_open"`
					RedisPool       int     `json:"redis_pool"`
					GoVersion       string  `json:"go_version"`
					CPUs            int     `json:"cpus"`
					PlannedRequests int     `json:"planned_requests"`
					NotDispatched   int     `json:"not_dispatched"`
					loadReport
				}{"v1", scenario.name, concurrency, scenario.stock, activity.Stock, len(rows), after.WaitCount - before.WaitCount, float64(after.WaitDuration-before.WaitDuration) / float64(time.Millisecond), cfg.MySQL.MaxOpenConns, cfg.Redis.PoolSize, runtime.Version(), runtime.NumCPU(), scenario.requests, scenario.requests - len(samples), report}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("LOAD_RESULT %s", encoded)
				if activity.Stock < 0 || int64(len(rows)) > scenario.stock || activity.Stock+int64(len(rows)) != scenario.stock || report.Accepted != len(rows) {
					t.Fatal("stock/order/accepted invariant failed")
				}
				if report.Requests != scenario.requests || report.SystemErrors != 0 || int64(report.Accepted) != scenario.stock || activity.Stock != 0 {
					t.Fatal("experiment did not finish cleanly; see LOAD_RESULT")
				}
				if scenario.name == "oversubscribed" && (report.Codes["already_purchased"] != 100 || report.Codes["sold_out"] != 200) {
					t.Fatal("unexpected business rejection distribution")
				}
			})
		}
	}
}

type loadReadyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func runOrderLoad(ctx context.Context, requests, concurrency int, request func(int) loadSample) ([]loadSample, time.Duration) {
	samples := make([]loadSample, requests)
	var next atomic.Int64
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range concurrency {
		workers.Go(func() {
			<-start
			for {
				if ctx.Err() != nil {
					return
				}
				i := int(next.Add(1) - 1)
				if i >= len(samples) {
					return
				}
				samples[i] = request(i)
			}
		})
	}
	began := time.Now()
	close(start)
	workers.Wait()
	elapsed := time.Since(began)
	completed := samples[:0]
	for _, sample := range samples {
		if sample.code != "" {
			completed = append(completed, sample)
		}
	}
	return completed, elapsed
}

func (l *loadReadyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

func loadOrderRequest(ctx context.Context, client *http.Client, url, token string, userID, voucherID uint64) (sample loadSample) {
	began := time.Now()
	defer func() { sample.latency = time.Since(began) }()
	sample.code = "transport_error"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return sample
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return sample
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if err != nil {
		return sample
	}
	var envelope struct {
		Code      string
		RequestID string `json:"request_id"`
		Data      service.OrderView
	}
	sample.code = "invalid_response"
	if json.Unmarshal(body, &envelope) != nil || envelope.RequestID == "" || res.Header.Get("Cache-Control") != "no-store" {
		return sample
	}
	if res.StatusCode == 200 && envelope.Code == "ok" && envelope.Data.ID > 0 && envelope.Data.UserID == userID && envelope.Data.VoucherID == voucherID && envelope.Data.Status == 1 {
		sample.code, sample.orderID, sample.userID = "ok", envelope.Data.ID, userID
	} else if res.StatusCode == 409 && (envelope.Code == "sold_out" || envelope.Code == "already_purchased") {
		sample.code = envelope.Code
	} else if res.StatusCode >= 500 {
		sample.code = fmt.Sprintf("http_%d_%s", res.StatusCode, envelope.Code)
	}
	return sample
}
