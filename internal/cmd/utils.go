package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"cmfy/internal/config"
	"cmfy/internal/workflow"
)

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
	if alias != "" {
		if m := cfg.StandardWorkflowParams[alias]; len(m) > 0 {
			for k, v := range params {
				if pth, ok := m[k]; ok {
					if err := workflow.SetPath(prompt, pth, v); err != nil {
						return err
					}
					delete(params, k)
				}
			}
		}
	}
	for k, v := range params {
		occ := 0
		key := k
		if strings.HasPrefix(k, "refiner.") {
			key = strings.TrimPrefix(k, "refiner.")
			occ = 1
		}
		_ = workflow.SetFirstByInput(prompt, key, v, occ)
	}
	return nil
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
