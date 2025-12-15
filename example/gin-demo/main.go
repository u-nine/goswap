package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	// Get address from environment variable (set by go-swap)
	// Using 127.0.0.1 avoids Windows firewall prompts
	addr := os.Getenv("ADDR")
	if addr == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8089"
		}
		addr = "127.0.0.1:" + port
	}

	r := gin.Default()

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// Example API endpoint
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello from Gin!",
			"version": "1.0.0",
		})
	})

	r.GET("/api/hello", func(c *gin.Context) {
		name := c.DefaultQuery("name", "World")
		c.JSON(http.StatusOK, gin.H{
			"message": fmt.Sprintf("Hello, %s!", name),
		})
	})

	// Run on the address specified by go-swap
	r.Run(addr)
}
