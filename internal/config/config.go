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

// IncludeFilter scopes the report to specific job templates and/or projects.
// An empty filter means full mode (no filtering).
type IncludeFilter struct {
	TemplateIDs []int `yaml:"template_ids"`
	ProjectIDs  []int `yaml:"project_ids"`
}

// Active reports whether selective mode should be used.
func (i IncludeFilter) Active() bool { return len(i.TemplateIDs)+len(i.ProjectIDs) > 0 }

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
	Include            IncludeFilter    `yaml:"include"`

	// SummaryWorkers bounds how many job (or host) summary fetches run
	// concurrently. Total request throughput is still capped by the global
	// pacing gate at 1000/RequestPacingMS requests per second regardless of
	// worker count -- more workers only helps latency hidden behind
	// round-trip time, not the request rate itself.
	SummaryWorkers int `yaml:"summary_workers"`

	// SummaryStrategy selects how job_host_summary rows are fetched:
	// "per_job" (default) walks jobs/{id}/job_host_summaries/ for every job;
	// "per_host" walks hosts/{id}/job_host_summaries/ for every active host
	// instead, trading missing null-host/deleted-host rows for far fewer
	// requests on job-heavy controllers. There is no "auto" value.
	SummaryStrategy string `yaml:"summary_strategy"`

	// Token may come from the file (for scheduled runs with no shell to set
	// an env var) or from AWX_TOKEN. AWX_TOKEN wins when set; the file value
	// is the fallback.
	Token string `yaml:"token"`
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
	if envToken := strings.TrimSpace(os.Getenv("AWX_TOKEN")); envToken != "" {
		c.Token = envToken
	}
	c.Token = strings.TrimSpace(c.Token)
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
	if c.SummaryWorkers == 0 {
		c.SummaryWorkers = 4
	}
	if c.SummaryStrategy == "" {
		c.SummaryStrategy = "per_job"
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
		return fmt.Errorf("token is required: set the AWX_TOKEN environment variable or 'token' in the config file")
	}
	if c.PageSize < 1 || c.PageSize > 200 {
		return fmt.Errorf("page_size must be between 1 and 200")
	}
	if c.SummaryWorkers < 1 || c.SummaryWorkers > 8 {
		return fmt.Errorf("summary_workers must be between 1 and 8, got %d", c.SummaryWorkers)
	}
	if c.SummaryStrategy != "per_job" && c.SummaryStrategy != "per_host" {
		return fmt.Errorf("summary_strategy must be one of: per_job, per_host (got %q)", c.SummaryStrategy)
	}
	for _, id := range c.Include.TemplateIDs {
		if id < 1 {
			return fmt.Errorf("include.template_ids: id must be a positive integer, got %d", id)
		}
	}
	for _, id := range c.Include.ProjectIDs {
		if id < 1 {
			return fmt.Errorf("include.project_ids: id must be a positive integer, got %d", id)
		}
	}
	return nil
}
