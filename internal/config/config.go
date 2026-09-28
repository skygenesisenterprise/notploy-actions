// Package config turns the GitHub Actions inputs into a validated, typed
// configuration. This is the only place that maps runner inputs to domain
// values; every other package receives plain values.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skygenesisenterprise/notploy-actions/internal/github"
)

const (
	// DefaultTimeout is used when the `timeout` input is not set.
	DefaultTimeout = 30 * time.Minute
	// DefaultPollInterval is used when the `poll-interval` input is not set.
	DefaultPollInterval = 5 * time.Second
	// MinPollInterval protects the Notploy instance from a tight polling loop.
	MinPollInterval = time.Second
)

// Config is the fully validated configuration of one action run.
type Config struct {
	Endpoint      string
	APIKey        string
	ApplicationID string
	Wait          bool
	Timeout       time.Duration
	PollInterval  time.Duration
}

// Load reads the action inputs from the environment and validates them.
//
// Error messages never include an input value, so a misconfigured secret cannot
// leak into the workflow log.
func Load(getenv github.Getenv) (*Config, error) {
	cfg := &Config{
		Endpoint:      strings.TrimSpace(github.ReadInput(getenv, "endpoint")),
		APIKey:        strings.TrimSpace(github.ReadInput(getenv, "api-key")),
		ApplicationID: strings.TrimSpace(github.ReadInput(getenv, "application-id")),
		Wait:          true,
		Timeout:       DefaultTimeout,
		PollInterval:  DefaultPollInterval,
	}

	missing := make([]string, 0, 3)
	if cfg.Endpoint == "" {
		missing = append(missing, "endpoint")
	}
	if cfg.APIKey == "" {
		missing = append(missing, "api-key")
	}
	if cfg.ApplicationID == "" {
		missing = append(missing, "application-id")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required input(s): %s", strings.Join(missing, ", "))
	}

	wait, err := parseBool(getenv, "wait", cfg.Wait)
	if err != nil {
		return nil, err
	}
	cfg.Wait = wait

	timeout, err := parseDuration(getenv, "timeout", cfg.Timeout)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("input `timeout` must be greater than zero")
	}
	cfg.Timeout = timeout

	interval, err := parseDuration(getenv, "poll-interval", cfg.PollInterval)
	if err != nil {
		return nil, err
	}
	if interval < MinPollInterval {
		return nil, fmt.Errorf("input `poll-interval` must be at least %s", MinPollInterval)
	}
	cfg.PollInterval = interval

	return cfg, nil
}

func parseBool(getenv github.Getenv, name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(github.ReadInput(getenv, name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("input `%s` must be a boolean such as `true` or `false`, got %q", name, raw)
	}
	return value, nil
}

func parseDuration(getenv github.Getenv, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(github.ReadInput(getenv, name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("input `%s` must be a duration such as `30m` or `5s`, got %q", name, raw)
	}
	return value, nil
}
