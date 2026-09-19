package cache

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestSignMonthBoundaryAndGap(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION") != "1" {
		t.Skip("requires isolated Redis")
	}
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDR"), Password: os.Getenv("REDIS_PASSWORD")})
	defer client.Close()
	// Derive a unique unsigned user key without relying on shared SQL fixtures.
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	var id uint64
	for _, v := range b {
		id = id<<8 | uint64(v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := NewCommunity(client)
	now := time.Date(2031, 1, 29, 16, 0, 0, 0, time.UTC) // Jan 30 in Shanghai.
	s.now = func() time.Time { return now }
	defer func() {
		for _, month := range []string{"203101", "203102"} {
			client.Del(context.Background(), fmt.Sprintf("locallife:sign:%d:%s", id, month))
		}
	}()
	for day := 30; day <= 31; day++ {
		now = time.Date(2031, 1, day, 1, 0, 0, 0, signZone)
		got, err := s.Sign(ctx, id, true)
		if err != nil || got.Streak != day-29 {
			t.Fatalf("day=%d state=%+v err=%v", day, got, err)
		}
	}
	now = time.Date(2031, 2, 1, 1, 0, 0, 0, signZone)
	got, err := s.Sign(ctx, id, false)
	if err != nil || got.Signed || got.Streak != 0 {
		t.Fatalf("new month=%+v %v", got, err)
	}
	got, err = s.Sign(ctx, id, true)
	if err != nil || got.Streak != 1 {
		t.Fatalf("new month signed=%+v %v", got, err)
	}
	now = now.AddDate(0, 0, 2)
	got, err = s.Sign(ctx, id, true)
	if err != nil || got.Streak != 1 {
		t.Fatalf("gap=%+v %v", got, err)
	}
}
