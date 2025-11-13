package fswatch

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Logger is a minimal logging interface used by the watcher.
type Logger interface {
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

// Watcher watches a directory tree and debounces change notifications.
type Watcher struct {
	dir      string
	debounce time.Duration
	watcher  *fsnotify.Watcher
	logger   Logger
	mu       sync.Mutex
	watched  map[string]struct{}
}

// New creates a new recursive watcher rooted at dir.
func New(dir string, debounce time.Duration, logger Logger) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		dir:      dir,
		debounce: debounce,
		watcher:  w,
		logger:   logger,
		watched:  make(map[string]struct{}),
	}, nil
}

// Start begins processing file events until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context, onChange func()) error {
	if err := w.addRecursive(w.dir); err != nil {
		return err
	}

	go w.run(ctx, onChange)
	return nil
}

func (w *Watcher) run(ctx context.Context, onChange func()) {
	defer w.watcher.Close()

	var timer *time.Timer
	var timerC <-chan time.Time

	trigger := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(w.debounce)
		timerC = timer.C
	}

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
			trigger()
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				w.logger.Warn("watcher error", "err", err)
			}
		case <-timerC:
			timer = nil
			timerC = nil
			onChange()
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Create == fsnotify.Create {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			if err := w.addRecursive(event.Name); err != nil {
				w.logger.Warn("failed to watch new directory", "path", event.Name, "err", err)
			}
		}
	}
}

func (w *Watcher) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		return w.addWatch(path)
	})
}

func (w *Watcher) addWatch(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[path]; ok {
		return nil
	}
	if err := w.watcher.Add(path); err != nil {
		return err
	}
	w.watched[path] = struct{}{}
	w.logger.Debug("watching directory", "path", path)
	return nil
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	return w.watcher.Close()
}
