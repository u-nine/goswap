package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
)

// Proxy is a reverse proxy server that can switch backends dynamically
type Proxy struct {
	port     int
	colorful bool

	mu       sync.RWMutex
	upstream *url.URL
	proxy    *httputil.ReverseProxy
	server   *http.Server

	// Stats
	requestCount uint64
}

// New creates a new reverse proxy
func New(port int, colorful bool) *Proxy {
	p := &Proxy{
		port:     port,
		colorful: colorful,
	}

	return p
}

// SetUpstream changes the upstream server
func (p *Proxy) SetUpstream(host string, port int) error {
	upstream, err := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.upstream = upstream
	p.proxy = httputil.NewSingleHostReverseProxy(upstream)

	// Customize the director
	originalDirector := p.proxy.Director
	p.proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = upstream.Host
	}

	// Custom error handler
	p.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Ignore connection closed errors during service switching (expected behavior)
		errStr := err.Error()
		if strings.Contains(errStr, "connection was forcibly closed") ||
			strings.Contains(errStr, "connection reset by peer") ||
			strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "EOF") {
			// This is expected when switching services - don't log as error
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		// Log other errors
		p.log("error", "Proxy error: %v", err)
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
	}

	// Custom transport with reasonable timeouts and better connection handling
	p.proxy.Transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false, // Keep connections alive for better performance
		ResponseHeaderTimeout: 30 * time.Second,
	}

	p.log("info", "Upstream set to %s", upstream.String())
	return nil
}

// Start starts the proxy server
func (p *Proxy) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)

	p.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", p.port),
		Handler: mux,
	}

	p.log("success", "Proxy server listening on :%d", p.port)

	go func() {
		<-ctx.Done()
		p.Stop()
	}()

	if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

// Stop gracefully stops the proxy server
func (p *Proxy) Stop() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.log("info", "Shutting down proxy server...")
	return p.server.Shutdown(ctx)
}

// handleRequest handles incoming HTTP requests
func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	atomic.AddUint64(&p.requestCount, 1)

	p.mu.RLock()
	proxy := p.proxy
	p.mu.RUnlock()

	if proxy == nil {
		http.Error(w, "No upstream server available", http.StatusServiceUnavailable)
		return
	}

	proxy.ServeHTTP(w, r)
}

// RequestCount returns the total number of requests handled
func (p *Proxy) RequestCount() uint64 {
	return atomic.LoadUint64(&p.requestCount)
}

// HasUpstream returns true if an upstream is configured
func (p *Proxy) HasUpstream() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.upstream != nil
}

// log prints a formatted log message
func (p *Proxy) log(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")

	if p.colorful {
		switch level {
		case "info":
			color.Cyan("[%s] [PROXY] %s", timestamp, msg)
		case "success":
			color.Green("[%s] [PROXY] %s", timestamp, msg)
		case "error":
			color.Red("[%s] [PROXY] %s", timestamp, msg)
		default:
			fmt.Printf("[%s] [PROXY] %s\n", timestamp, msg)
		}
	} else {
		fmt.Printf("[%s] [PROXY] %s\n", timestamp, msg)
	}
}
