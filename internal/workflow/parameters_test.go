package workflow_test

import (
	"strings"
	"testing"

	"cmfy/internal/workflow"
)

func TestApplyParametersRequiresExactMappingWhenInputIsAmbiguous(t *testing.T) {
	prompt := map[string]any{
		"1": map[string]any{"class_type": "Sampler", "inputs": map[string]any{"steps": float64(10)}},
		"2": map[string]any{"class_type": "Sampler", "inputs": map[string]any{"steps": float64(20)}},
	}
	if err := workflow.ApplyParameters(prompt, nil, map[string]any{"steps": 30}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	if err := workflow.ApplyParameters(prompt, map[string]string{"steps": "2.inputs.steps"}, map[string]any{"steps": 30}); err != nil {
		t.Fatal(err)
	}
	if prompt["2"].(map[string]any)["inputs"].(map[string]any)["steps"] != 30 {
		t.Fatalf("mapped parameter was not applied: %#v", prompt)
	}
}
