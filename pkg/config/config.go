// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

// ServerConfig holds the HTTP server configuration.
type ServerConfig struct {
	Port            string        `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	TLSEnabled      bool          `mapstructure:"tls_enabled"`
	TLSCert         string        `mapstructure:"tls_cert"`
	TLSKey          string        `mapstructure:"tls_key"`
}

// ProviderConfig defines a single AI provider endpoint.
type ProviderConfig struct {
	Name         string            `mapstructure:"name"`
	BaseURL      string            `mapstructure:"base_url"`
	APIKey       string            `mapstructure:"api_key"`
	APIKeyEnv    string            `mapstructure:"api_key_env"`
	Priority     int               `mapstructure:"priority"`
	Enabled      bool              `mapstructure:"enabled"`
	Timeout      time.Duration     `mapstructure:"timeout"`
	Retries      int               `mapstructure:"retries"`
	Headers      map[string]string `mapstructure:"headers"`
	Models       []string          `mapstructure:"models"`
	Weight       int               `mapstructure:"weight"`
	CostPer1KIn  float64           `mapstructure:"cost_per_1k_input"`
	CostPer1KOut float64           `mapstructure:"cost_per_1k_output"`
}

// CacheConfig controls the semantic cache behavior.
type CacheConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	TTL           time.Duration `mapstructure:"ttl"`
	MaxSize       int64         `mapstructure:"max_size"`
	Path          string        `mapstructure:"path"`
	SimilarityThreshold float64 `mapstructure:"similarity_threshold"`
	CleanupInterval time.Duration `mapstructure:"cleanup_interval"`
}

// MaskConfig controls PII masking behavior.
type MaskConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	MaskEmails    bool     `mapstructure:"mask_emails"`
	MaskPhones    bool     `mapstructure:"mask_phones"`
	MaskCreditCards bool   `mapstructure:"mask_credit_cards"`
	MaskSSN       bool     `mapstructure:"mask_ssn"`
	MaskAPIKeys   bool     `mapstructure:"mask_api_keys"`
	MaskIPs       bool     `mapstructure:"mask_ips"`
	CustomPatterns []string `mapstructure:"custom_patterns"`
	Placeholder   string   `mapstructure:"placeholder"`
}

// BudgetConfig defines the spending guardrails.
type BudgetConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	DailyLimit        float64       `mapstructure:"daily_limit"`
	MonthlyLimit      float64       `mapstructure:"monthly_limit"`
	WarningThreshold  float64       `mapstructure:"warning_threshold"`
	HardStop          bool          `mapstructure:"hard_stop"`
	ResetInterval     time.Duration `mapstructure:"reset_interval"`
	NotificationURL   string        `mapstructure:"notification_url"`
}

// FallbackConfig controls provider failover.
type FallbackConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	Timeout         time.Duration `mapstructure:"timeout"`
	MaxRetries      int           `mapstructure:"max_retries"`
	RetryBackoff    time.Duration `mapstructure:"retry_backoff"`
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
	CircuitBreakerThreshold int     `mapstructure:"circuit_breaker_threshold"`
}

// LoggingConfig controls log verbosity.
type LoggingConfig struct {
	Level    string `mapstructure:"level"`
	Format   string `mapstructure:"format"`
	File     string `mapstructure:"file"`
	Console  bool   `mapstructure:"console"`
}

// TUIConfig customizes the terminal UI.
type TUIConfig struct {
	Theme           string `mapstructure:"theme"`
	RefreshInterval int    `mapstructure:"refresh_interval"`
	ShowAnimations  bool   `mapstructure:"show_animations"`
}

// Config is the root configuration container.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Providers []ProviderConfig `mapstructure:"providers"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Mask     MaskConfig     `mapstructure:"mask"`
	Budget   BudgetConfig   `mapstructure:"budget"`
	Fallback FallbackConfig `mapstructure:"fallback"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	TUI      TUIConfig      `mapstructure:"tui"`
}

// Default returns a production-ready default configuration.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            "8080",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    120 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
			TLSEnabled:      false,
		},
		Providers: []ProviderConfig{
			{
				Name:         "openai",
				BaseURL:      "https://api.openai.com",
				APIKeyEnv:    "OPENAI_API_KEY",
				Priority:     1,
				Enabled:      true,
				Timeout:      60 * time.Second,
				Retries:      3,
				Weight:       100,
				CostPer1KIn:  0.0015,
				CostPer1KOut: 0.002,
			},
			{
				Name:         "anthropic",
				BaseURL:      "https://api.anthropic.com",
				APIKeyEnv:    "ANTHROPIC_API_KEY",
				Priority:     2,
				Enabled:      true,
				Timeout:      60 * time.Second,
				Retries:      3,
				Weight:       80,
				CostPer1KIn:  0.003,
				CostPer1KOut: 0.015,
			},
			{
				Name:         "gemini",
				BaseURL:      "https://generativelanguage.googleapis.com",
				APIKeyEnv:    "GEMINI_API_KEY",
				Priority:     3,
				Enabled:      true,
				Timeout:      60 * time.Second,
				Retries:      2,
				Weight:       70,
				CostPer1KIn:  0.0005,
				CostPer1KOut: 0.0015,
			},
		},
		Cache: CacheConfig{
			Enabled:             true,
			TTL:                 24 * time.Hour,
			MaxSize:             1024 * 1024 * 1024, // 1GB
			Path:                ".nexusguard/cache",
			SimilarityThreshold: 0.95,
			CleanupInterval:     1 * time.Hour,
		},
		Mask: MaskConfig{
			Enabled:         true,
			MaskEmails:      true,
			MaskPhones:      true,
			MaskCreditCards: true,
			MaskSSN:         true,
			MaskAPIKeys:     true,
			MaskIPs:         true,
			Placeholder:     "[REDACTED]",
		},
		Budget: BudgetConfig{
			Enabled:          true,
			DailyLimit:       5.0,
			MonthlyLimit:     50.0,
			WarningThreshold: 0.8,
			HardStop:         true,
			ResetInterval:    24 * time.Hour,
		},
		Fallback: FallbackConfig{
			Enabled:                 true,
			Timeout:                 30 * time.Second,
			MaxRetries:              3,
			RetryBackoff:            1 * time.Second,
			HealthCheckInterval:     30 * time.Second,
			CircuitBreakerThreshold: 5,
		},
		Logging: LoggingConfig{
			Level:   "info",
			Format:  "json",
			Console: true,
		},
		TUI: TUIConfig{
			Theme:           "cyber",
			RefreshInterval: 2,
			ShowAnimations:  true,
		},
	}
}

// Load reads configuration from file, environment, and defaults.
func Load(path string) (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "120s")
	v.SetDefault("cache.enabled", true)
	v.SetDefault("cache.ttl", "24h")
	v.SetDefault("mask.enabled", true)
	v.SetDefault("budget.enabled", true)
	v.SetDefault("budget.daily_limit", 5.0)
	v.SetDefault("fallback.enabled", true)

	// Environment variable bindings
	v.SetEnvPrefix("NEXUSGUARD")
	v.AutomaticEnv()

	// Read from file if provided
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("config file not found: %w", err)
		}
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	} else {
		// Search for default config files
		v.SetConfigName("nexusguard")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.nexusguard")
		v.AddConfigPath("/etc/nexusguard/")

		_ = v.ReadInConfig() // Optional
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Resolve API keys from environment
	for i := range cfg.Providers {
		if cfg.Providers[i].APIKey == "" && cfg.Providers[i].APIKeyEnv != "" {
			cfg.Providers[i].APIKey = os.Getenv(cfg.Providers[i].APIKeyEnv)
		}
	}

	return &cfg, nil
}

// Validate checks configuration consistency.
func (c *Config) Validate() error {
	if c.Server.Port == "" {
		return fmt.Errorf("server port is required")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured")
	}
	hasEnabled := false
	for _, p := range c.Providers {
		if p.Enabled {
			hasEnabled = true
			break
		}
	}
	if !hasEnabled {
		return fmt.Errorf("at least one provider must be enabled")
	}
	if c.Budget.Enabled && c.Budget.DailyLimit <= 0 {
		return fmt.Errorf("daily budget limit must be positive")
	}
	return nil
}
