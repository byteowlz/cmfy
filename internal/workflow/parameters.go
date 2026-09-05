package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// ApplyParameters applies convenience parameters through exact mappings or an
// unambiguous single matching input. It never guesses between multiple nodes.
func ApplyParameters(prompt map[string]any, mappings map[string]string, parameters map[string]any) error {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := parameters[key]
		if target := strings.TrimSpace(mappings[key]); target != "" {
			if err := SetPath(prompt, target, value); err != nil {
				return fmt.Errorf("apply mapped parameter %s: %w", key, err)
			}
			continue
		}
		inputName := strings.TrimPrefix(key, "refiner.")
		nodeIDs := FindNodesWithInput(prompt, inputName)
		switch len(nodeIDs) {
		case 0:
			return fmt.Errorf("parameter %s has no mapped or matching workflow input", key)
		case 1:
			if err := SetPath(prompt, fmt.Sprintf("%s.inputs.%s", nodeIDs[0], inputName), value); err != nil {
				return fmt.Errorf("apply parameter %s: %w", key, err)
			}
		default:
			return fmt.Errorf("parameter %s is ambiguous across nodes %s; configure an exact standard_workflows_params mapping", key, strings.Join(nodeIDs, ", "))
		}
	}
	return nil
}
