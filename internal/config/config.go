package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	ServerURL              string
	OutputDir              string
	WorkflowsDir           string
	DefaultWorkflow        string
	DefaultOutputName      string
	DefaultWidth           int
	DefaultHeight          int
	DefaultSteps           int
	Vars                   map[string]string
	WorkflowVars           map[string]map[string]string
	StandardWorkflows      map[string]string
	StandardWorkflowParams map[string]map[string]string
}

func defaultConfig() *Config {
	return &Config{
		ServerURL:              "http://127.0.0.1:8188",
		OutputDir:              "outputs",
		WorkflowsDir:           "workflows",
		DefaultWorkflow:        "",
		DefaultOutputName:      "ComfyUI",
		DefaultWidth:           768,
		DefaultHeight:          768,
		DefaultSteps:           28,
		Vars:                   map[string]string{},
		WorkflowVars:           map[string]map[string]string{},
		StandardWorkflows:      map[string]string{},
		StandardWorkflowParams: map[string]map[string]string{},
	}
}

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
	cfg.StandardWorkflows = map[string]string{
		"txt2img":            "",
		"img2img":            "",
		"canny2img":          "",
		"depth2img":          "",
		"img2vid":            "",
		"txt2vid":            "",
		"txt2img_lora":       "",
		"img2img_inpainting": "",
		"rmb":                "",
	}
	data := cfg.ToTOML()
	return os.WriteFile(p, []byte(data), 0o644)
}

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

func Load() (*Config, error) {
	cfg := defaultConfig()

	v := viper.New()
	v.SetConfigType("toml")

	p, err := Path()
	if err != nil {
		return nil, err
	}

	v.SetConfigFile(p)

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if v.IsSet("server_url") {
		cfg.ServerURL = v.GetString("server_url")
	}
	if v.IsSet("output_dir") {
		cfg.OutputDir = expandPath(v.GetString("output_dir"))
	}
	if v.IsSet("workflows_dir") {
		cfg.WorkflowsDir = expandPath(v.GetString("workflows_dir"))
	}
	if v.IsSet("default_workflow") {
		cfg.DefaultWorkflow = expandPath(v.GetString("default_workflow"))
	}
	if v.IsSet("default_output_name") {
		cfg.DefaultOutputName = v.GetString("default_output_name")
	}
	if v.IsSet("default_width") {
		cfg.DefaultWidth = v.GetInt("default_width")
	}
	if v.IsSet("default_height") {
		cfg.DefaultHeight = v.GetInt("default_height")
	}
	if v.IsSet("default_steps") {
		cfg.DefaultSteps = v.GetInt("default_steps")
	}

	if v.IsSet("vars") {
		vars := v.GetStringMapString("vars")
		cfg.Vars = make(map[string]string, len(vars))
		for k, v := range vars {
			cfg.Vars[k] = expandPath(v)
		}
	}

	if v.IsSet("workflows") {
		workflows := v.GetStringMap("workflows")
		cfg.WorkflowVars = make(map[string]map[string]string)
		for name := range workflows {
			key := fmt.Sprintf("workflows.%s.vars", name)
			if v.IsSet(key) {
				vars := v.GetStringMapString(key)
				cfg.WorkflowVars[name] = make(map[string]string, len(vars))
				for k, val := range vars {
					cfg.WorkflowVars[name][k] = expandPath(val)
				}
			}
		}
	}

	if v.IsSet("standard_workflows") {
		sw := v.GetStringMapString("standard_workflows")
		cfg.StandardWorkflows = make(map[string]string, len(sw))
		for k, val := range sw {
			cfg.StandardWorkflows[k] = expandPath(val)
		}
	}

	if v.IsSet("standard_workflows_params") {
		swp := v.GetStringMap("standard_workflows_params")
		cfg.StandardWorkflowParams = make(map[string]map[string]string)
		for alias := range swp {
			key := fmt.Sprintf("standard_workflows_params.%s", alias)
			params := v.GetStringMapString(key)
			cfg.StandardWorkflowParams[alias] = make(map[string]string, len(params))
			for k, val := range params {
				cfg.StandardWorkflowParams[alias][k] = expandPath(val)
			}
		}
	}

	return cfg, nil
}

func (c *Config) ToTOML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "server_url = \"%s\"\n", c.ServerURL)
	fmt.Fprintf(&b, "output_dir = \"%s\"\n", c.OutputDir)
	fmt.Fprintf(&b, "workflows_dir = \"%s\"\n", c.WorkflowsDir)
	if c.DefaultWorkflow != "" {
		fmt.Fprintf(&b, "default_workflow = \"%s\"\n", c.DefaultWorkflow)
	}
	fmt.Fprintf(&b, "default_output_name = \"%s\"\n", c.DefaultOutputName)
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
		known := []string{"txt2img", "img2img", "canny2img", "depth2img", "img2vid", "txt2vid", "txt2img_lora", "img2img_inpainting", "rmb"}
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
