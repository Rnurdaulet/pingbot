package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

type Config struct {
	Server    Server    `yaml:"server"`
	Telegram  Telegram  `yaml:"telegram"`
	Heartbeat Heartbeat `yaml:"heartbeat"`
	Storage   Storage   `yaml:"storage"`
	Defaults  Defaults  `yaml:"defaults"`
	Targets   []Target  `yaml:"targets"`
}

type Server struct {
	Addr string `yaml:"addr"`
	APIToken string `yaml:"api_token"` // Bearer for /api/*; empty = open
}

type Telegram struct {
	BotToken       string `yaml:"bot_token"`
	ChatID         string `yaml:"chat_id"`
	StartupNotify  *bool  `yaml:"startup_notify"`    // nil = true (send message on start)
	RequireOnStart bool   `yaml:"require_on_start"`  // if true, failed startup notify exits
}

// StartupNotifyEnabled reports whether a Telegram message should be sent on start.
// Omitted config defaults to enabled.
func (t Telegram) StartupNotifyEnabled() bool {
	if t.StartupNotify == nil {
		return true
	}
	return *t.StartupNotify
}

type Heartbeat struct {
	URL      string   `yaml:"url"`
	Interval Duration `yaml:"interval"`
}

type Storage struct {
	Driver string `yaml:"driver"` // "sqlite" (default) or "postgres"
	Path   string `yaml:"path"`   // sqlite file path
	DSN    string `yaml:"dsn"`    // postgres connection string
}

type Defaults struct {
	Interval      Duration `yaml:"interval"`
	Timeout       Duration `yaml:"timeout"`
	FailThreshold int      `yaml:"fail_threshold"`
	OkThreshold   int      `yaml:"ok_threshold"`
}

type Target struct {
	Name          string            `yaml:"name"`
	URL           string            `yaml:"url"`
	Method        string            `yaml:"method"`
	Headers       map[string]string `yaml:"headers"`
	Body          string            `yaml:"body"`
	ExpectStatus  []int             `yaml:"expect_status"`
	Interval      Duration          `yaml:"interval"`
	Timeout       Duration          `yaml:"timeout"`
	MaxLatencyMS  int               `yaml:"max_latency_ms"`
	FailThreshold int               `yaml:"fail_threshold"`
	OkThreshold   int               `yaml:"ok_threshold"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	expanded := os.Expand(string(raw), func(key string) string {
		return os.Getenv(key)
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "sqlite"
	}
	if c.Storage.Driver == "sqlite" && c.Storage.Path == "" {
		c.Storage.Path = "data/pingbot.db"
	}
	if c.Heartbeat.URL != "" && c.Heartbeat.Interval == 0 {
		c.Heartbeat.Interval = Duration(60 * time.Second)
	}
	if c.Defaults.Interval == 0 {
		c.Defaults.Interval = Duration(30 * time.Second)
	}
	if c.Defaults.Timeout == 0 {
		c.Defaults.Timeout = Duration(5 * time.Second)
	}
	if c.Defaults.FailThreshold == 0 {
		c.Defaults.FailThreshold = 3
	}
	if c.Defaults.OkThreshold == 0 {
		c.Defaults.OkThreshold = 2
	}

	for i := range c.Targets {
		t := &c.Targets[i]
		if t.Method == "" {
			t.Method = "GET"
		}
		if len(t.ExpectStatus) == 0 {
			t.ExpectStatus = []int{200}
		}
		if t.Interval == 0 {
			t.Interval = c.Defaults.Interval
		}
		if t.Timeout == 0 {
			t.Timeout = c.Defaults.Timeout
		}
		if t.FailThreshold == 0 {
			t.FailThreshold = c.Defaults.FailThreshold
		}
		if t.OkThreshold == 0 {
			t.OkThreshold = c.Defaults.OkThreshold
		}
	}
}

func (c *Config) validate() error {
	switch c.Storage.Driver {
	case "sqlite":
	case "postgres":
		if c.Storage.DSN == "" {
			return fmt.Errorf("storage.dsn is required for postgres driver")
		}
	default:
		return fmt.Errorf("storage.driver must be \"sqlite\" or \"postgres\", got %q", c.Storage.Driver)
	}
	if c.Telegram.BotToken == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if c.Telegram.ChatID == "" {
		return fmt.Errorf("telegram.chat_id is required")
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}

	seen := make(map[string]bool, len(c.Targets))
	for _, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("target name is required")
		}
		if t.URL == "" {
			return fmt.Errorf("target %q: url is required", t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate target name %q", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}
