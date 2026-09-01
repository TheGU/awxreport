package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_AppliesDefaults(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "base_url: https://awx.example.com\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIRoot != "/api/v2" {
		t.Errorf("APIRoot default = %q, want /api/v2", c.APIRoot)
	}
	if c.DaysBack != 30 {
		t.Errorf("DaysBack default = %d, want 30", c.DaysBack)
	}
	if c.PageSize != 200 {
		t.Errorf("PageSize default = %d, want 200", c.PageSize)
	}
	if c.RequestPacingMS != 200 {
		t.Errorf("RequestPacingMS default = %d, want 200", c.RequestPacingMS)
	}
	if c.MaxRetries != 5 {
		t.Errorf("MaxRetries default = %d, want 5", c.MaxRetries)
	}
	if c.OutputDir != "./out" {
		t.Errorf("OutputDir default = %q, want ./out", c.OutputDir)
	}
	if c.SummaryWorkers != 4 {
		t.Errorf("SummaryWorkers default = %d, want 4", c.SummaryWorkers)
	}
	if c.SummaryStrategy != "per_job" {
		t.Errorf("SummaryStrategy default = %q, want per_job", c.SummaryStrategy)
	}
}

func TestLoad_SummaryWorkersValidation(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	// summary_workers: 0 is treated as "use default" (like page_size), so it
	// passes validation; only out-of-range values should be rejected.
	for _, w := range []int{9, -1} {
		body := "base_url: https://awx.example.com\nsummary_workers: " + itoa(w) + "\n"
		p := writeYAML(t, body)
		if _, err := Load(p); err == nil {
			t.Errorf("summary_workers=%d should be rejected", w)
		}
	}
	for _, w := range []int{1, 8} {
		body := "base_url: https://awx.example.com\nsummary_workers: " + itoa(w) + "\n"
		p := writeYAML(t, body)
		c, err := Load(p)
		if err != nil {
			t.Errorf("summary_workers=%d should be accepted: %v", w, err)
			continue
		}
		if c.SummaryWorkers != w {
			t.Errorf("SummaryWorkers = %d, want %d", c.SummaryWorkers, w)
		}
	}
}

func TestLoad_SummaryStrategyValidation(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "base_url: https://awx.example.com\nsummary_strategy: auto\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("summary_strategy: auto should be rejected")
	}
	if !strings.Contains(err.Error(), "per_job") || !strings.Contains(err.Error(), "per_host") {
		t.Errorf("err = %q, want it to name the allowed values (per_job, per_host)", err.Error())
	}

	for _, strategy := range []string{"per_job", "per_host"} {
		p := writeYAML(t, "base_url: https://awx.example.com\nsummary_strategy: "+strategy+"\n")
		c, err := Load(p)
		if err != nil {
			t.Errorf("summary_strategy=%s should be accepted: %v", strategy, err)
			continue
		}
		if c.SummaryStrategy != strategy {
			t.Errorf("SummaryStrategy = %q, want %q", c.SummaryStrategy, strategy)
		}
	}
}

func TestLoad_TrimsTrailingSlashes(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "base_url: https://awx.example.com/\napi_root: /api/v2/\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BaseURL != "https://awx.example.com" {
		t.Errorf("BaseURL = %q, want trimmed", c.BaseURL)
	}
	if c.APIRoot != "/api/v2" {
		t.Errorf("APIRoot = %q, want trimmed", c.APIRoot)
	}
}

func TestLoad_LeadingSlashAddedToAPIRoot(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "base_url: https://awx.example.com\napi_root: api/controller/v2\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIRoot != "/api/controller/v2" {
		t.Errorf("APIRoot = %q, want leading slash added", c.APIRoot)
	}
}

func TestLoad_TokenFromEnv(t *testing.T) {
	t.Setenv("AWX_TOKEN", "  secret  ")
	p := writeYAML(t, "base_url: https://awx.example.com\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Token != "secret" {
		t.Errorf("Token = %q, want trimmed 'secret'", c.Token)
	}
}

func TestLoad_FailsOnMissingToken(t *testing.T) {
	t.Setenv("AWX_TOKEN", "")
	p := writeYAML(t, "base_url: https://awx.example.com\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing AWX_TOKEN")
	}
	want := "token is required: set the AWX_TOKEN environment variable or 'token' in the config file"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestLoad_TokenFromFileOnly(t *testing.T) {
	t.Setenv("AWX_TOKEN", "")
	p := writeYAML(t, "base_url: https://awx.example.com\ntoken: filetok\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Token != "filetok" {
		t.Errorf("Token = %q, want %q", c.Token, "filetok")
	}
}

func TestLoad_EnvOverridesFileToken(t *testing.T) {
	t.Setenv("AWX_TOKEN", "envtok")
	p := writeYAML(t, "base_url: https://awx.example.com\ntoken: filetok\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Token != "envtok" {
		t.Errorf("Token = %q, want %q (env should win over file)", c.Token, "envtok")
	}
}

func TestLoad_FailsOnMissingBaseURL(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "api_root: /api/v2\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing base_url")
	}
}

func TestLoad_FailsOnBadPageSize(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	// page_size: 0 is treated as "use default", so it passes validation.
	// Only out-of-range values should be rejected.
	for _, ps := range []int{201, -5} {
		body := "base_url: https://awx.example.com\npage_size: " + itoa(ps) + "\n"
		p := writeYAML(t, body)
		_, err := Load(p)
		if err == nil {
			t.Errorf("page_size=%d should be rejected", ps)
		}
	}
}

func TestLoad_IncludeBlockParses(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "base_url: https://awx.example.com\ninclude:\n  template_ids: [4, 9]\n  project_ids: [2]\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Include.Active() {
		t.Error("Include.Active() = false, want true")
	}
	if len(c.Include.TemplateIDs) != 2 || len(c.Include.ProjectIDs) != 1 {
		t.Errorf("Include = %+v, want template_ids=[4 9] project_ids=[2]", c.Include)
	}
}

func TestLoad_AbsentIncludeBlockIsInactive(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	p := writeYAML(t, "base_url: https://awx.example.com\n")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Include.Active() {
		t.Error("Include.Active() = true, want false for absent include block")
	}
}

func TestLoad_FailsOnNonPositiveIncludeID(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	for _, body := range []string{
		"base_url: https://awx.example.com\ninclude:\n  template_ids: [0]\n",
		"base_url: https://awx.example.com\ninclude:\n  project_ids: [-1]\n",
	} {
		p := writeYAML(t, body)
		if _, err := Load(p); err == nil {
			t.Errorf("body %q: expected validation error", body)
		}
	}
}

func TestLoad_FailsOnMissingFile(t *testing.T) {
	t.Setenv("AWX_TOKEN", "tok")
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

// itoa is a tiny stdlib-free int->string for table-driven tests where importing
// strconv just to format an int feels like overkill.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
