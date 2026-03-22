package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	defaultHeartbeatIntervalSec = 30
	defaultFirstTokenSec        = 30
	defaultTotalRequestSec      = 120
	defaultGatewayListenAddr    = "127.0.0.1:8080"
)

type Config struct {
	Node struct {
		Name string `yaml:"name"`
	} `yaml:"node"`

	Listen struct {
		TCPPort  int `yaml:"tcp_port"`
		QUICPort int `yaml:"quic_port"`
	} `yaml:"listen"`

	Network struct {
		BootstrapPeers []string `yaml:"bootstrap_peers"`
	} `yaml:"network"`

	Backend struct {
		Type    string `yaml:"type"`
		BaseURL string `yaml:"base_url"`
	} `yaml:"backend"`

	Models struct {
		Advertised []string `yaml:"advertised"`
	} `yaml:"models"`

	Heartbeat struct {
		IntervalSec int `yaml:"interval_sec"`
	} `yaml:"heartbeat"`

	Timeouts struct {
		FirstTokenSec   int `yaml:"first_token_sec"`
		TotalRequestSec int `yaml:"total_request_sec"`
	} `yaml:"timeouts"`

	Metrics struct {
		Enabled bool   `yaml:"enabled"`
		Listen  string `yaml:"listen"`
	} `yaml:"metrics"`

	Gateway struct {
		Listen string `yaml:"listen"`
	} `yaml:"gateway"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Heartbeat.IntervalSec == 0 {
		c.Heartbeat.IntervalSec = defaultHeartbeatIntervalSec
	}
	if c.Timeouts.FirstTokenSec == 0 {
		c.Timeouts.FirstTokenSec = defaultFirstTokenSec
	}
	if c.Timeouts.TotalRequestSec == 0 {
		c.Timeouts.TotalRequestSec = defaultTotalRequestSec
	}
	if c.Gateway.Listen == "" {
		c.Gateway.Listen = defaultGatewayListenAddr
	}
}

func (c *Config) Validate() error {
	if c.Node.Name == "" {
		return fmt.Errorf("node.name is required")
	}
	if err := validatePort("listen.tcp_port", c.Listen.TCPPort); err != nil {
		return err
	}
	if err := validatePort("listen.quic_port", c.Listen.QUICPort); err != nil {
		return err
	}
	if c.Backend.Type != "ollama" {
		return fmt.Errorf("backend.type must be \"ollama\" for v0.1")
	}
	if _, err := url.ParseRequestURI(c.Backend.BaseURL); err != nil {
		return fmt.Errorf("backend.base_url is invalid: %w", err)
	}
	if len(c.Models.Advertised) == 0 {
		return fmt.Errorf("models.advertised must contain at least one model")
	}
	if c.Heartbeat.IntervalSec <= 0 {
		return fmt.Errorf("heartbeat.interval_sec must be > 0")
	}
	if c.Timeouts.FirstTokenSec <= 0 {
		return fmt.Errorf("timeouts.first_token_sec must be > 0")
	}
	if c.Timeouts.TotalRequestSec <= 0 {
		return fmt.Errorf("timeouts.total_request_sec must be > 0")
	}
	if c.Gateway.Listen == "" {
		return fmt.Errorf("gateway.listen must not be empty")
	}
	return nil
}

func validatePort(name string, p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s must be in [1,65535], got %d", name, p)
	}
	return nil
}
