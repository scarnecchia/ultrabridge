package processor

import (
	"context"
	"time"
)

const (
	watchdogInterval = 1 * time.Minute
	stuckJobTimeout  = 10 * time.Minute
)

// watchdog periodically reclaims jobs that have been in_progress too long.
func (s *Store) watchdog(ctx context.Context) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reclaimStuck(ctx)
		}
	}
}

func (s *Store) reclaimStuck(ctx context.Context) {
	cutoff := time.Now().Add(-stuckJobTimeout).Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status=?, started_at=NULL, attempts=attempts+1
		WHERE status=? AND started_at < ?`,
		StatusPending, StatusInProgress, cutoff,
	)
	if err != nil {
		s.logger.Error("failed to reclaim stuck jobs", "error", err)
	}
}
