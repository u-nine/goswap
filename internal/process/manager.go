package process

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
)

// Process represents a managed application process
type Process struct {
	Binary  string
	Port    int
	Cmd     *exec.Cmd
	Started time.Time
	Ready   bool
}

// Manager manages application processes
type Manager struct {
	colorful      bool
	healthPath    string
	healthTimeout time.Duration

	mu          sync.RWMutex
	current     *Process
	portCounter int
}

// New creates a new process manager
func New(colorful bool) *Manager {
	return &Manager{
		colorful:      colorful,
		healthPath:    "/",
		healthTimeout: 30 * time.Second,
		portCounter:   9000,
	}
}

// SetHealthCheck sets the health check configuration
func (m *Manager) SetHealthCheck(path string, timeout time.Duration) {
	m.healthPath = path
	m.healthTimeout = timeout
}

// Start launches a new process with the given binary
func (m *Manager) Start(ctx context.Context, binary string) (*Process, error) {
	// Find an available port
	port, err := m.findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find available port: %w", err)
	}

	m.log("info", "Starting new process on port %d...", port)

	// Create the command with GOSWAP_PORT and GOSWAP_HOST environment variables
	// Using 127.0.0.1 to avoid Windows firewall prompts
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GOSWAP_PORT=%d", port),
		"GOSWAP_HOST=127.0.0.1",
		fmt.Sprintf("GOSWAP_ADDR=127.0.0.1:%d", port),
	)
	cmd.Stdout = &prefixWriter{prefix: "[APP] ", colorful: m.colorful}
	cmd.Stderr = &prefixWriter{prefix: "[APP] ", colorful: m.colorful, isError: true}

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	proc := &Process{
		Binary:  binary,
		Port:    port,
		Cmd:     cmd,
		Started: time.Now(),
	}

	// Wait for the process to be ready
	if err := m.waitForReady(ctx, proc); err != nil {
		// Check if process actually exited
		if proc.Cmd.ProcessState != nil {
			cmd.Process.Kill()
			return nil, fmt.Errorf("process exited unexpectedly during startup")
		}
		// Check if context was cancelled
		if ctx.Err() != nil {
			cmd.Process.Kill()
			return nil, fmt.Errorf("startup cancelled: %w", ctx.Err())
		}
		// Health check timeout - just log a warning and continue
		m.log("warn", "Health check did not pass: %v", err)
		m.log("info", "Process started on port %d (health check skipped, process continues running)", port)
		proc.Ready = true
		return proc, nil
	}

	proc.Ready = true
	m.log("success", "Process ready on port %d (startup took %v)", port, time.Since(proc.Started))

	return proc, nil
}

// Stop gracefully stops a process
func (m *Manager) Stop(proc *Process, timeout time.Duration) error {
	if proc == nil || proc.Cmd == nil || proc.Cmd.Process == nil {
		return nil
	}

	m.log("info", "Gracefully stopping process on port %d...", proc.Port)

	// Send SIGTERM for graceful shutdown
	if err := proc.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process might already be dead
		return nil
	}

	// Wait for the process to exit with timeout
	done := make(chan error, 1)
	go func() {
		_, err := proc.Cmd.Process.Wait()
		done <- err
	}()

	select {
	case <-time.After(timeout):
		// Force kill if timeout
		m.log("warn", "Process didn't exit gracefully, force killing...")
		proc.Cmd.Process.Kill()
	case <-done:
		m.log("info", "Process stopped gracefully")
	}

	return nil
}

// SetCurrent sets the current active process
func (m *Manager) SetCurrent(proc *Process) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = proc
}

// Current returns the current active process
func (m *Manager) Current() *Process {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// findAvailablePort finds an available port
func (m *Manager) findAvailablePort() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Try incrementing ports starting from portCounter
	for i := 0; i < 100; i++ {
		port := m.portCounter + i
		if m.isPortAvailable(port) {
			m.portCounter = port + 1
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports found")
}

// isPortAvailable checks if a port is available
func (m *Manager) isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// waitForReady waits for the process to be ready to accept connections
func (m *Manager) waitForReady(ctx context.Context, proc *Process) error {
	deadline := time.Now().Add(m.healthTimeout)

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", proc.Port, m.healthPath)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}

		// Check if process exited
		if proc.Cmd.ProcessState != nil {
			return fmt.Errorf("process exited unexpectedly")
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("health check timeout after %v", m.healthTimeout)
}

// log prints a formatted log message
func (m *Manager) log(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")

	if m.colorful {
		switch level {
		case "info":
			color.Cyan("[%s] [PROC] %s", timestamp, msg)
		case "success":
			color.Green("[%s] [PROC] %s", timestamp, msg)
		case "warn":
			color.Yellow("[%s] [PROC] %s", timestamp, msg)
		case "error":
			color.Red("[%s] [PROC] %s", timestamp, msg)
		default:
			fmt.Printf("[%s] [PROC] %s\n", timestamp, msg)
		}
	} else {
		fmt.Printf("[%s] [PROC] %s\n", timestamp, msg)
	}
}

// prefixWriter adds a prefix to each line of output
type prefixWriter struct {
	prefix   string
	colorful bool
	isError  bool
}

func (w *prefixWriter) Write(p []byte) (n int, err error) {
	timestamp := time.Now().Format("15:04:05")

	if w.colorful {
		if w.isError {
			color.Red("[%s] %s%s", timestamp, w.prefix, string(p))
		} else {
			fmt.Printf("[%s] %s%s", timestamp, w.prefix, string(p))
		}
	} else {
		fmt.Printf("[%s] %s%s", timestamp, w.prefix, string(p))
	}

	return len(p), nil
}

var _ io.Writer = (*prefixWriter)(nil)
