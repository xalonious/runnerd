package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppID                                   int64
	PrivateKeyPath, WebhookSecret           string
	RunnerHome, StateDir, Label, ListenAddr string
	Concurrency, QueueSize                  int
	RunnerGroupID                           int64
	LogLevel                                slog.Level
	ShutdownTimeout, RunnerTimeout          time.Duration
}

func Load() (Config, error) { return FromEnv(os.Getenv) }

func FromEnv(get func(string) string) (Config, error) {
	var c Config
	var err error
	c.AppID, err = strconv.ParseInt(get("GITHUB_APP_ID"), 10, 64)
	if err != nil || c.AppID <= 0 {
		return c, fmt.Errorf("GITHUB_APP_ID must be a positive integer")
	}
	c.PrivateKeyPath = get("GITHUB_PRIVATE_KEY_PATH")
	c.WebhookSecret = get("GITHUB_WEBHOOK_SECRET")
	value := func(k, fallback string) string {
		if v := get(k); v != "" {
			return v
		}
		return fallback
	}
	c.RunnerHome = value("RUNNER_HOME", "/opt/runnerd/runner")
	c.StateDir = value("RUNNER_STATE_DIR", "/var/lib/runnerd/runners")
	c.Label = value("RUNNER_LABEL", "runnerd")
	c.ListenAddr = value("LISTEN_ADDR", "127.0.0.1:8080")
	for _, item := range []struct {
		key, fallback string
		dst           *int
	}{
		{"MAX_CONCURRENT_RUNNERS", "1", &c.Concurrency}, {"QUEUE_SIZE", "100", &c.QueueSize},
	} {
		*item.dst, err = strconv.Atoi(value(item.key, item.fallback))
		if err != nil || *item.dst < 1 || *item.dst > 10000 {
			return c, fmt.Errorf("%s must be between 1 and 10000", item.key)
		}
	}
	c.RunnerGroupID, err = strconv.ParseInt(value("RUNNER_GROUP_ID", "1"), 10, 64)
	if err != nil || c.RunnerGroupID <= 0 {
		return c, fmt.Errorf("RUNNER_GROUP_ID must be a positive integer")
	}
	if err = c.LogLevel.UnmarshalText([]byte(value("LOG_LEVEL", "info"))); err != nil {
		return c, fmt.Errorf("LOG_LEVEL: %w", err)
	}
	c.ShutdownTimeout, err = time.ParseDuration(value("SHUTDOWN_TIMEOUT", "30s"))
	if err != nil || c.ShutdownTimeout < time.Second {
		return c, fmt.Errorf("SHUTDOWN_TIMEOUT must be a duration of at least 1s")
	}
	c.RunnerTimeout, err = time.ParseDuration(value("RUNNER_TIMEOUT", "24h"))
	if err != nil || c.RunnerTimeout < time.Second {
		return c, fmt.Errorf("RUNNER_TIMEOUT must be a duration of at least 1s")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.AppID <= 0 || c.WebhookSecret == "" {
		return fmt.Errorf("GITHUB_APP_ID and GITHUB_WEBHOOK_SECRET are required")
	}
	for key, path := range map[string]string{"GITHUB_PRIVATE_KEY_PATH": c.PrivateKeyPath, "RUNNER_HOME": c.RunnerHome, "RUNNER_STATE_DIR": c.StateDir} {
		if !filepath.IsAbs(path) || filepath.Clean(path) == filepath.VolumeName(path)+string(os.PathSeparator) {
			return fmt.Errorf("%s must be an absolute non-root path", key)
		}
	}
	a, b := filepath.Clean(c.RunnerHome), filepath.Clean(c.StateDir)
	if within(a, b) || within(b, a) {
		return fmt.Errorf("RUNNER_HOME and RUNNER_STATE_DIR must be disjoint")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`).MatchString(c.Label) {
		return fmt.Errorf("RUNNER_LABEL must contain 1-100 ASCII letters, digits, dots, underscores or hyphens")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("LISTEN_ADDR: %w", err)
	}
	if c.Concurrency < 1 || c.QueueSize < 1 || c.RunnerGroupID < 1 || c.ShutdownTimeout < time.Second || c.RunnerTimeout < time.Second {
		return fmt.Errorf("invalid queue, runner group or timeout configuration")
	}
	return nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
