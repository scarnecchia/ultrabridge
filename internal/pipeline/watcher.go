package pipeline

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceDelay = 2 * time.Second

func (p *Pipeline) runWatcher(ctx context.Context) {
	if p.notesPath == "" {
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		p.logger.Error("watcher create failed", "err", err)
		return
	}
	defer watcher.Close()

	// fsnotify is NOT recursive — add every subdirectory explicitly at startup.
	if err := p.addDirsRecursive(watcher, p.notesPath); err != nil {
		p.logger.Warn("watcher add dirs failed", "err", err)
	}

	// Debouncer: per-path timer to coalesce rapid writes into a single enqueue.
	var mu sync.Mutex
	timers := make(map[string]*time.Timer)

	debounce := func(path string) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := timers[path]; ok {
			t.Reset(debounceDelay)
			return
		}
		timers[path] = time.AfterFunc(debounceDelay, func() {
			mu.Lock()
			delete(timers, path)
			mu.Unlock()
			p.enqueue(ctx, path)
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Chmod) {
				continue // ignore attribute-only changes (common from antivirus etc.)
			}
			// If a new directory is created, watch it immediately.
			if event.Has(fsnotify.Create) {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					watcher.Add(event.Name)
				}
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Rename) {
				debounce(event.Name)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			p.logger.Warn("watcher error", "err", err)
		}
	}
}

// addDirsRecursive adds every directory under root to the watcher.
func (p *Pipeline) addDirsRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			w.Add(path)
		}
		return nil
	})
}
