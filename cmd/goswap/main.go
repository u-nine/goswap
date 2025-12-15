package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/u-nine/goswap/internal/coordinator"
	"github.com/u-nine/goswap/pkg/config"
)

const (
	version = "1.0.0"
	banner  = `
   _____        _____                       
  / ____|      / ____|                      
 | |  __  ___ | (_____      ____ _ _ __  
 | | |_ |/ _ \ \___ \ \ /\ / / _' | '_ \ 
 | |__| | (_) |____) \ V  V / (_| | |_) |
  \_____|\\___/|_____/ \_/\_/ \__,_| .__/ 
                                   | |    
                                   |_|    
`
)

func main() {
	// Parse command line flags
	var (
		configPath  string
		showVersion bool
		initConfig  bool
	)

	flag.StringVar(&configPath, "c", "goswap.toml", "path to config file")
	flag.BoolVar(&showVersion, "v", false, "show version")
	flag.BoolVar(&initConfig, "init", false, "create default config file")
	flag.Parse()

	// Show version
	if showVersion {
		fmt.Printf("go-swap version %s\n", version)
		return
	}

	// Print banner
	color.Cyan(banner)
	color.Green("  Zero-downtime hot reload for Gin applications")
	color.White("  Version: %s\n\n", version)

	// Initialize config file
	if initConfig {
		if err := createDefaultConfig(configPath); err != nil {
			color.Red("Error creating config file: %v\n", err)
			os.Exit(1)
		}
		color.Green("Created config file: %s\n", configPath)
		return
	}

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		color.Red("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Resolve root path relative to config file
	if !filepath.IsAbs(cfg.Root) {
		configDir := filepath.Dir(configPath)
		if configDir == "." {
			configDir, _ = os.Getwd()
		}
		cfg.Root = filepath.Join(configDir, cfg.Root)
	}

	// Create and run coordinator
	coord := coordinator.New(cfg)
	if err := coord.Run(); err != nil {
		color.Red("Error: %v\n", err)
		os.Exit(1)
	}
}

func createDefaultConfig(path string) error {
	cfg := config.DefaultConfig()
	return cfg.Save(path)
}
