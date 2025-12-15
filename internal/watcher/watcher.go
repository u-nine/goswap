package watcher

import (
	"crypto/md5"
	"encoding/hex"
	"io"
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
	onChange   func([]Event)

	fsWatcher *fsnotify.Watcher
	done      chan struct{}
	mu        sync.Mutex
	hashes    map[string]string
}

// New creates a new file watcher
func New(root string, extensions, excludes []string, delay time.Duration, onChange func([]Event)) (*Watcher, error) {
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
		hashes:     make(map[string]string),
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

		// Calculate initial hash for files
		if !info.IsDir() && w.hasValidExtension(path) {
			hash, err := w.calculateHash(path)
			if err == nil {
				w.mu.Lock()
				w.hashes[path] = hash
				w.mu.Unlock()
			}
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
		timer         *time.Timer
		pendingEvents []Event
		pendingPaths  = make(map[string]bool)
		timerActive   bool
	)

	for {
		var timerC <-chan time.Time
		if timerActive && timer != nil {
			timerC = timer.C
		}

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
			isDir := err == nil && info.IsDir()

			if isDir {
				// New directory created, watch it
				if event.Op&fsnotify.Create == fsnotify.Create {
					w.fsWatcher.Add(event.Name)
				}
				continue
			}

			if !w.hasValidExtension(event.Name) {
				continue
			}

			// Check content hash
			// For writes, check if content actually changed
			if event.Op&fsnotify.Write == fsnotify.Write {
				newHash, err := w.calculateHash(event.Name)
				if err != nil {
					// If error (e.g. file deleted quickly), treat as change
				} else {
					w.mu.Lock()
					oldHash, exists := w.hashes[event.Name]
					if exists && oldHash == newHash {
						w.mu.Unlock()
						continue // Content didn't change
					}
					w.hashes[event.Name] = newHash
					w.mu.Unlock()
				}
			} else if event.Op&fsnotify.Create == fsnotify.Create {
				newHash, err := w.calculateHash(event.Name)
				if err == nil {
					w.mu.Lock()
					w.hashes[event.Name] = newHash
					w.mu.Unlock()
				}
			} else if event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename {
				w.mu.Lock()
				delete(w.hashes, event.Name)
				w.mu.Unlock()
			}

			// Add to pending events
			if !pendingPaths[event.Name] {
				pendingEvents = append(pendingEvents, Event{
					Path: event.Name,
					Op:   event.Op,
					Time: time.Now(),
				})
				pendingPaths[event.Name] = true
			}

			// Reset timer
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(w.delay)
			timerActive = true

		case <-timerC:
			timerActive = false
			if len(pendingEvents) > 0 {
				w.onChange(pendingEvents)
				pendingEvents = nil
				pendingPaths = make(map[string]bool)
			}

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			// Log error but continue watching
			_ = err
		}
	}
}

// calculateHash computes the MD5 hash of a file
func (w *Watcher) calculateHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
