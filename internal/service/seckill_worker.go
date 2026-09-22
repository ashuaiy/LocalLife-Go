package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RunWorker is synchronous: the caller waits for it before closing pools. Redis
// deliveries are at least once; SQL Apply and compensation provide idempotency.
func (s *AsyncOrder) RunWorker(ctx context.Context, consumer string, logger *slog.Logger) error {
	if consumer == "" || logger == nil {
		return errors.New("worker requires a consumer name and logger")
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	report := time.NewTicker(30 * time.Second)
	defer report.Stop()
	cursors := make(map[uint64]string)
	var after uint64
	var processed, retries, queueErrors uint64
	stats := func() {
		logger.Info("seckill worker counters", "consumer", consumer, "processed_deliveries", processed, "retry_deliveries", retries, "queue_errors", queueErrors)
	}
	defer stats()
	for ctx.Err() == nil {
		query, stop := context.WithTimeout(ctx, 5*time.Second)
		ids, err := s.orders.ActiveIDs(query, after, 100)
		stop()
		if err != nil {
			queueErrors++
			logger.Error("seckill activity scan failed", "error", err)
		} else {
			if len(ids) < 100 {
				after = 0
			} else {
				after = ids[len(ids)-1]
			}
			for _, id := range ids {
				if ctx.Err() != nil {
					break
				}
				cursor := cursors[id]
				if cursor == "" {
					cursor = "0-0"
				}
				claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				pending, next, claimErr := s.queue.Claim(claimCtx, id, consumer, cursor, 30*time.Second, 20)
				cancel()
				if next != "" {
					cursors[id] = next
				}
				if claimErr != nil {
					queueErrors++
					logger.Error("seckill pending recovery failed", "voucher_id", id, "error", claimErr)
				}
				readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				fresh, readErr := s.queue.Read(readCtx, id, consumer, 20)
				cancel()
				if readErr != nil {
					queueErrors++
					logger.Error("seckill queue read failed", "voucher_id", id, "error", readErr)
				}
				for _, event := range append(pending, fresh...) {
					if ctx.Err() != nil {
						break
					}
					delivery, cancel := context.WithTimeout(ctx, 5*time.Second)
					err := s.Process(delivery, event)
					cancel()
					if err != nil {
						retries++
						logger.Error("seckill delivery retained for retry", "voucher_id", id, "event_id", event.ID, "error", err)
					} else {
						processed++
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-report.C:
			stats()
		case <-tick.C:
		}
	}
	return nil
}
