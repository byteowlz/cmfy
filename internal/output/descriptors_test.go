package output_test

import (
	"testing"

	"cmfy/internal/output"
)

func TestDescriptorsAreStableAcrossNodesMediaKindsAndDuplicates(t *testing.T) {
	outputs := map[string]any{
		"9": map[string]any{
			"images":   []any{map[string]any{"filename": "clip.mp4", "subfolder": "video", "type": "output"}},
			"animated": []any{map[string]any{"filename": "clip.mp4", "subfolder": "video", "type": "output"}},
		},
		"2": map[string]any{
			"images": []any{
				map[string]any{"filename": "same.png", "subfolder": "one", "type": "output"},
				map[string]any{"filename": "same.png", "subfolder": "two", "type": "output"},
			},
		},
	}
	descriptors, err := output.Descriptors(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 3 {
		t.Fatalf("expected duplicate video descriptor to collapse: %#v", descriptors)
	}
	if descriptors[0].NodeID != "2" || descriptors[0].Subfolder != "one" || descriptors[1].Subfolder != "two" || descriptors[2].MediaType != "video/mp4" {
		t.Fatalf("unexpected deterministic descriptor order: %#v", descriptors)
	}
}
