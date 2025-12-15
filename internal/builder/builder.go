package builder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
)

// Status represents the build status
type Status int

const (
	StatusIdle Status = iota
	StatusBuilding
	StatusSuccess
	StatusFailed
)

// Result contains the build result
type Result struct {
	Status   Status
	Binary   string
	Duration time.Duration
	Output   string
	Error    error
}

// Builder handles the compilation process
type Builder struct {
	cmdTemplate string
	binTemplate string
	workDir     string
	colorful    bool

	mu         sync.Mutex
	status     Status
	cancel     context.CancelFunc
	buildCount uint64
	lastBinary string
}

// New creates a new builder
func New(cmd, bin, workDir string, colorful bool) *Builder {
	return &Builder{
		cmdTemplate: cmd,
		binTemplate: bin,
		workDir:     workDir,
		colorful:    colorful,
		status:      StatusIdle,
	}
}

// Build compiles the application
func (b *Builder) Build(ctx context.Context) Result {
	b.mu.Lock()
	// Cancel any ongoing build
	if b.cancel != nil {
		b.cancel()
	}
	buildCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.status = StatusBuilding
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()
	}()

	start := time.Now()
	b.log("info", "Building...")

	// Generate unique binary name for this build
	buildNum := atomic.AddUint64(&b.buildCount, 1)
	binPath := b.generateBinaryPath(buildNum)
	cmd := b.generateBuildCmd(binPath)

	// Parse the build command
	args := parseCommand(cmd)
	if len(args) == 0 {
		return Result{
			Status: StatusFailed,
			Error:  fmt.Errorf("empty build command"),
		}
	}

	// Create the command
	execCmd := exec.CommandContext(buildCtx, args[0], args[1:]...)
	execCmd.Dir = b.workDir

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	// Run the build
	err := execCmd.Run()
	duration := time.Since(start)

	output := stdout.String() + stderr.String()

	if err != nil {
		b.mu.Lock()
		b.status = StatusFailed
		b.mu.Unlock()

		b.log("error", "Build failed in %v", duration)
		if output != "" {
			b.logOutput(output)
		}

		return Result{
			Status:   StatusFailed,
			Duration: duration,
			Output:   output,
			Error:    err,
		}
	}

	// Verify the binary exists
	if _, err := os.Stat(binPath); err != nil {
		b.mu.Lock()
		b.status = StatusFailed
		b.mu.Unlock()

		return Result{
			Status:   StatusFailed,
			Duration: duration,
			Output:   output,
			Error:    fmt.Errorf("binary not found: %s", binPath),
		}
	}

	b.mu.Lock()
	b.status = StatusSuccess
	b.lastBinary = binPath
	b.mu.Unlock()

	b.log("success", "Build succeeded in %v", duration)

	return Result{
		Status:   StatusSuccess,
		Binary:   binPath,
		Duration: duration,
		Output:   output,
	}
}

// generateBinaryPath generates a unique binary path for each build
func (b *Builder) generateBinaryPath(buildNum uint64) string {
	ext := filepath.Ext(b.binTemplate)
	base := strings.TrimSuffix(b.binTemplate, ext)
	return fmt.Sprintf("%s_%d%s", base, buildNum, ext)
}

// generateBuildCmd generates the build command with the specific binary path
func (b *Builder) generateBuildCmd(binPath string) string {
	// Replace the -o flag's target with our unique binary path
	cmd := b.cmdTemplate

	// Find and replace the -o argument
	parts := parseCommand(cmd)
	for i, part := range parts {
		if part == "-o" && i+1 < len(parts) {
			parts[i+1] = binPath
			break
		}
	}

	return strings.Join(parts, " ")
}

// Status returns the current build status
func (b *Builder) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

// Binary returns the path to the last compiled binary
func (b *Builder) Binary() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastBinary
}

// log prints a formatted log message
func (b *Builder) log(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")

	if b.colorful {
		switch level {
		case "info":
			color.Cyan("[%s] [BUILD] %s", timestamp, msg)
		case "success":
			color.Green("[%s] [BUILD] %s", timestamp, msg)
		case "error":
			color.Red("[%s] [BUILD] %s", timestamp, msg)
		default:
			fmt.Printf("[%s] [BUILD] %s\n", timestamp, msg)
		}
	} else {
		fmt.Printf("[%s] [BUILD] %s\n", timestamp, msg)
	}
}

// logOutput prints build output
func (b *Builder) logOutput(output string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line != "" {
			if b.colorful {
				color.Yellow("         %s", line)
			} else {
				fmt.Printf("         %s\n", line)
			}
		}
	}
}

// parseCommand splits a command string into arguments
func parseCommand(cmd string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range cmd {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current.WriteRune(r)
			}
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}
