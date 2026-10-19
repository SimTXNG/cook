package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher provides high-performance, debounced recursive file watching.
type Watcher struct {
	fsWatcher    *fsnotify.Watcher
	rootDir      string
	debounce     time.Duration
	ignoredDirs  map[string]bool
	changesCh    chan []string
	errorsCh     chan error
	closeCh      chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
	pendingFiles map[string]struct{}
	timer        *time.Timer
}

// NewWatcher initializes a new recursive directory watcher with debouncing.
func NewWatcher(rootDir string, debounce time.Duration, extraIgnoredDirs []string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if debounce <= 0 {
		debounce = 150 * time.Millisecond
	}

	ignored := map[string]bool{
		".git":         true,
		".github":      true,
		".idea":        true,
		".vscode":      true,
		"node_modules": true,
		"dist":         true,
		"bin":          true,
		"build":        true,
		"tmp":          true,
		"frontend":     true, // Handled natively by Vite HMR!
	}
	for _, dir := range extraIgnoredDirs {
		base := filepath.Base(dir)
		ignored[base] = true
		ignored[dir] = true
	}

	w := &Watcher{
		fsWatcher:    fsWatcher,
		rootDir:      rootDir,
		debounce:     debounce,
		ignoredDirs:  ignored,
		changesCh:    make(chan []string, 10),
		errorsCh:     make(chan error, 10),
		closeCh:      make(chan struct{}),
		pendingFiles: make(map[string]struct{}),
	}

	if err := w.watchRecursive(rootDir); err != nil {
		_ = fsWatcher.Close()
		return nil, err
	}

	go w.eventLoop()
	return w, nil
}

// Changes returns the channel delivering batches of changed file paths.
func (w *Watcher) Changes() <-chan []string {
	return w.changesCh
}

// Errors returns the channel delivering watcher errors.
func (w *Watcher) Errors() <-chan error {
	return w.errorsCh
}

// reportError delivers err without dropping it. It returns false once the
// watcher is closed, telling the caller to stop.
func (w *Watcher) reportError(err error) bool {
	select {
	case w.errorsCh <- err:
		return true
	case <-w.closeCh:
		return false
	}
}

// Close terminates the file watcher. It is safe to call more than once.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.closeCh)

		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
		w.mu.Unlock()

		err = w.fsWatcher.Close()
	})
	return err
}

func (w *Watcher) watchRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("failed to walk %s: %w", path, err)
		}
		if !info.IsDir() {
			return nil
		}

		name := info.Name()
		if name != "." && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		if w.ignoredDirs[name] {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(w.rootDir, path)
		if err == nil && w.ignoredDirs[rel] {
			return filepath.SkipDir
		}

		return w.fsWatcher.Add(path)
	})
}

func (w *Watcher) eventLoop() {
	for {
		select {
		case <-w.closeCh:
			return
		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			if !w.reportError(err) {
				return
			}
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Stale watches on renamed/deleted paths must be dropped; on Linux
			// the inotify watch is removed automatically, on other backends it
			// may linger, so remove it explicitly.
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				_ = w.fsWatcher.Remove(event.Name)
			}

			// If a new directory was created, watch it automatically
			if event.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					base := filepath.Base(event.Name)
					if !strings.HasPrefix(base, ".") && !w.ignoredDirs[base] {
						if err := w.watchRecursive(event.Name); err != nil {
							if !w.reportError(err) {
								return
							}
						}
					}
					continue
				}
			}

			// Filter only relevant source files (.go)
			if !isWatchableFile(event.Name) {
				continue
			}

			w.mu.Lock()
			w.pendingFiles[event.Name] = struct{}{}
			if w.timer != nil {
				w.timer.Stop()
			}
			w.timer = time.AfterFunc(w.debounce, w.flushChanges)
			w.mu.Unlock()
		}
	}
}

func (w *Watcher) flushChanges() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.pendingFiles) == 0 {
		return
	}

	files := make([]string, 0, len(w.pendingFiles))
	for f := range w.pendingFiles {
		files = append(files, f)
	}
	w.pendingFiles = make(map[string]struct{})

	select {
	case w.changesCh <- files:
	case <-w.closeCh:
	}
}

func isWatchableFile(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "#") || strings.HasSuffix(base, "~") {
		return false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	// Focus on Go source files
	return strings.HasSuffix(base, ".go")
}
