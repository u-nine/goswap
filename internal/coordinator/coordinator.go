package coordinator

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
}

// New creates a new coordinator
func New(cfg *config.Config) *Coordinator {
	ctx, cancel := context.WithCancel(context.Background())

	return &Coordinator{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
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

	c.log("success", "go-swap is running!")
	c.log("info", "Listening on http://localhost:%d", c.cfg.Proxy.Port)
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
	c.procMgr = process.New(c.cfg.Log.Color)

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
func (c *Coordinator) onFileChange(event watcher.Event) {
	c.log("info", "File changed: %s", event.Path)

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

	// Stop watcher
	c.watcher.Stop()

	// Stop current process
	if proc := c.procMgr.Current(); proc != nil {
		c.procMgr.Stop(proc, 10*time.Second)
	}

	c.log("info", "Goodbye!")
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
