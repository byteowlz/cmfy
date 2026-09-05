package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cmfy/internal/comfy"
	"cmfy/internal/config"
	"cmfy/internal/jobs"
	"cmfy/internal/output"
	"cmfy/internal/workflow"
)

func emitJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func humanf(format string, args ...any) {
	if !quiet && !machineJSON {
		fmt.Printf(format, args...)
	}
}

func selectServer(cfg *config.Config, explicitURL string) error {
	if serverProfile != "" {
		profile, ok := cfg.Servers[serverProfile]
		if !ok {
			return fmt.Errorf("server profile %q is not configured", serverProfile)
		}
		if strings.TrimSpace(profile.URL) == "" {
			return fmt.Errorf("server profile %q has no URL", serverProfile)
		}
		cfg.ServerURL = profile.URL
	}
	if strings.TrimSpace(explicitURL) != "" {
		cfg.ServerURL = strings.TrimSpace(explicitURL)
	}
	return nil
}

func configuredClient(cfg *config.Config) (*comfy.Client, error) {
	return comfy.NewClientWithOptions(cfg.ServerURL, comfy.ClientOptions{
		MaxJSONBytes:   cfg.MaxJSONBytes,
		MaxUploadBytes: cfg.MaxUploadBytes,
		MaxOutputBytes: cfg.MaxOutputBytes,
	})
}

func configuredOutputLimits(cfg *config.Config) output.Limits {
	return output.Limits{MaxFileBytes: cfg.MaxOutputBytes, MaxTotalBytes: cfg.MaxTotalOutputBytes, MaxFiles: cfg.MaxOutputFiles}
}

func openJobStore() (*jobs.Store, error) {
	if stateDir == "" {
		return jobs.Open("")
	}
	return jobs.Open(filepath.Join(stateDir, "history.sqlite3"))
}

func splitKV(s string) (string, string, bool) {
	eq := strings.Index(s, "=")
	if eq <= 0 {
		return "", "", false
	}
	return s[:eq], s[eq+1:], true
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.6f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func getString(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok {
		switch t := v.(type) {
		case string:
			return t
		case json.Number:
			return t.String()
		}
	}
	return ""
}

func getMap(m map[string]any, k string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return nil
}

func applyStandardParams(cfg *config.Config, alias string, prompt map[string]any, params map[string]any) error {
	mappings := map[string]string(nil)
	if alias != "" {
		mappings = cfg.StandardWorkflowParams[alias]
	}
	return workflow.ApplyParameters(prompt, mappings, params)
}

func ResolveAlias(alias string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if v := cfg.StandardWorkflows[alias]; strings.TrimSpace(v) != "" {
		return v, nil
	}
	if _, _, err := workflow.Load(cfg.WorkflowsDir, alias); err == nil {
		return alias, nil
	}
	return "", fmt.Errorf("alias %q is not assigned; set with 'cmfy workflows assign %s <workflow>'", alias, alias)
}

func resolveAliasMaybe(name string) (string, bool) {
	cfg, err := config.Load()
	if err != nil {
		return "", false
	}
	if v := cfg.StandardWorkflows[name]; strings.TrimSpace(v) != "" {
		return v, true
	}
	return "", false
}
