package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type VariableMetadata struct {
	Default     string `json:"default"`
	Description string `json:"description,omitempty"`
}

type PromptGuidelines struct {
	Summary   string   `json:"summary,omitempty"`
	Style     string   `json:"style,omitempty"`
	Dos       []string `json:"dos,omitempty"`
	Donts     []string `json:"donts,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
	Structure []string `json:"structure,omitempty"`
	Examples  []string `json:"examples,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// Load loads a workflow prompt JSON from name or path.
// If name is bare, reads from baseDir/<name>.json.
// Returns the prompt map, resolved path, variables metadata, and error.
func Load(baseDir, nameOrPath string) (map[string]interface{}, string, error) {
	p := resolveWorkflowPath(baseDir, nameOrPath)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", fmt.Errorf("invalid workflow JSON: %w", err)
	}
	hasNumericKeys := false
	for k := range m {
		if _, err := strconv.Atoi(k); err == nil {
			hasNumericKeys = true
			break
		}
	}
	if hasNumericKeys {
		return m, p, nil
	}
	if pr, ok := m["prompt"].(map[string]interface{}); ok {
		return pr, p, nil
	}
	return nil, "", errors.New("unsupported workflow JSON format: expected prompt map or {prompt: {...}}")
}

func LoadWithVars(baseDir, nameOrPath string) (map[string]interface{}, string, map[string]VariableMetadata, error) {
	p := resolveWorkflowPath(baseDir, nameOrPath)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", nil, fmt.Errorf("invalid workflow JSON: %w", err)
	}

	vars := make(map[string]VariableMetadata)
	if varsRaw, ok := m["variables"].(map[string]interface{}); ok {
		for k, v := range varsRaw {
			if vm, ok := v.(map[string]interface{}); ok {
				meta := VariableMetadata{}
				if def, ok := vm["default"].(string); ok {
					meta.Default = def
				}
				if desc, ok := vm["description"].(string); ok {
					meta.Description = desc
				}
				vars[k] = meta
			}
		}
	}

	hasNumericKeys := false
	for k := range m {
		if _, err := strconv.Atoi(k); err == nil {
			hasNumericKeys = true
			break
		}
	}
	if hasNumericKeys {
		return m, p, vars, nil
	}
	if pr, ok := m["prompt"].(map[string]interface{}); ok {
		return pr, p, vars, nil
	}
	return nil, "", nil, errors.New("unsupported workflow JSON format: expected prompt map or {prompt: {...}}")
}

func LoadPromptGuidelines(baseDir, nameOrPath string) (*PromptGuidelines, string, error) {
	p := resolveWorkflowPath(baseDir, nameOrPath)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", fmt.Errorf("invalid workflow JSON: %w", err)
	}

	raw, ok := m["prompt_guidelines"]
	if !ok {
		raw, ok = m["guidelines"]
	}
	if !ok {
		return nil, p, nil
	}

	pg := parsePromptGuidelines(raw)
	if pg == nil {
		return nil, p, nil
	}
	return pg, p, nil
}

func parsePromptGuidelines(raw interface{}) *PromptGuidelines {
	if raw == nil {
		return nil
	}
	pg := &PromptGuidelines{}
	switch v := raw.(type) {
	case string:
		pg.Summary = strings.TrimSpace(v)
	case map[string]interface{}:
		if s, ok := v["summary"].(string); ok {
			pg.Summary = strings.TrimSpace(s)
		}
		if s, ok := v["style"].(string); ok {
			pg.Style = strings.TrimSpace(s)
		}
		pg.Dos = toStringSlice(v["dos"])
		pg.Donts = toStringSlice(v["donts"])
		pg.Keywords = toStringSlice(v["keywords"])
		pg.Structure = toStringSlice(v["structure"])
		pg.Examples = toStringSlice(v["examples"])
		pg.Notes = toStringSlice(v["notes"])
	case []interface{}:
		pg.Notes = toStringSlice(v)
	default:
		return nil
	}

	if pg.Summary == "" && pg.Style == "" && len(pg.Dos) == 0 && len(pg.Donts) == 0 && len(pg.Keywords) == 0 && len(pg.Structure) == 0 && len(pg.Examples) == 0 && len(pg.Notes) == 0 {
		return nil
	}
	return pg
}

func toStringSlice(raw interface{}) []string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		return []string{s}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func resolveWorkflowPath(baseDir, nameOrPath string) string {
	p := nameOrPath
	if fileExists(p) {
		return p
	}
	if !strings.Contains(filepath.Base(p), ".") {
		return filepath.Join(baseDir, nameOrPath+".json")
	}
	return filepath.Join(baseDir, nameOrPath)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// List available workflows in baseDir.
func List(baseDir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			base := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			names = append(names, base)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// ApplyVars replaces ${KEY} in string inputs across the prompt.
// User-provided vars override defaults from varDefaults.
func ApplyVars(prompt map[string]interface{}, vars map[string]string) {
	ApplyVarsWithDefaults(prompt, vars, nil)
}

func ApplyVarsWithDefaults(prompt map[string]interface{}, vars map[string]string, varDefaults map[string]VariableMetadata) {
	merged := make(map[string]string)

	for k, v := range varDefaults {
		merged[k] = v.Default
	}

	for k, v := range vars {
		merged[k] = v
	}

	re := regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)
	for _, node := range prompt {
		if nm, ok := node.(map[string]interface{}); ok {
			if in, ok := nm["inputs"].(map[string]interface{}); ok {
				for k, v := range in {
					switch vv := v.(type) {
					case string:
						nv := re.ReplaceAllStringFunc(vv, func(s string) string {
							key := re.FindStringSubmatch(s)
							if len(key) == 2 {
								if r, ok := merged[key[1]]; ok {
									return r
								}
							}
							return s
						})
						if fullMatch := re.FindStringSubmatch(vv); len(fullMatch) == 2 && fullMatch[0] == vv {
							varVal := merged[fullMatch[1]]
							if iv, ok := toInt(varVal); ok {
								in[k] = iv
							} else if fv, ok := toFloat(varVal); ok {
								in[k] = fv
							} else {
								in[k] = nv
							}
						} else {
							in[k] = nv
						}
					}
				}
			}
		}
	}
}

// SetPath sets a value at path like "<nodeID>.inputs.<name>" under prompt map.
func SetPath(prompt map[string]interface{}, pathStr string, val interface{}) error {
	parts := strings.Split(pathStr, ".")
	if len(parts) < 3 || parts[1] != "inputs" {
		return fmt.Errorf("path must be '<nodeID>.inputs.<name>'")
	}
	nodeID := parts[0]
	inputName := strings.Join(parts[2:], ".")
	node, ok := prompt[nodeID]
	if !ok {
		return fmt.Errorf("node %s not found", nodeID)
	}
	nm, ok := node.(map[string]interface{})
	if !ok {
		return fmt.Errorf("node %s invalid", nodeID)
	}
	in, ok := nm["inputs"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("node %s has no inputs", nodeID)
	}
	in[inputName] = val
	return nil
}

// ApplySets applies multiple path=value overrides.
func ApplySets(prompt map[string]interface{}, sets []string) error {
	for _, s := range sets {
		if s == "" {
			continue
		}
		eq := strings.Index(s, "=")
		if eq <= 0 {
			return fmt.Errorf("--set expects path=value, got %q", s)
		}
		p := s[:eq]
		vraw := s[eq+1:]
		var v interface{} = vraw
		// Try to coerce numbers and booleans
		if iv, ok := toInt(vraw); ok {
			v = iv
		} else if fv, ok := toFloat(vraw); ok {
			v = fv
		} else if bv, ok := toBool(vraw); ok {
			v = bv
		} else {
			// strip quotes if provided
			if (strings.HasPrefix(vraw, "\"") && strings.HasSuffix(vraw, "\"")) || (strings.HasPrefix(vraw, "'") && strings.HasSuffix(vraw, "'")) {
				v = vraw[1 : len(vraw)-1]
			}
		}
		if err := SetPath(prompt, p, v); err != nil {
			return err
		}
	}
	return nil
}

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
	if !strings.Contains(s, ".") {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func toBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "on":
		return true, true
	case "false", "0", "no", "n", "off":
		return false, true
	}
	return false, false
}

func Save(path string, prompt map[string]interface{}, vars map[string]VariableMetadata) error {
	out := map[string]interface{}{
		"prompt": prompt,
	}

	if len(vars) > 0 {
		varsMap := make(map[string]interface{})
		for k, v := range vars {
			vm := map[string]interface{}{
				"default": v.Default,
			}
			if v.Description != "" {
				vm["description"] = v.Description
			}
			varsMap[k] = vm
		}
		out["variables"] = varsMap
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, b, 0o644)
}
