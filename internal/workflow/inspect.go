package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type NodeInfo struct {
	ID        string
	ClassType string
	Inputs    []string
}

type VariableCandidate struct {
	NodeID       string
	ClassType    string
	InputName    string
	CurrentValue interface{}
	SuggestedVar string
}

// Inspect builds a compact description of nodes for help/UX.
func Inspect(prompt map[string]interface{}) ([]NodeInfo, error) {
	var out []NodeInfo
	// Iterate deterministically by numeric-ish ID order
	ids := make([]string, 0, len(prompt))
	for id := range prompt {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		raw := prompt[id]
		nm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		ct, _ := nm["class_type"].(string)
		in, _ := nm["inputs"].(map[string]interface{})
		var keys []string
		for k := range in {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, NodeInfo{ID: id, ClassType: ct, Inputs: keys})
	}
	return out, nil
}

// FindNodesWithInput returns node IDs where inputs contain key.
func FindNodesWithInput(prompt map[string]interface{}, key string) []string {
	var ids []string
	for id, raw := range prompt {
		if nm, ok := raw.(map[string]interface{}); ok {
			if in, ok := nm["inputs"].(map[string]interface{}); ok {
				if _, ok := in[key]; ok {
					ids = append(ids, id)
				}
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// SetFirstByInput sets the first occurrence (or nth via occurrence index) of an input key.
func SetFirstByInput(prompt map[string]interface{}, key string, val interface{}, occurrence int) error {
	ids := FindNodesWithInput(prompt, key)
	if len(ids) == 0 {
		return fmt.Errorf("no input %q found", key)
	}
	if occurrence < 0 {
		occurrence = 0
	}
	if occurrence >= len(ids) {
		occurrence = len(ids) - 1
	}
	return SetPath(prompt, fmt.Sprintf("%s.inputs.%s", ids[occurrence], key), val)
}

func SuggestVariables(prompt map[string]interface{}) []VariableCandidate {
	var candidates []VariableCandidate
	usedNames := make(map[string]int)
	varPattern := regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

	ids := make([]string, 0, len(prompt))
	for id := range prompt {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		node, ok := prompt[id].(map[string]interface{})
		if !ok {
			continue
		}

		classType, _ := node["class_type"].(string)
		inputs, ok := node["inputs"].(map[string]interface{})
		if !ok {
			continue
		}

		inputKeys := make([]string, 0, len(inputs))
		for k := range inputs {
			inputKeys = append(inputKeys, k)
		}
		sort.Strings(inputKeys)

		for _, inputName := range inputKeys {
			value := inputs[inputName]

			if isNodeReference(value) {
				continue
			}

			suggested := suggestVariableName(classType, inputName, value, varPattern, usedNames)
			if suggested != "" {
				candidates = append(candidates, VariableCandidate{
					NodeID:       id,
					ClassType:    classType,
					InputName:    inputName,
					CurrentValue: value,
					SuggestedVar: suggested,
				})
			}
		}
	}

	return candidates
}

func isNodeReference(value interface{}) bool {
	arr, ok := value.([]interface{})
	return ok && len(arr) == 2
}

func suggestVariableName(classType, inputName string, value interface{}, varPattern *regexp.Regexp, usedNames map[string]int) string {
	strVal, isString := value.(string)

	if isString {
		if matches := varPattern.FindStringSubmatch(strVal); len(matches) == 2 {
			return matches[1]
		}
	}

	var baseName string

	commonInputs := map[string]string{
		"seed":            "SEED",
		"steps":           "STEPS",
		"cfg":             "CFG",
		"denoise":         "DENOISE",
		"width":           "WIDTH",
		"height":          "HEIGHT",
		"batch_size":      "BATCH_SIZE",
		"sampler_name":    "SAMPLER",
		"scheduler":       "SCHEDULER",
		"filename_prefix": "OUTPUT_PREFIX",
	}

	if name, ok := commonInputs[inputName]; ok {
		baseName = name
	} else if classType == "CLIPTextEncode" && inputName == "text" {
		if isString && (strings.Contains(strings.ToLower(strVal), "negative") || strings.Contains(strings.ToLower(classType), "negative")) {
			baseName = "NEGATIVE_PROMPT"
		} else {
			baseName = "PROMPT"
		}
	} else if classType == "LoadImage" && inputName == "image" {
		baseName = "INPUT_IMAGE"
	} else if isString {
		baseName = strings.ToUpper(inputName)
	} else {
		return ""
	}

	count := usedNames[baseName]
	usedNames[baseName]++

	if count == 0 {
		return baseName
	}
	return fmt.Sprintf("%s_%d", baseName, count+1)
}
