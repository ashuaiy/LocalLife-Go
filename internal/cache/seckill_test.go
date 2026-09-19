package cache

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

func TestSeckillReplyMapping(t *testing.T) {
	for _, code := range []string{"activity_not_started", "activity_ended", "sold_out", "already_purchased", "not_found"} {
		_, err := decodeSeckillReply([]any{code}, 7, 8)
		_, got, _ := apperror.Describe(err)
		if got != code {
			t.Fatalf("want=%s got=%s", code, got)
		}
	}
	for _, reply := range [][]any{nil, {1}, {"unknown"}, {"accepted"}, {"accepted", "bad", strings.Repeat("a", 32), "1"}, {"accepted", "1-0", "bad", "1"}, {"accepted", "1-0", strings.Repeat("a", 32), "9007199254740992"}, {"accepted", "1-0", strings.Repeat("a", 32), "-1"}, {"accepted", "1-0", strings.Repeat("a", 32), int64(1)}, {"sold_out", "unexpected"}} {
		_, err := decodeSeckillReply(reply, 7, 8)
		status, _, _ := apperror.Describe(err)
		if status != 503 {
			t.Fatalf("reply=%v status=%d", reply, status)
		}
	}
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	event, err := decodeSeckillReply([]any{"accepted", "1789652961000-1", strings.Repeat("a", 32), strconv.FormatInt(stamp.UnixMilli(), 10)}, ^uint64(0), ^uint64(0)-1)
	if err != nil || event.VoucherID != ^uint64(0) || event.UserID != ^uint64(0)-1 || !event.AcceptedAt.Equal(stamp) {
		t.Fatalf("event=%+v err=%v", event, err)
	}
}
