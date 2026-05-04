package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type ExcludeTemplates struct {
	IDs          []int    `yaml:"ids"`
	NameContains []string `yaml:"name_contains"`
}

type Config struct {
	BaseURL            string           `yaml:"base_url"`
	APIRoot            string           `yaml:"api_root"`
	DaysBack           int              `yaml:"days_back"`
	PageSize           int              `yaml:"page_size"`
	RequestPacingMS    int              `yaml:"request_pacing_ms"`
	HTTPTimeoutSec     int              `yaml:"http_timeout_sec"`
	MaxRetries         int              `yaml:"max_retries"`
	InsecureSkipVerify bool             `yaml:"insecure_skip_verify"`
	OutputDir          string           `yaml:"output_dir"`
	DebugDir           string           `yaml:"debug_dir"`
	ExcludeTemplates   ExcludeTemplates `yaml:"exclude_templates"`

	// Populated from env, never from the file.
	Token string `yaml:"-"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	c.Token = strings.TrimSpace(os.Getenv("AWX_TOKEN"))
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.APIRoot == "" {
		c.APIRoot = "/api/v2"
	}
	if c.DaysBack == 0 {
		c.DaysBack = 30
	}
	if c.PageSize == 0 {
		c.PageSize = 200
	}
	if c.RequestPacingMS == 0 {
		c.RequestPacingMS = 200
	}
	if c.HTTPTimeoutSec == 0 {
		c.HTTPTimeoutSec = 60
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 5
	}
	if c.OutputDir == "" {
		c.OutputDir = "./out"
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if !strings.HasPrefix(c.APIRoot, "/") {
		c.APIRoot = "/" + c.APIRoot
	}
	c.APIRoot = strings.TrimRight(c.APIRoot, "/")
}

func (c *Config) validate() error {
	if c.BaseURL == "" {
		return fmt.Errorf("base_url is required")
	}
	if c.Token == "" {
		return fmt.Errorf("AWX_TOKEN environment variable is required")
	}
	if c.PageSize < 1 || c.PageSize > 200 {
		return fmt.Errorf("page_size must be between 1 and 200")
	}
	return nil
}
