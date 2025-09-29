# cmfy — A Flexible ComfyUI CLI

cmfy is a fast, flexible command‑line tool to run ComfyUI workflows. It loads JSON workflows from your filesystem, applies template variables and targeted overrides, submits them to a running ComfyUI server, and saves the generated outputs locally.


## Features

- Simple commands: `run`, `workflows` (list/show/inspect/assign), `server ping`, `config` (init/path), `version`.
- Works with local workflow JSONs; supports both raw prompt maps and `{ "prompt": { ... } }` wrappers.
- Configurable `workflows_dir` and `output_dir` in a TOML config stored under `$XDG_DATA_HOME/cmfy/config.toml`.
- Rich templating via `${KEY}` placeholders across string inputs.
- Precise overrides with `--set <nodeID>.inputs.<name>=<value>` (coerces ints/floats/bools; quoted strings preserved).
- Asset uploads for images/masks/inputs; exposes `${IMAGE}`, `${MASK}`, `${INPUT}` and enumerated variants.
- First‑class sampler/refiner flags (`--sampler`, `--scheduler`, `--steps`, `--cfg`, `--denoise`, etc.).
- Standard workflow aliases (e.g., `txt2img`, `img2img`, …) via `[standard_workflows]` and path mappings via `[standard_workflows_params.<alias>]`.


## Requirements

- Go 1.21+ to build.
- A running ComfyUI server (HTTP API) reachable at `server_url` (default `http://127.0.0.1:8188`).


## Install / Build

```bash
# From repository root
go build -o cmfy ./cmd/cmfy
./cmfy version
```


## Configuration

Config is stored at `$XDG_DATA_HOME/cmfy/config.toml`. If `XDG_DATA_HOME` is empty, falls back to `~/.local/share/cmfy/config.toml`.

Initialize a default config:

```bash
./cmfy config init
./cmfy config path  # prints the path
```

Config keys:

```toml
server_url = "http://127.0.0.1:8188"
output_dir = "outputs"
workflows_dir = "workflows"
default_workflow = ""       # optional fallback when -w is omitted
default_width = 768
default_height = 768
default_steps = 28

[vars]
# Global template vars available to all workflows
# MODEL = "sdxl.safetensors"
# CFG = "4.5"

[workflows.my_custom.vars]
# Per-workflow defaults (workflow name = file name without extension)
# PROMPT = "a cute cat"
# STEPS = "20"

[standard_workflows]
# Map well-known aliases to workflow names or absolute/relative JSON paths.
# If unset but a matching file exists (e.g., <workflows_dir>/txt2img.json), it is used implicitly.
txt2img = ""
img2img = ""
canny2img = ""
depth2img = ""
img2vid = ""
txt2vid = ""
txt2img_lora = ""
img2img_inpainting = ""

# Optional: precise paths for first-class flags for a given alias
[standard_workflows_params.txt2img]
# sampler_name = "12.inputs.sampler_name"
# scheduler    = "12.inputs.scheduler"
# steps        = "12.inputs.steps"
# cfg          = "12.inputs.cfg"
# denoise      = "12.inputs.denoise"

[standard_workflows_params.txt2img_lora]
# sampler_name       = "28.inputs.sampler_name"
# refiner.sampler_name = "42.inputs.sampler_name"
# refiner.steps        = "42.inputs.steps"
```


## Workflows

- Place JSON workflows under `workflows_dir` (default `./workflows`).
- Supported formats:
  - Raw prompt map: a JSON object with numeric keys for nodes: `{ "0": { ... }, "1": { ... }, ... }`.
  - Wrapper with `prompt` key: `{ "prompt": { "0": { ... } } }`.

Inspect a workflow to discover node IDs, class types, and inputs:

```bash
./cmfy workflows inspect txt2img
./cmfy workflows show txt2img     # prints JSON with prompt map
./cmfy workflows list             # lists available names in workflows_dir
```


## Running a Workflow

Basic run:

```bash
# By name (resolved in workflows_dir)
./cmfy run -w txt2img --prompt "a cozy cabin in the woods" --width 768 --height 768 --steps 30

# By path (absolute or relative)
./cmfy run -w ./workflows/my_flow.json --prompt "a sketch of an owl"
```

Using standard aliases:

```bash
# Assign or rely on implicit matching file (e.g., workflows/txt2img.json)
./cmfy workflows assign txt2img my_txt2img

# Then run directly by alias
./cmfy txt2img --prompt "sunlit woodland" --steps 28 --cfg 5.5 --sampler euler
```

Asset uploads:

```bash
./cmfy run -w img2img \
  --image input.png \
  --mask mask.png \
  --input controlnet_hint.png

# Exposes variables: ${IMAGE}, ${IMAGE1}, ${MASK}, ${INPUT}, etc.
# Use these variables in your workflow node string inputs.
```

Targeted overrides:

```bash
# Set any input on any node using <nodeID>.inputs.<name>=<value>
./cmfy run -w txt2img --set 12.inputs.steps=25 --set 12.inputs.cfg=5.0
./cmfy run -w img2img --set 8.inputs.denoise=0.6 --set 9.inputs.seed=1337

# Values are coerced when possible: ints, floats, bools; quoted strings stay strings
```

First‑class sampler/refiner flags:

```bash
./cmfy txt2img \
  --sampler euler --scheduler normal \
  --steps 30 --cfg 5.5 --denoise 0.2

./cmfy txt2img_lora \
  --sampler dpmpp_2m --refiner-sampler euler \
  --refiner-steps 15 --refiner-cfg 4.0
```

How first‑class flags are applied:

- If `[standard_workflows_params.<alias>]` mappings exist, flags (e.g., `steps`, `cfg`, `sampler_name`) are written to those exact paths.
- Otherwise, cmfy uses heuristics:
  - Base flags set the first node that has a matching input name.
  - `refiner.*` flags target the second occurrence (common in two‑stage flows).
- Use `workflows inspect` to find stable node IDs and add precise mappings in config.

Outputs:

- Saved to `output_dir` (default `./outputs`).
- Filenames are taken from the server’s `/view` endpoint responses.


## Template Variables

- Any string input in the workflow that contains `${KEY}` will be replaced with the value of `KEY`.
- Sources (precedence):
  1) Global `[vars]` in config
  2) `[workflows.<name>.vars]` in config
  3) Convenience flags (`--prompt`, `--seed`, `--width`, `--height`, `--steps`, `--cfg`)
  4) `--var KEY=VAL` on CLI
  5) Uploaded assets populate `${IMAGE}`, `${MASK}`, `${INPUT}` and enumerated variants
- Numeric inputs generally require `--set` unless you model them as strings in the workflow and template them.


## Server Utilities

```bash
./cmfy server ping      # checks connectivity to server_url
./cmfy version          # prints CLI version
```


## API Endpoints Used

- `POST /upload` — uploads files (form-data) as type `input`.
- `POST /prompt` — submits a prompt graph, returns `prompt_id`.
- `GET  /history/<prompt_id>` — tracks prompt execution and outputs.
- `GET  /view?filename=...&subfolder=...&type=...` — downloads generated assets.
- `GET  /system_stats` — used for `server ping`.


## Tips & Troubleshooting

- If `cmfy run` reports “no workflow specified”, set `default_workflow` in config or pass `-w`.
- If an alias is not set, cmfy will still run it implicitly if `<workflows_dir>/<alias>.json` exists.
- Use `workflows inspect` to discover inputs; then use `--set` or add mappings under `[standard_workflows_params.<alias>]` for stable behavior.
- The bundled TOML parser supports keys/strings/ints/floats/bools/arrays and simple sections. If you need advanced TOML features, open an issue to switch to a full TOML library.


## Roadmap

- Optional WebSocket progress and live preview support
- Built‑in templates for popular workflows
- More first‑class flags (e.g., clip_skip, scheduler params)
- Richer templating with type hints (e.g., `${WIDTH:int}`)


## Contributing

Issues and PRs are welcome. Please keep changes focused and consistent with the existing style. If you add new flags or config keys, update this README accordingly.

