package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

type loadSample struct {
	latency time.Duration
	code    string
	orderID uint64
	userID  uint64
}

type loadReport struct {
	Requests         int            `json:"requests"`
	ElapsedMS        float64        `json:"elapsed_ms"`
	QPS              float64        `json:"qps"`
	P95MS            float64        `json:"p95_ms"`
	P99MS            float64        `json:"p99_ms"`
	Accepted         int            `json:"accepted"`
	BusinessRejected int            `json:"business_rejected"`
	SystemErrors     int            `json:"system_errors"`
	SystemErrorRate  float64        `json:"system_error_rate"`
	NonSuccessRate   float64        `json:"non_success_rate"`
	Codes            map[string]int `json:"codes"`
}

func summarizeLoad(samples []loadSample, elapsed time.Duration) loadReport {
	r := loadReport{Requests: len(samples), ElapsedMS: float64(elapsed) / float64(time.Millisecond), Codes: make(map[string]int)}
	if len(samples) == 0 {
		return r
	}
	latencies := make([]time.Duration, len(samples))
	for i, sample := range samples {
		latencies[i] = sample.latency
		r.Codes[sample.code]++
		switch sample.code {
		case "ok":
			r.Accepted++
		case "already_purchased", "sold_out":
			r.BusinessRejected++
		default:
			r.SystemErrors++
		}
	}
	slices.Sort(latencies)
	// Nearest-rank percentile: ceil(percent*n/100), using a one-based rank.
	r.P95MS = float64(latencies[(95*len(latencies)+99)/100-1]) / float64(time.Millisecond)
	r.P99MS = float64(latencies[(99*len(latencies)+99)/100-1]) / float64(time.Millisecond)
	if elapsed > 0 {
		r.QPS = float64(len(samples)) / elapsed.Seconds()
	}
	r.SystemErrorRate = float64(r.SystemErrors) / float64(len(samples))
	r.NonSuccessRate = float64(len(samples)-r.Accepted) / float64(len(samples))
	return r
}

func TestLoadSummaryCountsAndNearestRank(t *testing.T) {
	samples := make([]loadSample, 100)
	for i := range samples {
		samples[i] = loadSample{latency: time.Duration(100-i) * time.Millisecond, code: "ok"}
	}
	samples[0].code = "already_purchased"
	samples[1].code = "sold_out"
	samples[2].code = "transport_error"
	samples[3].code = "timeout"
	s := summarizeLoad(samples, 2*time.Second)
	if s.Requests != 100 || s.QPS != 50 || s.P95MS != 95 || s.P99MS != 99 || s.ElapsedMS != 2000 {
		t.Fatalf("incorrect load measurements: %+v", s)
	}
	if s.Accepted != 96 || s.BusinessRejected != 2 || s.SystemErrors != 2 || s.SystemErrorRate != .02 || s.NonSuccessRate != .04 || s.Codes["ok"] != 96 {
		t.Fatalf("incorrect outcome classification: %+v", s)
	}
	if samples[0].latency != 100*time.Millisecond {
		t.Fatal("summary mutated input samples")
	}
}

func TestLoadSummarySmallSamples(t *testing.T) {
	for _, size := range []int{1, 2, 3} {
		samples := make([]loadSample, size)
		for i := range samples {
			samples[i] = loadSample{latency: time.Duration(i+1) * time.Millisecond, code: "ok"}
		}
		s := summarizeLoad(samples, time.Second)
		if s.P95MS != float64(size) || s.P99MS != float64(size) {
			t.Fatalf("size=%d report=%+v", size, s)
		}
	}
	s := summarizeLoad(nil, time.Second)
	if s.Requests != 0 || s.QPS != 0 || s.SystemErrorRate != 0 {
		t.Fatalf("empty=%+v", s)
	}
}

func TestLoadOrderRequestValidatesOutcome(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"created", 200, `{"code":"ok","request_id":"test","data":{"id":"9","user_id":"7","voucher_id":"8","status":1}}`, "ok"},
		{"other_owner", 200, `{"code":"ok","request_id":"test","data":{"id":"9","user_id":"6","voucher_id":"8","status":1}}`, "invalid_response"},
		{"other_voucher", 200, `{"code":"ok","request_id":"test","data":{"id":"9","user_id":"7","voucher_id":"6","status":1}}`, "invalid_response"},
		{"duplicate", 409, `{"code":"already_purchased","request_id":"test","data":null}`, "already_purchased"},
		{"sold_out", 409, `{"code":"sold_out","request_id":"test","data":null}`, "sold_out"},
		{"unexpected_rejection", 409, `{"code":"activity_ended","request_id":"test","data":null}`, "invalid_response"},
		{"http_code_mismatch", 200, `{"code":"sold_out","request_id":"test","data":null}`, "invalid_response"},
		{"dependency", 503, `{"code":"dependency_failure","request_id":"test","data":null}`, "http_503_dependency_failure"},
		{"malformed", 200, `not json`, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("incorrect load request")
				}
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			sample := loadOrderRequest(context.Background(), srv.Client(), srv.URL, "test-token", 7, 8)
			if sample.code != tc.want {
				t.Fatalf("sample=%+v", sample)
			}
			if tc.want == "ok" && (sample.orderID != 9 || sample.userID != 7) {
				t.Fatal("lost successful identity")
			}
		})
	}
}

func TestLoadDispatchStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := 0
	samples, _ := runOrderLoad(ctx, 100, 1, func(int) loadSample {
		called++
		cancel()
		return loadSample{code: "transport_error"}
	})
	if called != 1 || len(samples) != 1 {
		t.Fatalf("canceled batch dispatched=%d recorded=%d, want 1", called, len(samples))
	}
	samples, _ = runOrderLoad(ctx, 100, 4, func(int) loadSample {
		t.Error("dispatched a request with an already canceled batch")
		return loadSample{code: "transport_error"}
	})
	if len(samples) != 0 {
		t.Fatalf("recorded %d unsent requests", len(samples))
	}
}
