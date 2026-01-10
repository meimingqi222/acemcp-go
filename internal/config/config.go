package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds daemon runtime configuration.
type Config struct {
	Listen          string // JSON-RPC / IPC listener (reserved)
	HTTPAddr        string // HTTP management / health
	HTTPToken       string // optional token for HTTP management endpoints
	DataDir         string // data directory (~/.acemcp/data)
	SettingsPath    string // settings file (~/.acemcp/settings.toml)
	LogLevel        string // debug|info|warn|error
	BatchSize       int
	MaxLinesPerBlob int
	BaseURL         string
	Token           string
	TextExtensions  []string
	ExcludePatterns []string
}

// Load reads config from ~/.acemcp/settings.toml (compatible path) and applies defaults.
func Load(dataDirOverride string) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home: %w", err)
	}

	settingsPath := filepath.Join(home, ".acemcp", "settings.toml")
	dataDir := filepath.Join(home, ".acemcp", "data")
	if dataDirOverride != "" {
		dataDir = dataDirOverride
	}

	v := viper.New()
	v.SetConfigFile(settingsPath)
	v.SetConfigType("toml")

	// defaults aligned with Python版
	v.SetDefault("LISTEN", "127.0.0.1:7033")
	v.SetDefault("HTTP_ADDR", "127.0.0.1:7034")
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("BATCH_SIZE", 10)
	v.SetDefault("MAX_LINES_PER_BLOB", 800)
	v.SetDefault("BASE_URL", "https://api.example.com")
	v.SetDefault("TOKEN", "")
	v.SetDefault("HTTP_TOKEN", "")
	v.SetDefault("TEXT_EXTENSIONS", []string{".py", ".js", ".ts", ".go", ".rs", ".java", ".md", ".txt"})
	v.SetDefault("EXCLUDE_PATTERNS", []string{".git", "node_modules", "vendor", ".venv", "venv", "__pycache__"})

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// missing file: continue with defaults
	}

	cfg := &Config{
		Listen:          v.GetString("LISTEN"),
		HTTPAddr:        v.GetString("HTTP_ADDR"),
		HTTPToken:       v.GetString("HTTP_TOKEN"),
		DataDir:         dataDir,
		SettingsPath:    settingsPath,
		LogLevel:        v.GetString("LOG_LEVEL"),
		BatchSize:       v.GetInt("BATCH_SIZE"),
		MaxLinesPerBlob: v.GetInt("MAX_LINES_PER_BLOB"),
		BaseURL:         v.GetString("BASE_URL"),
		Token:           v.GetString("TOKEN"),
		TextExtensions:  v.GetStringSlice("TEXT_EXTENSIONS"),
		ExcludePatterns: v.GetStringSlice("EXCLUDE_PATTERNS"),
	}

	return cfg, nil
}

// Reload re-reads settings.toml and updates runtime fields (excluding CLI overrides like DataDir).
func (c *Config) Reload() error {
	v := viper.New()
	v.SetConfigFile(c.SettingsPath)
	v.SetConfigType("toml")
	v.SetDefault("LISTEN", "127.0.0.1:7033")
	v.SetDefault("HTTP_ADDR", "127.0.0.1:7034")
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("BATCH_SIZE", 10)
	v.SetDefault("MAX_LINES_PER_BLOB", 800)
	v.SetDefault("BASE_URL", "https://api.example.com")
	v.SetDefault("TOKEN", "")
	v.SetDefault("HTTP_TOKEN", "")
	v.SetDefault("TEXT_EXTENSIONS", []string{".py", ".js", ".ts", ".go", ".rs", ".java", ".md", ".txt"})
	v.SetDefault("EXCLUDE_PATTERNS", []string{".git", "node_modules", "vendor", ".venv", "venv", "__pycache__"})

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("read config: %w", err)
		}
		// missing file: keep defaults
	}

	c.BatchSize = v.GetInt("BATCH_SIZE")
	c.MaxLinesPerBlob = v.GetInt("MAX_LINES_PER_BLOB")
	c.BaseURL = v.GetString("BASE_URL")
	c.Token = v.GetString("TOKEN")
	c.HTTPToken = v.GetString("HTTP_TOKEN")
	c.TextExtensions = v.GetStringSlice("TEXT_EXTENSIONS")
	c.ExcludePatterns = v.GetStringSlice("EXCLUDE_PATTERNS")
	return nil
}
