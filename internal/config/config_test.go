package config

import (
	"os"
	"path/filepath"
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
