# Issues

## Open

### [trx-s45b] SaveVideo output not downloaded - cmfy only checks 'images' key (P1, bug)
When a workflow uses SaveVideo, ComfyUI returns output with both 'images' (containing .mp4 files) and 'animated' keys. The run_cmd output download loop correctly iterates om['images'], but reports 'no images saved' when the video was actually generated on the server. This may be due to the /view endpoint not serving video files correctly, or a timing issue with the history API. Need to investigate why SaveVideo outputs appear in /api/history but cmfy reports 0 saved files.

### [trx-8b2t] run_cmd uses Load() instead of LoadWithVars(), ignoring workflow variable defaults (P1, bug)
run_cmd.go line 123 calls workflow.Load() which does not parse the 'variables' section from workflow JSON files. This means: (1) Variable defaults defined in the workflow are never applied. (2) The 'variables' block is sent to ComfyUI as a node, causing 'missing_node_type' error for node ID '#variables'. Fix: Change workflow.Load() to workflow.LoadWithVars() in run_cmd.go and pass varDefaults to ApplyVarsWithDefaults() instead of ApplyVars().

### [trx-ncsv] ComfyUI export plugin should emit 'variables' block with defaults (P2, feature)
The ComfyUI-cmfy-export plugin should include a 'variables' section in the exported JSON with default values taken from the current workflow values. This way workflows are self-documenting and cmfy can use the defaults when flags are not provided. Depends on trx-8b2t (LoadWithVars fix) to work correctly.

