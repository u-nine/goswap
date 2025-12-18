package coordinator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/u-nine/goswap/internal/builder"
	"github.com/u-nine/goswap/internal/process"
	"github.com/u-nine/goswap/internal/proxy"
	"github.com/u-nine/goswap/internal/watcher"
	"github.com/u-nine/goswap/pkg/config"
)

// Coordinator orchestrates all components for zero-downtime hot reload
type Coordinator struct {
	cfg     *config.Config
	builder *builder.Builder
	proxy   *proxy.Proxy
	procMgr *process.Manager
	watcher *watcher.Watcher

	ctx    context.Context
	cancel context.CancelFunc

	buildTrigger chan struct{}
}

// New creates a new coordinator
func New(cfg *config.Config) *Coordinator {
	ctx, cancel := context.WithCancel(context.Background())

	return &Coordinator{
		cfg:          cfg,
		ctx:          ctx,
		cancel:       cancel,
		buildTrigger: make(chan struct{}, 1),
	}
}

// Run starts the coordinator and all its components
func (c *Coordinator) Run() error {
	c.log("info", "Starting go-swap...")
	c.log("info", "Working directory: %s", c.cfg.Root)

	// Initialize components
	if err := c.init(); err != nil {
		return err
	}

	// Perform initial build
	c.log("info", "Performing initial build...")
	result := c.builder.Build(c.ctx)
	if result.Status != builder.StatusSuccess {
		return fmt.Errorf("initial build failed: %v", result.Error)
	}

	// Clean up any stale binaries from previous runs, but keep the one we just built
	c.cleanupBinaries(c.builder.Binary())

	// Start initial process
	proc, err := c.procMgr.Start(c.ctx, c.builder.Binary())
	if err != nil {
		return fmt.Errorf("failed to start initial process: %w", err)
	}
	c.procMgr.SetCurrent(proc)

	// Set proxy upstream
	if err := c.proxy.SetUpstream("127.0.0.1", proc.Port); err != nil {
		return fmt.Errorf("failed to set proxy upstream: %w", err)
	}

	// Start file watcher
	if err := c.watcher.Start(); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Start proxy server in a goroutine
	go func() {
		if err := c.proxy.Start(c.ctx); err != nil {
			c.log("error", "Proxy server error: %v", err)
		}
	}()

	// Start build loop
	go c.buildLoop()

	c.log("success", "go-swap is running!")
	c.log("info", "Listening on http://localhost:%d", c.cfg.Proxy.Port)
	c.log("info", "Press Ctrl+C to stop")
	c.log("info", "Type 'rebuild' or 'r' and press Enter to trigger rebuild")

	// Start command line input handler
	go c.handleCommandInput()

	// Start signal handler for manual rebuild (SIGHUP and file-based)
	go c.handleRebuildSignal()

	// Wait for interrupt signal
	c.waitForShutdown()

	return nil
}

// init initializes all components
func (c *Coordinator) init() error {
	// Ensure tmp directory exists
	tmpDir := filepath.Join(c.cfg.Root, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create tmp directory: %w", err)
	}

	// Resolve binary path
	binPath := c.cfg.Build.Bin
	if !filepath.IsAbs(binPath) {
		binPath = filepath.Join(c.cfg.Root, binPath)
	}

	// Initialize builder
	c.builder = builder.New(
		c.cfg.Build.Cmd,
		binPath,
		c.cfg.Root,
		c.cfg.Log.Color,
	)

	// Initialize proxy
	c.proxy = proxy.New(c.cfg.Proxy.Port, c.cfg.Log.Color)

	// Initialize process manager
	c.procMgr = process.New(c.cfg.Process.StartPort, c.cfg.Log.Color)

	// Initialize watcher
	delay := time.Duration(c.cfg.Build.Delay) * time.Millisecond
	w, err := watcher.New(
		c.cfg.Root,
		c.cfg.Watch.Extensions,
		c.cfg.Watch.Exclude,
		delay,
		c.onFileChange,
	)
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	c.watcher = w

	return nil
}

// onFileChange handles file change events
// Note: File changes no longer trigger automatic rebuilds.
// Users must manually trigger rebuilds via signal (SIGHUP on Unix).
func (c *Coordinator) onFileChange(events []watcher.Event) {
	if len(events) == 0 {
		return
	}
	c.log("info", "Detected changes in %d files", len(events))
	for _, e := range events {
		c.log("info", "  - %s", e.Path)
	}
	c.log("info", "Files changed. Type 'rebuild' or 'r' and press Enter to trigger rebuild")
}

// buildLoop handles the serialized build process
func (c *Coordinator) buildLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.buildTrigger:
			// Drain any pending triggers that arrived while we were waiting?
			// No, the channel has buffer 1, so at most one pending.
			// But we want to ensure we clear it so we don't build twice if one came fast?
			// The logic:
			// 1. We got a trigger.
			// 2. Perform build.
			// 3. Loop back.
			// If a new trigger arrived DURING build, it will be in the channel.
			// Next loop will pick it up and rebuild. This is correct behavior.
			// If 10 triggers arrived, channel buffer 1 + 1 blocked sender?
			// onFileChange: select case default -> drops if full.
			// So if we are building, and 5 triggers come:
			// 1st fills channel. 2nd-5th drop.
			// After build finishes, we pick up the one in channel => Rebuild once.
			// This is PERFECT coalescing.

			c.rebuild()
		}
	}
}

func (c *Coordinator) rebuild() {
	c.log("info", "Starting rebuild...")

	// Build new version
	result := c.builder.Build(c.ctx)
	if result.Status != builder.StatusSuccess {
		c.log("error", "Rebuild failed, keeping current version running")

		// Output detailed failure reason
		if result.Error != nil {
			c.log("error", "Build error: %v", result.Error)
		}

		// Output build output (contains compiler errors/warnings)
		// Builder already logs this, but we also log it here for clarity at coordinator level
		if result.Output != "" {
			c.log("error", "Compilation errors:")
			lines := strings.Split(strings.TrimSpace(result.Output), "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					c.log("error", "  %s", trimmed)
				}
			}
		}

		c.log("info", "Old service continues running. Please fix the errors above and try again")
		return
	}

	// Start new process
	newProc, err := c.procMgr.Start(c.ctx, c.builder.Binary())
	if err != nil {
		c.log("error", "Failed to start new process: %v", err)
		return
	}

	// Get old process before switching
	oldProc := c.procMgr.Current()

	// Switch proxy to new process
	if err := c.proxy.SetUpstream("127.0.0.1", newProc.Port); err != nil {
		c.log("error", "Failed to switch upstream: %v", err)
		// Stop the new process since we couldn't switch
		c.procMgr.Stop(newProc, 5*time.Second)
		return
	}

	// Update current process
	c.procMgr.SetCurrent(newProc)

	// Gracefully stop old process
	if oldProc != nil {
		go func() {
			// Wait a bit for in-flight requests to complete
			time.Sleep(2 * time.Second)
			c.procMgr.Stop(oldProc, 10*time.Second)

			// Clean up all old binaries except the current one
			// This handles the case where previous deletions failed due to file locking
			c.cleanupBinaries(newProc.Binary)
		}()
	}

	c.log("success", "Hot reload completed!")
}

// handleCommandInput listens for command line input to trigger rebuild
func (c *Coordinator) handleCommandInput() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.ToLower(line)

		if line == "rebuild" || line == "r" {
			c.log("info", "Rebuild command received, triggering rebuild...")
			select {
			case c.buildTrigger <- struct{}{}:
			default:
				// Already triggered
			}
		} else if line != "" {
			c.log("info", "Unknown command: %s. Type 'rebuild' or 'r' to trigger rebuild", line)
		}
	}

	// If scanner encounters an error (like stdin closed), just return
	if err := scanner.Err(); err != nil {
		// Don't log error if context is cancelled (normal shutdown)
		select {
		case <-c.ctx.Done():
			return
		default:
			c.log("warn", "Error reading command input: %v", err)
		}
	}
}

// handleRebuildSignal listens for SIGHUP signal or file-based trigger to rebuild
func (c *Coordinator) handleRebuildSignal() {
	// Listen for SIGHUP signal (Unix systems)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	// Also check for file-based trigger (cross-platform)
	rebuildFile := filepath.Join(c.cfg.Root, ".goswap-rebuild")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastModTime time.Time

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-sigChan:
			// SIGHUP received, trigger rebuild
			c.log("info", "Rebuild signal received, triggering rebuild...")
			select {
			case c.buildTrigger <- struct{}{}:
			default:
				// Already triggered
			}
		case <-ticker.C:
			// Check if rebuild trigger file exists or was modified
			info, err := os.Stat(rebuildFile)
			if err == nil {
				modTime := info.ModTime()
				if !modTime.Equal(lastModTime) {
					lastModTime = modTime
					c.log("info", "Rebuild trigger file detected, triggering rebuild...")
					// Remove the file after reading
					os.Remove(rebuildFile)
					select {
					case c.buildTrigger <- struct{}{}:
					default:
						// Already triggered
					}
				}
			} else if !os.IsNotExist(err) {
				// Reset lastModTime if file was deleted
				lastModTime = time.Time{}
			}
		}
	}
}

// waitForShutdown waits for interrupt signal and performs graceful shutdown
func (c *Coordinator) waitForShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	c.log("info", "Shutting down...")

	// Cancel context to stop all components
	c.cancel()

	// Stop watcher
	c.watcher.Stop()

	// Stop current process
	if proc := c.procMgr.Current(); proc != nil {
		c.procMgr.Stop(proc, 10*time.Second)
		// Cleanup binary on shutdown
		if proc.Binary != "" {
			os.Remove(proc.Binary)
		}
	}
	// Final cleanup
	c.cleanupBinaries("")

	c.log("info", "Goodbye!")
}

// cleanupBinaries removes all executable files in the output directory
// except the one specified by keepBinary
func (c *Coordinator) cleanupBinaries(keepBinary string) {
	// Determine the directory to clean
	// We use the configured binary path template to find the directory
	binTemplate := c.cfg.Build.Bin
	if !filepath.IsAbs(binTemplate) {
		binTemplate = filepath.Join(c.cfg.Root, binTemplate)
	}
	dir := filepath.Dir(binTemplate)

	entries, err := os.ReadDir(dir)
	if err != nil {
		c.log("warn", "Failed to read directory for cleanup: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Check if it's an executable file (simple check by extension for Windows)
		ext := filepath.Ext(entry.Name())
		if ext != ".exe" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())

		// Skip the one we want to keep
		if fullPath == keepBinary {
			continue
		}

		// Attempt to delete
		err := os.Remove(fullPath)
		if err != nil {
			// Just log debug/warn, don't fail. It might be locked, we'll get it next time.
			// Only log if it's not "file not found" (race condition)
			if !os.IsNotExist(err) {
				// To avoid spamming, we could only log if it's NOT a "file used by another process" error,
				// but distinguishing that platform-independently is tricky.
				// For now, let's just log it if we can't delete it.
				// c.log("warn", "Failed to cleanup old binary %s: %v", entry.Name(), err)
			}
		} else {
			c.log("info", "Cleaned up stale binary: %s", entry.Name())
		}
	}
}

// log prints a formatted log message
func (c *Coordinator) log(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")

	if c.cfg.Log.Color {
		switch level {
		case "info":
			color.Cyan("[%s] [SWAP] %s", timestamp, msg)
		case "success":
			color.Green("[%s] [SWAP] %s", timestamp, msg)
		case "error":
			color.Red("[%s] [SWAP] %s", timestamp, msg)
		default:
			fmt.Printf("[%s] [SWAP] %s\n", timestamp, msg)
		}
	} else {
		fmt.Printf("[%s] [SWAP] %s\n", timestamp, msg)
	}
}
