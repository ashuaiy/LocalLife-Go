package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeReturnsBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := testConfig(t)
	cfg.HTTP.Addr = ln.Addr().String()
	err = Serve(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err == nil {
		t.Fatal("occupied port must fail startup")
	}
}

func TestServeStopsAfterCancellation(t *testing.T) {
	// A pre-bound listener avoids guessing ports or sleeping to detect startup.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveListener(ctx, testConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)), nil, ln) }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	res, err := client.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server failed to shut down")
	}
	if _, err = client.Get("http://" + ln.Addr().String() + "/healthz"); err == nil {
		t.Fatal("listener remains open")
	}
}

func TestServeAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Serve(ctx, testConfig(t), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// This gate models the serve goroutine being scheduled after both readiness and
// cancellation become observable, while a real HTTP request is still in flight.
type gatedCancellation struct {
	context.Context
	started <-chan struct{}
	cancel  context.CancelFunc
}

func (c gatedCancellation) Done() <-chan struct{} {
	<-c.started
	c.cancel()
	return c.Context.Done()
}

func TestStartupCancellationDrainsInFlightRequest(t *testing.T) {
	for i := 0; i < 20; i++ {
		cfg := testConfig(t)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		started, release := make(chan struct{}), make(chan struct{})
		base, cancel := context.WithCancel(context.Background())
		ctx := gatedCancellation{Context: base, started: started, cancel: cancel}
		done := make(chan error, 1)
		go func() {
			done <- serveListener(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), map[string]Check{
				"mysql": func(ctx context.Context) error {
					close(started)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
			}, ln)
		}()
		requestDone := make(chan error, 1)
		go func() {
			client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			res, err := client.Get("http://" + ln.Addr().String() + "/readyz")
			if err == nil {
				_, err = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}
			requestDone <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			_ = ln.Close()
			t.Fatal("request never started")
		}
		earlyReturn := false
		select {
		case <-done:
			earlyReturn = true
		case <-time.After(20 * time.Millisecond):
		}
		close(release)
		cancel()
		if err := <-requestDone; err != nil {
			t.Fatal(err)
		}
		if earlyReturn {
			t.Fatal("Serve returned before the in-flight request completed")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("drained server did not exit")
		}
	}
}
