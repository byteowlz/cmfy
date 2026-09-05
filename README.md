# cmfy — A Flexible ComfyUI CLI

cmfy is a fast, flexible command‑line tool to run ComfyUI workflows. It loads JSON workflows from your filesystem, applies template variables and targeted overrides, submits them to a running ComfyUI server, and saves the generated outputs locally.


## Features

- One shared execution path for `run`, aliases, batch jobs, durable job operations, and automation adapters: Resolve → Plan → Submit → Observe → Collect.
- Durable SQLite/WAL job and provenance history with idempotent request IDs, retry, filtering, cursor pagination, cleanup, and restart recovery.
- Secure bounded HTTP transport, content-hash upload reuse, resumable output collection, containment checks, and collision-safe atomic publication.
- Versioned JSON results and JSONL progress events suitable for terminal users, agents, and Oqto Apps.
- Deterministic workflow describe/validate/plan contracts and optional server node dependency validation.
- Works with local workflow JSONs; supports both raw prompt maps and `{ "prompt": { ... } }` wrappers.
- Configurable `workflows_dir` and `output_dir` in a TOML config stored under `$XDG_CONFIG_HOME/cmfy/config.toml`.
- Rich templating via `${KEY}` placeholders across string inputs.
- Precise overrides with `--set <nodeID>.inputs.<name>=<value>` (coerces ints/floats/bools; quoted strings preserved).
- Asset uploads for images/masks/inputs; exposes `${IMAGE}`, `${MASK}`, `${INPUT}` and enumerated variants.
- First‑class sampler/refiner flags (`--sampler`, `--scheduler`, `--steps`, `--cfg`, `--denoise`, etc.).
- Music prompt convenience flags: `--tags` and `--lyrics` map to `${TAGS}` and `${LYRICS}`.
- Standard workflow aliases (e.g., `txt2img`, `img2img`, …) via `[standard_workflows]` and path mappings via `[standard_workflows_params.<alias>]`.


## Requirements

- Go 1.23+ to build.
- A running ComfyUI server (HTTP API) reachable at `server_url` (default `http://127.0.0.1:8188`).


## Install / Build

```bash
# From repository root
go build -o cmfy ./cmd/cmfy
./cmfy version
```

Or use `just`:

```bash
just help
just build
just test
just check
```


## Configuration

Config is stored at `$XDG_CONFIG_HOME/cmfy/config.toml`. If `XDG_CONFIG_HOME` is empty, falls back to `~/.config/cmfy/config.toml`.

By default, `output_dir` is `./outputs` (relative to the current working directory where you run `cmfy`).

Initialize a default config:

```bash
./cmfy config init
./cmfy config path  # prints the path
```

Config keys:

```toml
"$schema" = "https://raw.githubusercontent.com/byteowlz/schemas/refs/heads/main/cmfy/cmfy.config.schema.json"

server_url = "http://127.0.0.1:8188"

# Optional named API endpoint profiles. Credentials must remain in an external
# credential mechanism; URLs containing userinfo are rejected.
[servers.local_gpu]
url = "http://127.0.0.1:8188"
output_dir = "./outputs"
workflows_dir = "workflows"
default_workflow = ""       # optional fallback when -w is omitted
default_width = 768
default_height = 768
default_steps = 28

# Positive transport/materialization bounds (bytes/count)
max_json_bytes = 8388608
max_upload_bytes = 1073741824
max_output_bytes = 536870912
max_total_output_bytes = 1073741824
max_output_files = 256

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
txt2music = ""
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
# sampler_name         = "28.inputs.sampler_name"
# refiner.sampler_name = "42.inputs.sampler_name"
# refiner.steps        = "42.inputs.steps"

# Optional: named remote servers for SSH workflow discovery/import
[remote_servers.local_gpu]
ssh_config_host = "local-gpu"
workflows_dir = "~/ComfyUI/user/default/workflows"
# host = "192.168.1.20"
# user = "agent"
# port = 22
# key_path = "~/.ssh/id_ed25519"
```


## Workflows

- Place JSON workflows under `workflows_dir` (default `./workflows`).
- Supported formats:
  - Raw prompt map: a JSON object with numeric keys for nodes: `{ "0": { ... }, "1": { ... }, ... }`.
  - Wrapper with `prompt` key: `{ "prompt": { "0": { ... } } }`.
- Optional workflow metadata keys:
  - `variables`: variable defaults/descriptions.
  - `prompt_guidelines` (or `guidelines`): authoring hints for prompt/tag writing.

Inspect a workflow to discover node IDs, class types, and inputs:

```bash
./cmfy workflows inspect txt2img
./cmfy workflows inspect txt2music --guidelines  # prints optional prompting guidance
./cmfy workflows show txt2img      # prints JSON with prompt map
./cmfy workflows list              # lists available names in workflows_dir
./cmfy --json workflows describe txt2img
./cmfy --json workflows validate txt2img --var PROMPT="a forest owl"

# Resolve parameters/assets and verify required node classes against the target server.
./cmfy --json run --plan -w txt2img --prompt "a forest owl"

# From SSH-configured remote servers in config.toml
./cmfy workflows ssh-list local_gpu
./cmfy workflows ssh-list local_gpu flux --json
./cmfy workflows ssh-import local_gpu Flux_upscale.json
```


## Running a Workflow

Basic run:

```bash
# By name (resolved in workflows_dir)
./cmfy run -w txt2img --prompt "a cozy cabin in the woods" --width 768 --height 768 --steps 30

# By path (absolute or relative)
./cmfy run -w ./workflows/my_flow.json --prompt "a sketch of an owl"

# Async submit (returns immediately; prints prompt ID)
./cmfy run -w txt2img --prompt "a sketch of an owl" --async

# txt2music convenience variables
./cmfy run -w txt2music --tags "lofi chill, warm keys" --lyrics "Late night city lights"
```

Using standard and custom aliases:

```bash
# Assign (CLI writes config.toml [standard_workflows])
./cmfy workflows assign txt2img my_txt2img
./cmfy workflows assign ltx23 ~/cmfy/workflows/image_to_video_ltx2_3_i2v_with_sound.json

# Then run directly by alias
./cmfy txt2img --prompt "sunlit woodland" --steps 28 --cfg 5.5 --sampler euler
./cmfy ltx23 --image input.png --var PROMPT="cinematic close-up" --async
```

You can also define aliases manually in config:

```toml
[standard_workflows]
ltx23 = "~/cmfy/workflows/image_to_video_ltx2_3_i2v_with_sound.json"
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

- `[standard_workflows_params.<alias>]` mappings write flags such as `steps`, `cfg`, and `sampler_name` to exact paths.
- Without a mapping, cmfy applies a parameter only when exactly one workflow node exposes that input.
- Zero matches or multiple matches fail loudly. cmfy never guesses between nodes; use `workflows inspect` and add an exact mapping.

Outputs:

- Saved beneath `output_dir` (default `./outputs`); `--output`/`-o` or `--output-dir` overrides it per run.
- Server filenames and subfolders are validated as relative paths. Absolute paths, traversal, separator tricks, symlink destinations, oversized files, excessive output counts, and aggregate-size overflows fail closed.
- Downloads stream to same-directory partial files, resume through validated range responses, compute SHA-256, and publish complete files atomically without overwriting unrelated content.
- Duplicate content reuses its existing path. Different content receives the deterministic suffix `-<sha256-prefix>`.


## Template Variables

- Any string input in the workflow that contains `${KEY}` will be replaced with the value of `KEY`.
- Sources (precedence):
  1) Global `[vars]` in config
  2) `[workflows.<name>.vars]` in config
  3) Convenience flags (`--prompt`, `--tags`, `--lyrics`, `--seed`, `--width`, `--height`, `--steps`, `--cfg`)
  4) `--var KEY=VAL` on CLI
  5) Uploaded assets populate `${IMAGE}`, `${MASK}`, `${INPUT}` and enumerated variants
- Numeric inputs generally require `--set` unless you model them as strings in the workflow and template them.

Optional prompting guidelines in workflow JSON:

```json
{
  "prompt_guidelines": {
    "summary": "Write cinematic, high-contrast prompts.",
    "dos": ["Use strong nouns", "Include mood and pacing"],
    "donts": ["Avoid contradictory styles"],
    "examples": ["epic trailer score, dark tension, rising strings"]
  }
}
```

Show them with:

```bash
./cmfy workflows inspect txt2music --guidelines
```

## Batch Mode (JSONL)

Submit multiple jobs from a JSONL file. Each line can target a different workflow.

```bash
# Print example JSONL to stdout
./cmfy batch run example --mode mixed-workflows

# Run batch file
./cmfy batch run --file jobs.jsonl --async

# Bound local orchestration concurrency and optionally throttle starts
./cmfy batch run --file jobs.jsonl --concurrency 4 --submit-delay 500ms

# Machine-readable result summary (one JSON array)
./cmfy --json batch run --file jobs.jsonl
```

JSONL line schema (core fields):

- `workflow` (required): alias, name, or workflow path
- `id` (optional): user tracking ID
- `vars` (optional): map for `${KEY}` substitutions
- `set` (optional): map for `<nodeID>.inputs.<name>=value`
- `image` / `mask` / `input` (optional): arrays of file paths
- `server` (optional): override server URL
- `async` (optional): per-line async override
- `timeout` (optional): per-line wait timeout (e.g. `30m`)

## Durable Jobs and Server Utilities

Job state defaults to `$CMFY_STATE_DIR/history.sqlite3`, then `$XDG_STATE_HOME/cmfy/history.sqlite3`, then `~/.local/state/cmfy/history.sqlite3`. `--state-dir` has highest precedence. Oqto should bind a workspace-specific directory rather than the user's global state. The database uses WAL and mode `0600`; credentials are never persisted.

```bash
./cmfy --json jobs list --limit 50 --status completed
./cmfy --json jobs show <job-or-prompt-id>
./cmfy jobs watch --jsonl <job-or-prompt-id>  # WebSocket, polling fallback
./cmfy --json jobs retry <job-or-prompt-id> --request-id retry-001
./cmfy jobs prune --older-than 720h --keep-recent 1000 --dry-run

./cmfy server ping
./cmfy --json server inspect     # nodes, models, digests, supported features
./cmfy --profile local_gpu server ping
./cmfy queue
./cmfy job status <id>           # compatibility surface; updates durable status
./cmfy job wait <id> --download
./cmfy job cancel <id>           # records the cancellation outcome
```

`jobs list` is newest-first. Its opaque `next_cursor` can be supplied as `--cursor`; page size is bounded to 200.

## Machine Contract

Global `--json` emits exactly one versioned JSON value on stdout for automation-relevant commands. Failures emit one `cmfy/error-v1` value and return non-zero. Human output is suppressed in machine mode. `jobs watch --jsonl` is the streaming exception: it emits one bounded `cmfy/job-event-v1` object per line. `--quiet` suppresses human progress without changing results.

Schemas are versioned by their `schema` field. Additive fields may appear within a schema version; removing or changing field meaning requires a new version. Consumers should ignore unknown fields and must not parse human prose.


## API Endpoints Used

- `POST /upload` — uploads files (form-data) as type `input`.
- `POST /prompt` — submits a prompt graph, returns `prompt_id`.
- `GET  /history/<prompt_id>` — tracks prompt execution and outputs.
- `GET  /queue` — queue state for running/pending prompts.
- `POST /queue` — queue manipulation (used for cancel attempts).
- `GET  /view?filename=...&subfolder=...&type=...` — downloads generated assets.
- `GET  /system_stats` — used for `server ping`.
- `GET  /object_info` — server node dependency and capability inspection.
- `GET  /models[/<folder>]` — bounded model inventory when supported.
- `GET  /ws?clientId=...` — execution/progress/preview events with polling fallback.


## Tips & Troubleshooting

- If `cmfy run` reports “no workflow specified”, set `default_workflow` in config or pass `-w`.
- If an alias is not set, cmfy will still run it implicitly if `<workflows_dir>/<alias>.json` exists.
- Use `workflows inspect` to discover inputs; then use `--set` or add mappings under `[standard_workflows_params.<alias>]` for stable behavior.
- The bundled TOML parser supports keys/strings/ints/floats/bools/arrays and simple sections. If you need advanced TOML features, open an issue to switch to a full TOML library.


## Contributing

Issues and PRs are welcome. Please keep changes focused and consistent with the existing style. If you add new flags or config keys, update this README accordingly.

