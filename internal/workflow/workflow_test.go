package workflow

import (
	"encoding/json"
	"testing"
)

func TestApplyVarsWithDefaults(t *testing.T) {
	prompt := map[string]interface{}{
		"1": map[string]interface{}{
			"inputs": map[string]interface{}{
				"seed": "${SEED}",
				"steps": "${STEPS}",
			},
		},
	}
	
	defaults := map[string]VariableMetadata{
		"SEED": {Default: "12345"},
		"STEPS": {Default: "20"},
	}
	
	userVars := map[string]string{
		"SEED": "99999",
	}
	
	ApplyVarsWithDefaults(prompt, userVars, defaults)
	
	node := prompt["1"].(map[string]interface{})
	inputs := node["inputs"].(map[string]interface{})
	
	if inputs["seed"] != 99999 {
		t.Errorf("Expected seed to be 99999 (user override), got %v", inputs["seed"])
	}
	
	if inputs["steps"] != 20 {
		t.Errorf("Expected steps to be 20 (default), got %v", inputs["steps"])
	}
}

func TestSuggestVariables(t *testing.T) {
	prompt := map[string]interface{}{
		"3": map[string]interface{}{
			"class_type": "KSampler",
			"inputs": map[string]interface{}{
				"seed": 12345,
				"steps": 20,
			},
		},
		"6": map[string]interface{}{
			"class_type": "CLIPTextEncode",
			"inputs": map[string]interface{}{
				"text": "a beautiful landscape",
			},
		},
	}
	
	candidates := SuggestVariables(prompt)
	
	if len(candidates) != 3 {
		t.Errorf("Expected 3 candidates, got %d", len(candidates))
	}
	
	found := make(map[string]bool)
	for _, c := range candidates {
		found[c.SuggestedVar] = true
	}
	
	if !found["SEED"] {
		t.Error("Expected SEED variable suggestion")
	}
	if !found["STEPS"] {
		t.Error("Expected STEPS variable suggestion")
	}
	if !found["PROMPT"] {
		t.Error("Expected PROMPT variable suggestion")
	}
}

func TestSaveAndLoad(t *testing.T) {
	prompt := map[string]interface{}{
		"1": map[string]interface{}{
			"inputs": map[string]interface{}{
				"seed": "${SEED}",
			},
		},
	}
	
	vars := map[string]VariableMetadata{
		"SEED": {Default: "12345", Description: "Random seed"},
	}
	
	tmpFile := "/tmp/test_workflow_save.json"
	
	if err := Save(tmpFile, prompt, vars); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	
	loadedPrompt, _, loadedVars, err := LoadWithVars("", tmpFile)
	if err != nil {
		t.Fatalf("LoadWithVars failed: %v", err)
	}
	
	if len(loadedVars) != 1 {
		t.Errorf("Expected 1 variable, got %d", len(loadedVars))
	}
	
	if loadedVars["SEED"].Default != "12345" {
		t.Errorf("Expected SEED default to be 12345, got %s", loadedVars["SEED"].Default)
	}
	
	if loadedVars["SEED"].Description != "Random seed" {
		t.Errorf("Expected SEED description to be 'Random seed', got %s", loadedVars["SEED"].Description)
	}
	
	promptJSON, _ := json.Marshal(prompt)
	loadedJSON, _ := json.Marshal(loadedPrompt)
	if string(promptJSON) != string(loadedJSON) {
		t.Error("Loaded prompt doesn't match saved prompt")
	}
}
