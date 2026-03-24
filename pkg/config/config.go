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
	defaultX402Network          = "eip155:84532"
	defaultX402Asset            = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	defaultX402TokenName        = "USDC"
	defaultX402TokenVersion     = "2"
	defaultX402OutputTokens     = 256
)

type Config struct {
	Node struct {
		Name            string `yaml:"name"`
		IdentityKeyFile string `yaml:"identity_key_file"`
		X402            struct {
			Enabled             bool   `yaml:"enabled"`
			FacilitatorURL      string `yaml:"facilitator_url"`
			Network             string `yaml:"network"`
			Asset               string `yaml:"asset"`
			PayTo               string `yaml:"pay_to"`
			TokenName           string `yaml:"token_name"`
			TokenVersion        string `yaml:"token_version"`
			PricePer1KAtomic    int64  `yaml:"price_per_1k_atomic"`
			MinAmountAtomic     int64  `yaml:"min_amount_atomic"`
			DefaultOutputTokens int64  `yaml:"default_output_tokens"`
		} `yaml:"x402"`
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
		Advertised   []string                    `yaml:"advertised"`
		ModelPricing map[string]X402ModelPricing `yaml:"model_pricing"`
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
		X402   struct {
			ModelPricing map[string]X402ModelPricing `yaml:"model_pricing"`
		} `yaml:"x402"`
	} `yaml:"gateway"`
}

type X402ModelPricing struct {
	PricePer1KAtomic    int64 `yaml:"price_per_1k_atomic"`
	MinAmountAtomic     int64 `yaml:"min_amount_atomic"`
	MaxAmountAtomic     int64 `yaml:"max_amount_atomic"`
	DefaultOutputTokens int64 `yaml:"default_output_tokens"`
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
	if c.Node.X402.Network == "" {
		c.Node.X402.Network = defaultX402Network
	}
	if c.Node.X402.Asset == "" {
		c.Node.X402.Asset = defaultX402Asset
	}
	if c.Node.X402.TokenName == "" {
		c.Node.X402.TokenName = defaultX402TokenName
	}
	if c.Node.X402.TokenVersion == "" {
		c.Node.X402.TokenVersion = defaultX402TokenVersion
	}
	if c.Node.X402.DefaultOutputTokens == 0 {
		c.Node.X402.DefaultOutputTokens = defaultX402OutputTokens
	}
}

func (c *Config) Validate() error {
	if c.Node.Name == "" {
		return fmt.Errorf("node.name is required")
	}
	if c.Node.X402.PricePer1KAtomic < 0 {
		return fmt.Errorf("node.x402.price_per_1k_atomic must be >= 0")
	}
	if c.Node.X402.MinAmountAtomic < 0 {
		return fmt.Errorf("node.x402.min_amount_atomic must be >= 0")
	}
	if c.Node.X402.DefaultOutputTokens < 0 {
		return fmt.Errorf("node.x402.default_output_tokens must be >= 0")
	}
	if c.Node.X402.Enabled {
		if c.Node.X402.PayTo == "" {
			return fmt.Errorf("node.x402.enabled requires node.x402.pay_to")
		}
		if c.Node.X402.PricePer1KAtomic <= 0 {
			return fmt.Errorf("node.x402.enabled requires node.x402.price_per_1k_atomic > 0")
		}
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
	for model, pricing := range c.Models.ModelPricing {
		if err := validateX402ModelPricing("models.model_pricing", model, pricing); err != nil {
			return err
		}
	}
	for model, pricing := range c.Gateway.X402.ModelPricing {
		// Backward compatibility: allow legacy placement under gateway.x402.model_pricing.
		if err := validateX402ModelPricing("gateway.x402.model_pricing", model, pricing); err != nil {
			return err
		}
	}
	return nil
}

func validateX402ModelPricing(path, model string, pricing X402ModelPricing) error {
	if pricing.PricePer1KAtomic < 0 {
		return fmt.Errorf("%s[%q].price_per_1k_atomic must be >= 0", path, model)
	}
	if pricing.MinAmountAtomic < 0 {
		return fmt.Errorf("%s[%q].min_amount_atomic must be >= 0", path, model)
	}
	if pricing.MaxAmountAtomic < 0 {
		return fmt.Errorf("%s[%q].max_amount_atomic must be >= 0", path, model)
	}
	if pricing.DefaultOutputTokens < 0 {
		return fmt.Errorf("%s[%q].default_output_tokens must be >= 0", path, model)
	}
	if pricing.MaxAmountAtomic > 0 && pricing.MinAmountAtomic > 0 && pricing.MaxAmountAtomic < pricing.MinAmountAtomic {
		return fmt.Errorf("%s[%q].max_amount_atomic must be >= min_amount_atomic", path, model)
	}
	return nil
}

func validatePort(name string, p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s must be in [1,65535], got %d", name, p)
	}
	return nil
}
