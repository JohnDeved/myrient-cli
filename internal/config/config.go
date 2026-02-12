package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func homeDirOrFallback() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}

// Config holds all user-configurable settings.
type Config struct {
	// DownloadDir is where downloaded files are saved.
	DownloadDir string `json:"download_dir"`
	// MaxConcurrentDownloads is how many files to download in parallel.
	MaxConcurrentDownloads int `json:"max_concurrent_downloads"`
	// RequestsPerSecond rate-limits HTTP requests to Myrient.
	RequestsPerSecond float64 `json:"requests_per_second"`
	// IndexStaleDays controls how many days before a directory is re-crawled.
	IndexStaleDays int `json:"index_stale_days"`
	// BaseURL is the root URL for Myrient's file listings.
	BaseURL string `json:"base_url"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	home := homeDirOrFallback()
	return &Config{
		DownloadDir:            filepath.Join(home, "Downloads", "myrient"),
		MaxConcurrentDownloads: 3,
		RequestsPerSecond:      5.0,
		IndexStaleDays:         7,
		BaseURL:                "https://myrient.erista.me/files/",
	}
}

// ConfigDir returns the directory where config and data files are stored.
func ConfigDir() string {
	if dir := os.Getenv("MYRIENT_CONFIG_DIR"); dir != "" {
		return dir
	}
	home := homeDirOrFallback()
	return filepath.Join(home, ".config", "myrient")
}

// DBPath returns the path to the SQLite database.
func DBPath() string {
	return filepath.Join(ConfigDir(), "index.db")
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

// Load reads config from disk, returning defaults if the file doesn't exist.
func Load() (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			if err := cfg.Save(); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the config to disk.
func (c *Config) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0o644)
}
