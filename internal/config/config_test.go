package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ConfigTestSuite struct {
	suite.Suite
}

func (suite *ConfigTestSuite) TestLoadDefaultsSuite() {
	t := suite.Suite.T()
	cfg, err := Load("") // no file present in test cwd -> defaults
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.Services) == 0 {
		t.Fatal("expected default services")
	}
	rdap, ok := cfg.Service("rdap")
	if !ok {
		t.Fatal("expected default rdap service")
	}
	if rdap.BaseURL != "https://rdap.org" {
		t.Errorf("rdap base_url = %q, want https://rdap.org", rdap.BaseURL)
	}
	if !rdap.Enabled {
		t.Error("rdap should default to enabled")
	}
}

func (suite *ConfigTestSuite) TestLoadFileOverride() {
	t := suite.Suite.T()
	dir := t.TempDir()
	p := filepath.Join(dir, "gophish.toml")
	content := `
[services.rdap]
base_url = "https://rdap.example.com"
enabled = false
requests_per_minute = 5
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	rdap, ok := cfg.Service("rdap")
	if !ok {
		t.Fatal("expected rdap service")
	}
	if rdap.BaseURL != "https://rdap.example.com" {
		t.Errorf("rdap base_url = %q, want override", rdap.BaseURL)
	}
	if rdap.Enabled {
		t.Error("rdap should be disabled by override")
	}
	// Unspecified service keeps its default.
	if cfg.Services["crtsh"].BaseURL != "https://crt.sh" {
		t.Errorf("crtsh should retain default base_url, got %q", cfg.Services["crtsh"].BaseURL)
	}
}

func (suite *ConfigTestSuite) TestResolveKeysFromEnv() {
	t := suite.Suite.T()
	t.Setenv("TEST_PT_KEY", "super-secret")

	dir := t.TempDir()
	p := filepath.Join(dir, "gophish.toml")
	content := `
[services.phishtank]
base_url = "https://data.phishtank.com/data"
api_key_env = "TEST_PT_KEY"
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.Services["phishtank"].APIKey; got != "super-secret" {
		t.Errorf("APIKey = %q, want value from env", got)
	}
}

func (suite *ConfigTestSuite) TestMissingFileIsNotError() {
	t := suite.Suite.T()
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing config file should not error, got %v", err)
	}
	if len(cfg.Services) == 0 {
		t.Fatal("expected defaults when config file is missing")
	}
}

func TestConfigTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigTestSuite))
}
