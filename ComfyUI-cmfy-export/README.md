# cmfy Export Plugin for ComfyUI

A ComfyUI plugin that exports workflows to [cmfy](https://github.com/byteowlz/cmfy) format with automatic wildcard detection and customization.

## Features

- **Export Workflows**: Convert ComfyUI workflows to cmfy-compatible JSON format
- **Automatic Wildcard Detection**: Automatically identifies common parameter fields (prompts, seeds, dimensions, tags, lyrics, etc.)
- **Customizable Wildcards**: Select which fields should become wildcards before export
- **Menu Integration**: Adds an "Export to cmfy" button to the ComfyUI interface
- **Python Nodes**: Optional ComfyUI nodes for programmatic workflow conversion

## Installation

### Option 1: Symlink (Recommended)

Create a symlink to the plugin in your ComfyUI custom_nodes directory:

```bash
# Find your ComfyUI installation
# Common locations:
# - ~/ComfyUI/
# - /path/to/ComfyUI/

# Create symlink
cd /path/to/ComfyUI/custom_nodes
ln -s /Users/tommyfalkowski/cmfy/comfyui_plugin cmfy_export
```

### Option 2: Copy

Copy the entire plugin folder:

```bash
cp -r /Users/tommyfalkowski/cmfy/comfyui_plugin /path/to/ComfyUI/custom_nodes/cmfy_export
```

### Option 3: Git Clone

```bash
cd /path/to/ComfyUI/custom_nodes
git clone --depth 1 https://github.com/byteowlz/cmfy.git temp_cmfy
mv temp_cmfy/comfyui_plugin cmfy_export
rm -rf temp_cmfy
```

After installation, restart ComfyUI.

## Usage

### Via UI

1. Open ComfyUI
2. Create/load your workflow
3. Click the **"Export to cmfy"** button in the menu
4. A dialog will appear showing detected wildcard fields
5. Check/uncheck fields you want to convert to wildcards
6. Enter a filename and click **Export**

The workflow will be saved to `~/cmfy/workflows/` (or the configured cmfy workflows directory).

### Via ComfyUI Nodes

#### CmifyExport Node

```
Input:
  - workflow_json: Your workflow as JSON string
  - filename: Output filename
  - auto_detect_wildcards: Enable automatic detection
  - include_metadata: Include node metadata

Output:
  - status: Export status message
```

#### CmifyWildcardPreview Node

```
Input:
  - workflow_json: Your workflow as JSON string

Output:
  - wildcards_json: Detected wildcards as JSON
```

### Via API

```bash
# Export workflow via API
curl -X POST http://localhost:8188/cmfy/export \
  -H "Content-Type: application/json" \
  -d '{
    "workflow": {
      "3": {
        "inputs": { "seed": 12345, "steps": 20 },
        "class_type": "KSampler"
      }
    },
    "filename": "my_workflow.json"
  }'
```

## Wildcard Variables

The plugin automatically detects and converts these common fields:

| Variable | Used In | Type |
|----------|---------|------|
| `${PROMPT}` | CLIPTextEncode | string |
| `${TAGS}` | TextEncodeAceStepAudio* | string |
| `${LYRICS}` | TextEncodeAceStepAudio* | string |
| `${NEGATIVE}` | KSampler | string |
| `${SEED}` | KSampler, RandomNoise | integer |
| `${STEPS}` | KSampler, BasicScheduler | integer |
| `${CFG}` | KSampler | float |
| `${WIDTH}` | EmptyLatentImage | integer |
| `${HEIGHT}` | EmptyLatentImage | integer |
| `${DENOISE}` | KSampler | float |
| `${IMAGE}` | LoadImage | file |
| `${OUTPUT}` | SaveImage | string |
| `${GUIDANCE}` | FluxGuidance | float |

## cmfy Workflow Format

The exported workflow uses cmfy's wildcard system:

```json
{
  "3": {
    "inputs": {
      "seed": "${SEED}",
      "steps": "${STEPS}",
      "cfg": 8,
      "sampler_name": "euler",
      "model": ["4", 0],
      "positive": ["5", 0],
      "negative": ["6", 0],
      "latent_image": ["7", 0]
    },
    "class_type": "KSampler",
    "_meta": {
      "title": "KSampler"
    }
  }
}
```

## Configuration

Default wildcard fields are defined in `nodes.py` and `cmfy_export.js`. You can customize by editing these files:

### Python (for nodes)

Edit `DEFAULT_WILDCARD_FIELDS` in `nodes.py`:

```python
DEFAULT_WILDCARD_FIELDS = {
    "text": ["CLIPTextEncode", "Text"],
    # Add custom mappings...
}
```

### JavaScript (for web UI)

Edit `DEFAULT_WILDCARD_FIELDS` in `web/cmfy_export.js`:

```javascript
const DEFAULT_WILDCARD_FIELDS = {
    text: ["CLIPTextEncode", "Text"],
    // Add custom mappings...
};
```

## Running with cmfy

After exporting, use cmfy to run the workflow:

```bash
# List workflows
cmfy workflows list

# Run with parameters
cmfy run my_workflow --prompt "a beautiful sunset" --seed 12345 --steps 25

# Or use the cmfy config to set defaults
cmfy config set wildcards.PROMPT "your default prompt"
```

## Development

Project structure:

```
cmfy/
├── comfyui_plugin/
│   ├── __init__.py           # Plugin entry point
│   └── cmfy_export/
│       ├── nodes.py          # ComfyUI nodes
│       ├── http.py           # HTTP API handlers
│       └── web/
│           ├── __init__.py
│           └── cmfy_export.js  # Web UI
├── workflows/                # cmfy workflows
└── README.md
```

## License

MIT