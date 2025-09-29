package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config represents cmfy configuration loaded from TOML.
type Config struct {
	ServerURL              string
	OutputDir              string
	WorkflowsDir           string
	DefaultWorkflow        string
	DefaultWidth           int
	DefaultHeight          int
	DefaultSteps           int
	Vars                   map[string]string
	WorkflowVars           map[string]map[string]string // [workflowName]vars
	StandardWorkflows      map[string]string            // alias -> workflow name/path
	StandardWorkflowParams map[string]map[string]string // alias -> param -> path
}

func defaultConfig() *Config {
	return &Config{
		ServerURL:              "http://127.0.0.1:8188",
		OutputDir:              "outputs",
		WorkflowsDir:           "workflows",
		DefaultWorkflow:        "",
		DefaultWidth:           768,
		DefaultHeight:          768,
		DefaultSteps:           28,
		Vars:                   map[string]string{},
		WorkflowVars:           map[string]map[string]string{},
		StandardWorkflows:      map[string]string{},
		StandardWorkflowParams: map[string]map[string]string{},
	}
}

// Path returns the path to the config.toml file.
func Path() (string, error) {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		xdg = filepath.Join(home, ".config")
	}
	dir := filepath.Join(xdg, "cmfy")
	return filepath.Join(dir, "config.toml"), nil
}

// InitDefault writes a default config file if it does not exist.
func InitDefault() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	cfg := defaultConfig()
	// Pre-populate known aliases with empty values for discoverability
	cfg.StandardWorkflows = map[string]string{
		"txt2img":            "",
		"img2img":            "",
		"canny2img":          "",
		"depth2img":          "",
		"img2vid":            "",
		"txt2vid":            "",
		"txt2img_lora":       "",
		"img2img_inpainting": "",
	}
	data := cfg.ToTOML()
	return os.WriteFile(p, []byte(data), 0o644)
}

// Save writes the config back to disk.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(c.ToTOML()), 0o644)
}

// Load reads config from TOML, falling back to defaults when missing.
func Load() (*Config, error) {
	cfg := defaultConfig()
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}
	m, err := parseTOML(string(b))
	if err != nil {
		return nil, err
	}
	// Flat keys
	if v := str(m["server_url"]); v != "" {
		cfg.ServerURL = v
	}
	if v := str(m["output_dir"]); v != "" {
		cfg.OutputDir = expandPath(v)
	}
	if v := str(m["workflows_dir"]); v != "" {
		cfg.WorkflowsDir = expandPath(v)
	}
	if v := str(m["default_workflow"]); v != "" {
		cfg.DefaultWorkflow = expandPath(v)
	}
	if v := asInt(m["default_width"]); v != 0 {
		cfg.DefaultWidth = v
	}
	if v := asInt(m["default_height"]); v != 0 {
		cfg.DefaultHeight = v
	}
	if v := asInt(m["default_steps"]); v != 0 {
		cfg.DefaultSteps = v
	}
	// [vars]
	if vm, ok := m["vars"].(map[string]interface{}); ok {
		if cfg.Vars == nil {
			cfg.Vars = map[string]string{}
		}
		for k, v := range vm {
			cfg.Vars[k] = expandPath(fmt.Sprint(v))
		}
	}
	// [workflows.<name>.vars]
	if wfm, ok := m["workflows"].(map[string]interface{}); ok {
		for name, v := range wfm {
			if sect, ok := v.(map[string]interface{}); ok {
				if vars, ok := sect["vars"].(map[string]interface{}); ok {
					if cfg.WorkflowVars == nil {
						cfg.WorkflowVars = map[string]map[string]string{}
					}
					m2 := map[string]string{}
					for k, vv := range vars {
						m2[k] = expandPath(fmt.Sprint(vv))
					}
					cfg.WorkflowVars[name] = m2
				}
			}
		}
	}
	// [standard_workflows]
	if sw, ok := m["standard_workflows"].(map[string]interface{}); ok {
		if cfg.StandardWorkflows == nil {
			cfg.StandardWorkflows = map[string]string{}
		}
		for k, v := range sw {
			cfg.StandardWorkflows[k] = expandPath(fmt.Sprint(v))
		}
	}
	// [standard_workflows_params.<alias>]
	if swp, ok := m["standard_workflows_params"].(map[string]interface{}); ok {
		if cfg.StandardWorkflowParams == nil {
			cfg.StandardWorkflowParams = map[string]map[string]string{}
		}
		for alias, v := range swp {
			if sect, ok := v.(map[string]interface{}); ok {
				mm := map[string]string{}
				for k, vv := range sect {
					mm[k] = expandPath(fmt.Sprint(vv))
				}
				cfg.StandardWorkflowParams[alias] = mm
			}
		}
	}
	return cfg, nil
}

// ToTOML renders a simple TOML from the config.
func (c *Config) ToTOML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "server_url = \"%s\"\n", c.ServerURL)
	fmt.Fprintf(&b, "output_dir = \"%s\"\n", c.OutputDir)
	fmt.Fprintf(&b, "workflows_dir = \"%s\"\n", c.WorkflowsDir)
	if c.DefaultWorkflow != "" {
		fmt.Fprintf(&b, "default_workflow = \"%s\"\n", c.DefaultWorkflow)
	}
	fmt.Fprintf(&b, "default_width = %d\n", c.DefaultWidth)
	fmt.Fprintf(&b, "default_height = %d\n", c.DefaultHeight)
	fmt.Fprintf(&b, "default_steps = %d\n", c.DefaultSteps)
	if len(c.Vars) > 0 {
		fmt.Fprintf(&b, "\n[vars]\n")
		for k, v := range c.Vars {
			fmt.Fprintf(&b, "%s = \"%s\"\n", k, escapeString(v))
		}
	}
	if len(c.WorkflowVars) > 0 {
		fmt.Fprintf(&b, "\n")
		for name, m := range c.WorkflowVars {
			fmt.Fprintf(&b, "[workflows.%s.vars]\n", name)
			for k, v := range m {
				fmt.Fprintf(&b, "%s = \"%s\"\n", k, escapeString(v))
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	if len(c.StandardWorkflows) > 0 {
		fmt.Fprintf(&b, "[standard_workflows]\n")
		known := []string{"txt2img", "img2img", "canny2img", "depth2img", "img2vid", "txt2vid", "txt2img_lora", "img2img_inpainting"}
		emitted := map[string]bool{}
		for _, k := range known {
			if v, ok := c.StandardWorkflows[k]; ok {
				fmt.Fprintf(&b, "%s = \"%s\"\n", k, escapeString(v))
				emitted[k] = true
			}
		}
		for k, v := range c.StandardWorkflows {
			if emitted[k] {
				continue
			}
			fmt.Fprintf(&b, "%s = \"%s\"\n", k, escapeString(v))
		}
	}
	if len(c.StandardWorkflowParams) > 0 {
		for alias, m := range c.StandardWorkflowParams {
			if len(m) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n[standard_workflows_params.%s]\n", alias)
			for k, v := range m {
				fmt.Fprintf(&b, "%s = \"%s\"\n", k, escapeString(v))
			}
		}
	}
	return b.String()
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	if strings.HasPrefix(p, "$HOME/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[6:])
		}
	}
	return os.ExpandEnv(p)
}

// Minimal TOML parser sufficient for our config structure.
func parseTOML(src string) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	cur := out
	sections := []string{}

	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		line := strings.TrimSpace(ln)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sect := strings.TrimSpace(line[1 : len(line)-1])
			if sect == "" {
				return nil, fmt.Errorf("toml: empty section on line %d", i+1)
			}
			sections = strings.Split(sect, ".")
			cur = out
			for _, s := range sections {
				if s == "" {
					return nil, fmt.Errorf("toml: bad section on line %d", i+1)
				}
				m, ok := cur[s].(map[string]interface{})
				if !ok {
					m = map[string]interface{}{}
					cur[s] = m
				}
				cur = m
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("toml: expected key=value on line %d", i+1)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return nil, fmt.Errorf("toml: empty key on line %d", i+1)
		}
		v, err := parseTOMLValue(val)
		if err != nil {
			return nil, fmt.Errorf("toml: line %d: %w", i+1, err)
		}
		cur[key] = v
	}
	return out, nil
}

func parseTOMLValue(v string) (interface{}, error) {
	// strip comments at end
	if idx := strings.IndexByte(v, '#'); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	if v == "" {
		return "", nil
	}
	if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") && len(v) >= 2 {
		s := v[1 : len(v)-1]
		s = strings.ReplaceAll(s, "\\\"", "\"")
		s = strings.ReplaceAll(s, "\\\\", "\\")
		return s, nil
	}
	if v == "true" || v == "false" {
		return v == "true", nil
	}
	// int
	if iv, ok := toInt(v); ok {
		return iv, nil
	}
	// float
	if fv, ok := toFloat(v); ok {
		return fv, nil
	}
	// array of strings or numbers (very simple)
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := strings.TrimSpace(v[1 : len(v)-1])
		if inner == "" {
			return []interface{}{}, nil
		}
		parts := splitComma(inner)
		arr := make([]interface{}, 0, len(parts))
		for _, p := range parts {
			x, err := parseTOMLValue(strings.TrimSpace(p))
			if err != nil {
				return nil, err
			}
			arr = append(arr, x)
		}
		return arr, nil
	}
	// fallback raw string
	return v, nil
}

func splitComma(s string) []string {
	var parts []string
	var cur strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inStr = !inStr
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' && !inStr {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	parts = append(parts, cur.String())
	return parts
}

func str(v interface{}) string { return fmt.Sprint(v) }

func toInt(s string) (int, bool) {
	neg := false
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

func toFloat(s string) (float64, bool) {
	// extremely basic: require at least one '.'
	if !strings.Contains(s, ".") {
		return 0, false
	}
	// Delegate to fmt
	var f float64
	_, err := fmt.Sscan(s, &f)
	if err != nil {
		return 0, false
	}
	return f, true
}

func asInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if i, ok := toInt(t); ok {
			return i
		}
	}
	return 0
}
