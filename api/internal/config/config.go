// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AdminPassword string
	AppSecret     string
	DataDir       string
	ListenAddr    string
	PollInterval  time.Duration
	CookieSecure  bool
	TrustProxy    bool
	DockerCommand string
}

// Load reads configuration from the process environment.
func Load() (*Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads configuration using the supplied lookup function (for tests).
func LoadFrom(getenv func(string) string) (*Config, error) {
	c := &Config{
		AdminPassword: getenv("ADMIN_PASSWORD"),
		AppSecret:     getenv("APP_SECRET"),
		DataDir:       getenv("DATA_DIR"),
		ListenAddr:    getenv("LISTEN_ADDR"),
		DockerCommand: getenv("DOCKER_COMMAND"),
		PollInterval:  60 * time.Second,
	}
	if c.AdminPassword == "" {
		return nil, errors.New("ADMIN_PASSWORD is required")
	}
	if c.AppSecret == "" {
		return nil, errors.New("APP_SECRET is required")
	}
	if c.DataDir == "" {
		c.DataDir = "/opt/compose-deploy"
	}
	if c.DockerCommand == "" {
		c.DockerCommand = "docker"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":3000"
	}
	if v := getenv("POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid POLL_INTERVAL %q: %w", v, err)
		}
		if d < time.Second {
			return nil, fmt.Errorf("POLL_INTERVAL must be at least 1s, got %s", d)
		}
		c.PollInterval = d
	}
	if v := getenv("COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid COOKIE_SECURE %q: %w", v, err)
		}
		c.CookieSecure = b
	}
	if v := getenv("TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid TRUST_PROXY %q: %w", v, err)
		}
		c.TrustProxy = b
	}
	return c, nil
}
