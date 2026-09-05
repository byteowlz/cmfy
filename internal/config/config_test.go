package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cmfy/internal/config"
)

func TestLoadHonorsExplicitConfigAndServerProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.toml")
	body := `server_url = "http://127.0.0.1:8188"
workflows_dir = "workflows"
output_dir = "outputs"
max_json_bytes = 1024
max_upload_bytes = 2048
max_output_bytes = 4096
max_total_output_bytes = 8192
max_output_files = 12
[servers.gpu]
url = "https://comfy.example.test"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMFY_CONFIG", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["gpu"].URL != "https://comfy.example.test" || cfg.MaxOutputFiles != 12 || cfg.MaxTotalOutputBytes != 8192 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	serialized := cfg.ToTOML()
	if !strings.Contains(serialized, "[servers.gpu]") || !strings.Contains(serialized, "max_output_files = 12") {
		t.Fatalf("serialized config omitted new fields:\n%s", serialized)
	}
}

func TestLoadRejectsNonPositiveLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(path, []byte("max_output_bytes = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMFY_CONFIG", path)
	if _, err := config.Load(); err == nil {
		t.Fatal("expected non-positive output limit to fail")
	}
}
