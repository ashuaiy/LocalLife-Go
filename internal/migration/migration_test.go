package migration

import (
	"context"
	"errors"
	"testing"

	"github.com/ashuaiy/local-life-go/internal/config"
)

func TestCanceledMigrationDoesNotConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Up(ctx, config.Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
