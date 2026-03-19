package pipeline

import (
	"context"
	"time"
)

const reconcileInterval = 15 * time.Minute

func (p *Pipeline) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.reconcile(ctx)
		}
	}
}

func (p *Pipeline) reconcile(ctx context.Context) {
	changed, err := p.store.Scan(ctx)
	if err != nil {
		p.logger.Warn("reconciler scan failed", "err", err)
		return
	}
	for _, path := range changed {
		p.enqueue(ctx, path)
	}
}
