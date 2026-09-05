package output

import (
	"fmt"
	"sort"
	"strings"
)

// Descriptors converts ComfyUI's node-keyed output map into a stable list of
// file references. Unknown output fields are ignored; malformed recognized
// entries fail loud instead of being silently dropped.
func Descriptors(outputs map[string]any) ([]Descriptor, error) {
	keys := []string{"images", "gifs", "animated", "videos", "audio"}
	nodeIDs := make([]string, 0, len(outputs))
	for nodeID := range outputs {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	result := make([]Descriptor, 0)
	for _, nodeID := range nodeIDs {
		node, ok := outputs[nodeID].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range keys {
			rawItems, present := node[key]
			if !present {
				continue
			}
			items, ok := rawItems.([]any)
			if !ok {
				return nil, fmt.Errorf("output node %s field %s is not an array", nodeID, key)
			}
			for index, raw := range items {
				item, ok := raw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("output node %s field %s item %d is not an object", nodeID, key, index)
				}
				filename, _ := item["filename"].(string)
				if strings.TrimSpace(filename) == "" {
					return nil, fmt.Errorf("output node %s field %s item %d has no filename", nodeID, key, index)
				}
				subfolder, _ := item["subfolder"].(string)
				typeName, _ := item["type"].(string)
				result = append(result, Descriptor{
					Filename:  filename,
					Subfolder: subfolder,
					Type:      typeName,
					MediaType: mediaTypeFor(key, filename),
					NodeID:    nodeID,
				})
			}
		}
	}
	return result, nil
}

func mediaTypeFor(kind, filename string) string {
	extension := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(extension, ".png"):
		return "image/png"
	case strings.HasSuffix(extension, ".jpg"), strings.HasSuffix(extension, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(extension, ".webp"):
		return "image/webp"
	case strings.HasSuffix(extension, ".gif"):
		return "image/gif"
	case strings.HasSuffix(extension, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(extension, ".webm"):
		return "video/webm"
	case strings.HasSuffix(extension, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(extension, ".wav"):
		return "audio/wav"
	case kind == "videos" || kind == "animated":
		return "video/*"
	case kind == "audio":
		return "audio/*"
	default:
		return "application/octet-stream"
	}
}
