package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"testing"
)

func TestInvalidCommandStopsBeforeOpeningDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, args := range [][]string{nil, {"-mode", "enable"}, {"-mode", "unknown"}, {"-mode", "worker", "-voucher-id", "3"}, {"-mode", "enable", "-voucher-id", "3", "extra"}} {
		if err := run(context.Background(), args, logger); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run(context.Background(), []string{"-h"}, logger); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}
