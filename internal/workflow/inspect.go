package workflow

import (
    "fmt"
    "sort"
)

type NodeInfo struct {
    ID        string
    ClassType string
    Inputs    []string
}

// Inspect builds a compact description of nodes for help/UX.
func Inspect(prompt map[string]interface{}) ([]NodeInfo, error) {
    var out []NodeInfo
    // Iterate deterministically by numeric-ish ID order
    ids := make([]string, 0, len(prompt))
    for id := range prompt { ids = append(ids, id) }
    sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
    for _, id := range ids {
        raw := prompt[id]
        nm, ok := raw.(map[string]interface{})
        if !ok { continue }
        ct, _ := nm["class_type"].(string)
        in, _ := nm["inputs"].(map[string]interface{})
        var keys []string
        for k := range in { keys = append(keys, k) }
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
    if len(ids) == 0 { return fmt.Errorf("no input %q found", key) }
    if occurrence < 0 { occurrence = 0 }
    if occurrence >= len(ids) { occurrence = len(ids)-1 }
    return SetPath(prompt, fmt.Sprintf("%s.inputs.%s", ids[occurrence], key), val)
}

