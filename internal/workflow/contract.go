package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var variablePattern = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

type Contract struct {
	Schema         string             `json:"schema"`
	Digest         string             `json:"digest"`
	NodeCount      int                `json:"node_count"`
	NodeClasses    []string           `json:"node_classes"`
	Variables      []ContractVariable `json:"variables"`
	RequiredAssets []string           `json:"required_assets"`
	Outputs        []ContractOutput   `json:"outputs"`
}

type ContractVariable struct {
	Name        string   `json:"name"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Locations   []string `json:"locations"`
}

type ContractOutput struct {
	NodeID    string `json:"node_id"`
	ClassType string `json:"class_type"`
	Kind      string `json:"kind"`
	Connected bool   `json:"connected"`
}

type Validation struct {
	Valid      bool              `json:"valid"`
	Digest     string            `json:"digest"`
	Unresolved []string          `json:"unresolved"`
	Errors     []ValidationError `json:"errors"`
}

type ValidationError struct {
	Code    string `json:"code"`
	NodeID  string `json:"node_id,omitempty"`
	Message string `json:"message"`
}

func Describe(prompt map[string]any, metadata map[string]VariableMetadata) (Contract, error) {
	encoded, err := json.Marshal(prompt)
	if err != nil {
		return Contract{}, fmt.Errorf("encode workflow contract: %w", err)
	}
	digest := sha256.Sum256(encoded)
	locations := map[string][]string{}
	classes := map[string]struct{}{}
	outputs := make([]ContractOutput, 0)
	nodeIDs := make([]string, 0, len(prompt))
	for nodeID := range prompt {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		node, ok := prompt[nodeID].(map[string]any)
		if !ok {
			continue
		}
		classType, _ := node["class_type"].(string)
		if classType != "" {
			classes[classType] = struct{}{}
		}
		inputs, _ := node["inputs"].(map[string]any)
		walkVariables(inputs, nodeID+".inputs", locations)
		if kind := outputKind(classType); kind != "" {
			outputs = append(outputs, ContractOutput{
				NodeID:    nodeID,
				ClassType: classType,
				Kind:      kind,
				Connected: containsLink(inputs),
			})
		}
	}
	classNames := make([]string, 0, len(classes))
	for className := range classes {
		classNames = append(classNames, className)
	}
	sort.Strings(classNames)
	variableNames := make([]string, 0, len(locations))
	for name := range locations {
		variableNames = append(variableNames, name)
	}
	sort.Strings(variableNames)
	variables := make([]ContractVariable, 0, len(variableNames))
	requiredAssets := make([]string, 0)
	for _, name := range variableNames {
		locationsForName := locations[name]
		sort.Strings(locationsForName)
		variable := ContractVariable{Name: name, Locations: locationsForName}
		if details, ok := metadata[name]; ok {
			variable.Default = details.Default
			variable.Description = details.Description
		}
		variables = append(variables, variable)
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "IMAGE") || strings.HasPrefix(upper, "MASK") || strings.HasPrefix(upper, "INPUT") {
			requiredAssets = append(requiredAssets, name)
		}
	}
	return Contract{
		Schema:         "cmfy/workflow-contract-v1",
		Digest:         "sha256:" + hex.EncodeToString(digest[:]),
		NodeCount:      len(nodeIDs),
		NodeClasses:    classNames,
		Variables:      variables,
		RequiredAssets: requiredAssets,
		Outputs:        outputs,
	}, nil
}

func Validate(prompt map[string]any, values map[string]string) (Validation, error) {
	contract, err := Describe(prompt, nil)
	if err != nil {
		return Validation{}, err
	}
	result := Validation{
		Digest:     contract.Digest,
		Unresolved: make([]string, 0),
		Errors:     make([]ValidationError, 0),
	}
	for _, variable := range contract.Variables {
		if _, ok := values[variable.Name]; !ok {
			result.Unresolved = append(result.Unresolved, variable.Name)
		}
	}
	if len(contract.Outputs) == 0 {
		result.Errors = append(result.Errors, ValidationError{Code: "no_output", Message: "workflow has no recognized output node"})
	}
	for _, output := range contract.Outputs {
		if !output.Connected {
			result.Errors = append(result.Errors, ValidationError{
				Code:    "output_not_connected",
				NodeID:  output.NodeID,
				Message: fmt.Sprintf("output node %s (%s) has no connected input", output.NodeID, output.ClassType),
			})
		}
	}
	result.Valid = len(result.Unresolved) == 0 && len(result.Errors) == 0
	return result, nil
}

func walkVariables(value any, location string, found map[string][]string) {
	switch typed := value.(type) {
	case string:
		for _, match := range variablePattern.FindAllStringSubmatch(typed, -1) {
			found[match[1]] = append(found[match[1]], location)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkVariables(typed[key], location+"."+key, found)
		}
	case []any:
		for index, item := range typed {
			walkVariables(item, fmt.Sprintf("%s[%d]", location, index), found)
		}
	}
}

func containsLink(value any) bool {
	switch typed := value.(type) {
	case []any:
		if len(typed) >= 2 {
			if source, ok := typed[0].(string); ok && source != "" {
				return true
			}
		}
		for _, item := range typed {
			if containsLink(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsLink(item) {
				return true
			}
		}
	}
	return false
}

func outputKind(classType string) string {
	lower := strings.ToLower(classType)
	if !strings.Contains(lower, "save") && !strings.Contains(lower, "preview") {
		return ""
	}
	switch {
	case strings.Contains(lower, "video"), strings.Contains(lower, "animated"), strings.Contains(lower, "gif"):
		return "video"
	case strings.Contains(lower, "audio"), strings.Contains(lower, "mp3"), strings.Contains(lower, "wav"):
		return "audio"
	case strings.Contains(lower, "image"), strings.Contains(lower, "preview"):
		return "image"
	default:
		return "file"
	}
}
