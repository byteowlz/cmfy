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

// Load loads a workflow prompt JSON from name or path.
// If name is bare, reads from baseDir/<name>.json.
func Load(baseDir, nameOrPath string) (map[string]interface{}, string, error) {
	p := nameOrPath
	if !fileExists(p) {
		// try relative to baseDir with .json
		if !strings.Contains(filepath.Base(p), ".") {
			p = filepath.Join(baseDir, nameOrPath+".json")
		} else {
			p = filepath.Join(baseDir, nameOrPath)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", fmt.Errorf("invalid workflow JSON: %w", err)
	}
	// Some exports nest prompt under "prompt"; some are raw prompt map.
	// Check if this looks like a prompt map (has numeric string keys)
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
func ApplyVars(prompt map[string]interface{}, vars map[string]string) {
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
								if r, ok := vars[key[1]]; ok {
									return r
								}
							}
							return s
						})
						in[k] = nv
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
