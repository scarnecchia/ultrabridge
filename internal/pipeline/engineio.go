package pipeline

import (
	"context"
)

// runEngineIOListener watches for inbound Engine.IO events and enqueues
// affected note paths. The specific event names and payload format must be
// verified by observing live traffic (see Task 1 investigation step).
//
// If supernote-service emits no useful sync-complete events, extractNotePaths
// returns nil and file detection falls back to the watcher and reconciler.
func (p *Pipeline) runEngineIOListener(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-p.events:
			if !ok {
				return
			}
			for _, path := range extractNotePaths(msg) {
				p.enqueue(ctx, path)
			}
		}
	}
}

// extractNotePaths parses an inbound Engine.IO frame and returns any note
// file paths that should be queued for processing.
//
// STUB: Replace with real parsing once event names are confirmed via Task 1
// investigation. Currently returns nil so no paths are enqueued from Engine.IO.
func extractNotePaths(_ []byte) []string {
	// TODO: implement after investigating live Engine.IO traffic.
	// Expected frame format (unverified): 42["EventName",{"filePath":"..."}]
	return nil
}
