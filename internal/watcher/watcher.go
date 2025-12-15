package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event represents a file change event
type Event struct {
	Path string
	Op   fsnotify.Op
	Time time.Time
}

// Watcher monitors file system changes
type Watcher struct {
	root       string
	extensions []string
	excludes   []string
	delay      time.Duration
	onChange   func(Event)

	fsWatcher *fsnotify.Watcher
	done      chan struct{}
	mu        sync.Mutex
	lastEvent time.Time
}

// New creates a new file watcher
func New(root string, extensions, excludes []string, delay time.Duration, onChange func(Event)) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:       root,
		extensions: extensions,
		excludes:   excludes,
		delay:      delay,
		onChange:   onChange,
		fsWatcher:  fsWatcher,
		done:       make(chan struct{}),
	}

	return w, nil
}

// Start begins watching for file changes
func (w *Watcher) Start() error {
	// Add directories to watch
	if err := w.addDirs(); err != nil {
		return err
	}

	go w.loop()
	return nil
}

// Stop stops watching for file changes
func (w *Watcher) Stop() {
	close(w.done)
	w.fsWatcher.Close()
}

// addDirs walks the root directory and adds all directories to the watcher
func (w *Watcher) addDirs() error {
	return filepath.Walk(w.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip excluded directories
		if info.IsDir() && w.isExcluded(path) {
			return filepath.SkipDir
		}

		// Watch directories
		if info.IsDir() {
			return w.fsWatcher.Add(path)
		}

		return nil
	})
}

// isExcluded checks if a path should be excluded from watching
func (w *Watcher) isExcluded(path string) bool {
	relPath, err := filepath.Rel(w.root, path)
	if err != nil {
		relPath = path
	}

	for _, exclude := range w.excludes {
		if strings.Contains(relPath, exclude) {
			return true
		}
	}
	return false
}

// hasValidExtension checks if the file has a valid extension
func (w *Watcher) hasValidExtension(path string) bool {
	if len(w.extensions) == 0 {
		return true
	}

	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	for _, validExt := range w.extensions {
		if ext == validExt {
			return true
		}
	}
	return false
}

// loop handles file system events
func (w *Watcher) loop() {
	var (
		timer    *time.Timer
		lastPath string
	)

	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Skip excluded paths and invalid extensions
			if w.isExcluded(event.Name) {
				continue
			}

			// Check if it's a relevant file
			info, err := os.Stat(event.Name)
			if err == nil && info.IsDir() {
				// New directory created, watch it
				if event.Op&fsnotify.Create == fsnotify.Create {
					w.fsWatcher.Add(event.Name)
				}
				continue
			}

			if !w.hasValidExtension(event.Name) {
				continue
			}

			// Debounce: reset timer on each event
			lastPath = event.Name
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(w.delay, func() {
				w.mu.Lock()
				defer w.mu.Unlock()

				w.onChange(Event{
					Path: lastPath,
					Op:   event.Op,
					Time: time.Now(),
				})
			})

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			// Log error but continue watching
			_ = err
		}
	}
}
