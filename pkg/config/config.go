package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the main configuration structure
type Config struct {
	Root    string        `toml:"root"`
	Build   BuildConfig   `toml:"build"`
	Process ProcessConfig `toml:"process"`
	Proxy   ProxyConfig   `toml:"proxy"`
	Watch   WatchConfig   `toml:"watch"`
	Log     LogConfig     `toml:"log"`
}

// BuildConfig contains build-related settings
type BuildConfig struct {
	Cmd   string `toml:"cmd"`
	Bin   string `toml:"bin"`
	Delay int    `toml:"delay"` // milliseconds
}

// ProcessConfig contains process management settings
type ProcessConfig struct {
	StartPort int `toml:"start_port"`
}

// ProxyConfig contains proxy server settings
type ProxyConfig struct {
	Port int `toml:"port"`
}

// WatchConfig contains file watching settings
type WatchConfig struct {
	Include    []string `toml:"include"`
	Exclude    []string `toml:"exclude"`
	Extensions []string `toml:"extensions"`
}

// LogConfig contains logging settings
type LogConfig struct {
	Level string `toml:"level"`
	Color bool   `toml:"color"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Root: ".",
		Build: BuildConfig{
			Cmd:   "go build -o ./tmp/main.exe .",
			Bin:   "./tmp/main.exe",
			Delay: 500,
		},
		Process: ProcessConfig{
			StartPort: 56700,
		},
		Proxy: ProxyConfig{
			Port: 8080,
		},
		Watch: WatchConfig{
			Include:    []string{"./"},
			Exclude:    []string{"vendor", ".git", "tmp", "node_modules"},
			Extensions: []string{"go", "html", "tmpl"},
		},
		Log: LogConfig{
			Level: "info",
			Color: true,
		},
	}
}

// Load reads configuration from a TOML file
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Return defaults if config file doesn't exist
		}
		return nil, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Resolve relative paths
	if !filepath.IsAbs(cfg.Root) {
		dir := filepath.Dir(path)
		cfg.Root = filepath.Join(dir, cfg.Root)
	}

	return cfg, nil
}

// Save writes configuration to a TOML file
func (c *Config) Save(path string) error {
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
