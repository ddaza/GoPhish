// Package config loads GoPhish configuration from a TOML file.
//
// The design is config-driven by intent (see AGENTS.md §5.1): every external
// OSINT source is described by data, not code. Adding, removing, or swapping a
// source's endpoint is a config edit, not a rebuild. Each source gets a
// BaseURL plus optional credentials and the rate/quota knobs from §5.6.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/spf13/viper"
)

// ServiceConfig holds the connection details for a single external source.
type ServiceConfig struct {
	// BaseURL is the endpoint used by the source client. It is fully
	// config-driven so alternative hosts or mirrors can be plugged in.
	BaseURL string `mapstructure:"base_url"`
	// APIKey is the optional credential. Prefer APIKeyEnv over hardcoding.
	APIKey string `mapstructure:"api_key"`
	// APIKeyEnv names an environment variable that, if set, overrides APIKey.
	APIKeyEnv string `mapstructure:"api_key_env"`
	// Enabled toggles the source. Disabled services are skipped by the pipeline.
	Enabled bool `mapstructure:"enabled"`
	// RequestsPerMinute bounds the per-source request rate (AGENTS.md §5.6).
	RequestsPerMinute int `mapstructure:"requests_per_minute"`
	// MaxChecks is the hard quota ceiling for this source.
	MaxChecks int `mapstructure:"max_checks"`
	// QuotaWindow resets the quota counter after this duration (e.g. "24h").
	QuotaWindow time.Duration `mapstructure:"quota_window"`
}

// Config is the top-level configuration.
type Config struct {
	// Services is keyed by source name (e.g. "rdap", "crtsh", "certstream",
	// "phishtank", "whois").
	Services map[string]ServiceConfig `mapstructure:"services"`
}

// Service returns a named service and whether it was configured.
func (c *Config) Service(name string) (ServiceConfig, bool) {
	svc, ok := c.Services[name]
	return svc, ok
}

// resolveKeys overlays environment-supplied credentials onto APIKey.
func (c *Config) resolveKeys() {
	for name, svc := range c.Services {
		if svc.APIKeyEnv != "" {
			if val, ok := os.LookupEnv(svc.APIKeyEnv); ok && val != "" {
				svc.APIKey = val
			}
		}
		c.Services[name] = svc
	}
}

// defaultServices provides known public endpoints so the tool runs out of the
// box. The TOML config overrides any matching service key (see Load).
func defaultServices() map[string]ServiceConfig {
	return map[string]ServiceConfig{
		"rdap": {
			BaseURL:           "https://rdap.org",
			Enabled:           true,
			RequestsPerMinute: 30,
		},
		"whois": {
			Enabled:           false,
			RequestsPerMinute: 20,
		},
		"crtsh": {
			BaseURL:           "https://crt.sh",
			Enabled:           true,
			RequestsPerMinute: 20,
		},
		"certstream": {
			BaseURL: "wss://certstream.calidog.io",
			Enabled: true,
		},
		"phishtank": {
			BaseURL:           "https://data.phishtank.com/data",
			Enabled:           true,
			RequestsPerMinute: 10,
		},
	}
}

// Load reads configuration from path (a TOML file). When path is empty,
// "gophish.toml" is searched in the current directory and $HOME/.gophish.
//
// A missing config file is not an error: built-in service defaults are
// returned. A present-but-unreadable or malformed file is an error.
//
// Service entries present in the file replace the matching default entry
// entirely; services not mentioned keep their defaults. After loading, any
// APIKeyEnv is resolved from the environment.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")

	cfg := &Config{Services: defaultServices()}

	if path != "" {
		// An explicitly-named file that is absent means "use defaults".
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				cfg.resolveKeys()
				return cfg, nil
			}
			return nil, fmt.Errorf("stat config: %w", err)
		}
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("gophish")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.gophish")
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// Missing file: keep defaults.
	} else {
		var fileCfg Config
		if err := v.Unmarshal(&fileCfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
		// Merge file services over defaults (per-service replacement).
		maps.Copy(cfg.Services, fileCfg.Services)
	}

	cfg.resolveKeys()
	return cfg, nil
}
