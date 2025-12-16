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
	"github.com/u-nine/goswap/pkg/config"
)

// Coordinator orchestrates all components for zero-downtime hot reload
type Coordinator struct {
	cfg     *config.Config
	builder *builder.Builder
	proxy   *proxy.Proxy
	procMgr *process.Manager

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

	// Start proxy server in a goroutine
	go func() {
		if err := c.proxy.Start(c.ctx); err != nil {
			c.log("error", "Proxy server error: %v", err)
		}
	}()

	// Start build loop
	go c.buildLoop()

	// Start command input handler
	go c.commandLoop()

	c.log("success", "go-swap is running!")
	c.log("info", "Listening on http://localhost:%d", c.cfg.Proxy.Port)
	c.log("info", "Commands:")
	c.log("info", "  rebuild, r - Rebuild and reload the application")
	c.log("info", "  quit, q   - Stop go-swap")
	c.log("info", "Press Ctrl+C to stop")

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

	return nil
}

// commandLoop handles user command input
func (c *Coordinator) commandLoop() {
	scanner := bufio.NewScanner(os.Stdin)

	// Use a channel to handle input in a non-blocking way
	inputChan := make(chan string, 1)

	// Read input in a separate goroutine
	go func() {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				select {
				case inputChan <- line:
				case <-c.ctx.Done():
					return
				}
			}
		}
		close(inputChan)
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		case line, ok := <-inputChan:
			if !ok {
				// Channel closed, exit
				return
			}

			// Parse command
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}

			cmd := strings.ToLower(parts[0])
			switch cmd {
			case "rebuild", "r":
				c.log("info", "Manual rebuild triggered")
				select {
				case c.buildTrigger <- struct{}{}:
				default:
					// Already triggered
				}
			case "quit", "q", "exit":
				c.log("info", "Shutting down...")
				c.cancel()
				return
			default:
				c.log("info", "Unknown command: %s. Use 'rebuild' or 'r' to rebuild, 'quit' or 'q' to exit", cmd)
			}
		}
	}
}

// buildLoop handles the serialized build process
func (c *Coordinator) buildLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.buildTrigger:
			// The channel has buffer 1, so at most one pending trigger.
			// If a new trigger arrives during build, it will be in the channel.
			// Next loop will pick it up and rebuild. This provides coalescing.
			// If multiple triggers arrive while building, only one will be queued.

			c.rebuild()
		}
	}
}

func (c *Coordinator) rebuild() {
	// Build new version
	result := c.builder.Build(c.ctx)
	if result.Status != builder.StatusSuccess {
		c.log("error", "Build failed, keeping current version")
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

// waitForShutdown waits for interrupt signal and performs graceful shutdown
func (c *Coordinator) waitForShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	c.log("info", "Shutting down...")

	// Cancel context to stop all components
	c.cancel()

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
