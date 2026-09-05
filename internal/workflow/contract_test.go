package workflow_test

import (
	"testing"

	"cmfy/internal/workflow"
)

func TestDescribeProducesDeterministicWorkflowContract(t *testing.T) {
	t.Parallel()
	prompt := map[string]any{
		"9": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{"images": []any{"5", 0}}},
		"5": map[string]any{"class_type": "EmptyLatentImage", "inputs": map[string]any{"width": "${WIDTH}", "height": "${HEIGHT}"}},
		"3": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "${PROMPT}"}},
	}
	contract, err := workflow.Describe(prompt, map[string]workflow.VariableMetadata{
		"PROMPT": {Description: "Image prompt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if contract.Digest == "" || contract.NodeCount != 3 {
		t.Fatalf("unexpected contract: %#v", contract)
	}
	if len(contract.Variables) != 3 || contract.Variables[1].Name != "PROMPT" {
		t.Fatalf("variables are not stable/sorted: %#v", contract.Variables)
	}
	if len(contract.Outputs) != 1 || contract.Outputs[0].Kind != "image" || contract.Outputs[0].NodeID != "9" {
		t.Fatalf("unexpected outputs: %#v", contract.Outputs)
	}
	second, err := workflow.Describe(map[string]any{"3": prompt["3"], "5": prompt["5"], "9": prompt["9"]}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest != contract.Digest {
		t.Fatalf("digest depends on map iteration: %s != %s", second.Digest, contract.Digest)
	}
}

func TestValidateFailsLoudForMissingInputsAndOutputs(t *testing.T) {
	t.Parallel()
	prompt := map[string]any{
		"17": map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": "${IMAGE}"}},
		"18": map[string]any{"class_type": "PreviewImage", "inputs": map[string]any{}},
	}
	result, err := workflow.Validate(prompt, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || len(result.Unresolved) != 1 || result.Unresolved[0] != "IMAGE" {
		t.Fatalf("missing input not reported: %#v", result)
	}
	if len(result.Errors) == 0 || result.Errors[0].Code != "output_not_connected" {
		t.Fatalf("disconnected output not reported: %#v", result.Errors)
	}
	resolved, err := workflow.Validate(prompt, map[string]string{"IMAGE": "input.png"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Valid {
		t.Fatalf("disconnected output unexpectedly valid: %#v", resolved)
	}
}

func TestDescribeClassifiesImageVideoAudioAndRequiredAssets(t *testing.T) {
	t.Parallel()
	prompt := map[string]any{
		"1": map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": "${IMAGE}"}},
		"2": map[string]any{"class_type": "SaveVideo", "inputs": map[string]any{"video": []any{"1", 0}}},
		"3": map[string]any{"class_type": "SaveAudioMP3", "inputs": map[string]any{"audio": []any{"1", 1}}},
	}
	contract, err := workflow.Describe(prompt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.RequiredAssets) != 1 || contract.RequiredAssets[0] != "IMAGE" {
		t.Fatalf("unexpected required assets: %#v", contract.RequiredAssets)
	}
	if len(contract.Outputs) != 2 || contract.Outputs[0].Kind != "video" || contract.Outputs[1].Kind != "audio" {
		t.Fatalf("unexpected output kinds: %#v", contract.Outputs)
	}
}
