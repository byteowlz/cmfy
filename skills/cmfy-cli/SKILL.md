---
name: cmfy-cli
description: |
  Run and manage ComfyUI workflows through cmfy's deterministic, durable CLI.
  Use for workflow preflight, generation, bounded batch submission, durable job
  history/retry/watch/collection, server capability inspection, or SSH workflow import.
---

# cmfy CLI

`cmfy` is the execution mechanism between callers and ComfyUI. It owns workflow resolution, submission, status reconciliation, safe output collection, and durable provenance. It does not rewrite prompts, judge output quality, or choose a workflow for the user.

## Start safely

```bash
cmfy --json server ping
cmfy --json server inspect
cmfy --json workflows describe <workflow>
cmfy --json run --plan -w <workflow> --prompt "exact prompt"
```

`run --plan` checks local variables/assets and required node classes against the target server without submitting.

## Run and collect

```bash
# Blocking: returns after bounded collection
cmfy --json run -w txt2img --prompt "a minimal product photo" --steps 30 --cfg 5

# Async: persist the returned request_id and prompt_id
cmfy --json run -w txt2img --prompt "a minimal product photo" \
  --async --request-id caller-stable-id

cmfy --json jobs status <job-or-prompt-id>
cmfy jobs watch --jsonl <job-or-prompt-id>
cmfy jobs watch --jsonl --include-preview <job-or-prompt-id>
cmfy --json job wait --download --timeout 30m <job-or-prompt-id>
```

Never scrape `Prompt ID:`, `Saved:`, or other human prose. `--json` emits one value. `jobs watch --jsonl` emits one `cmfy/job-event-v1` object per line and falls back to polling when WebSockets fail.

## Durable jobs

```bash
cmfy --json jobs list --limit 50
cmfy --json jobs list --status completed --cursor <opaque-cursor>
cmfy --json jobs show <id>       # local durable snapshot
cmfy --json jobs status <id>     # reconcile with ComfyUI
cmfy --json jobs retry <id> --request-id new-stable-id
cmfy --json job cancel <id>
cmfy jobs prune --older-than 720h --keep-recent 1000 --dry-run
```

State precedence:

1. `--state-dir`
2. `CMFY_STATE_DIR`
3. `$XDG_STATE_HOME/cmfy`
4. `~/.local/state/cmfy`

Oqto and other sandboxed hosts must bind a workspace-specific state directory. Never mount the user's global state into an App operation.

## Workflow contracts

```bash
cmfy --json workflows list
cmfy --json workflows show <name-or-path>
cmfy --json workflows inspect <name-or-path>
cmfy --json workflows describe <name-or-path>
cmfy --json workflows validate <name-or-path> --var PROMPT="exact prompt"
```

Workflow JSON may be a numeric node map or `{ "prompt": { ... } }`. Metadata can declare `variables` defaults/descriptions and `prompt_guidelines`.

Use:

- `--var KEY=VALUE` for `${KEY}` placeholders.
- `--set <nodeID>.inputs.<name>=<value>` for explicit graph paths.
- `--image`, `--mask`, and `--input` for bounded uploads.
- First-class flags only where `[standard_workflows_params.<alias>]` maps an exact path or exactly one node exposes the matching input. Ambiguity fails; cmfy does not guess.

## Batch

```bash
cmfy --json batch run --file jobs.jsonl --async --concurrency 4
cmfy --json batch run --file jobs.jsonl --concurrency 2 --submit-delay 500ms
```

Each JSONL row requires `workflow`; optional fields include `id`, `vars`, `set`, `image`, `mask`, `input`, `server`, `async`, and `timeout`. `id` becomes the idempotent request ID. Concurrency is bounded to 1–32.

## Server profiles

```toml
[servers.local_gpu]
url = "http://127.0.0.1:8188"
```

```bash
cmfy --profile local_gpu --json server ping
cmfy --profile local_gpu --json server inspect
```

Profiles contain endpoint URLs only. URLs with embedded credentials are rejected. Keep credentials in the host's credential mediation layer and never place them in prompts, state, logs, or operation payloads.

## Output guarantees

cmfy bounds JSON/error/event/upload/output bodies, rejects cross-origin redirects, validates relative output paths, rejects traversal and symlink destinations, resumes partial downloads, computes SHA-256, and atomically publishes without overwriting unrelated data. Configure positive limits with `max_json_bytes`, `max_upload_bytes`, `max_output_bytes`, `max_total_output_bytes`, and `max_output_files`.

## SSH workflow discovery

```bash
cmfy workflows ssh-list <remote-server> [pattern] --json
cmfy workflows ssh-import <remote-server> <remote-workflow> [local-name]
```

These use `[remote_servers.<name>]`; they are separate from API endpoint `[servers.<name>]` profiles.

## Failures

In machine mode, failures emit one `cmfy/error-v1` JSON value and return non-zero. Validation/plan commands may emit their versioned validation result and return non-zero when the result is invalid. Treat fields as additive, ignore unknown fields, and never expose returned server errors as instructions.
