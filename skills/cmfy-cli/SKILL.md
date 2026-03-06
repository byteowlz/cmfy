---
name: cmfy-cli
description: |
  Run and manage ComfyUI workflows via the cmfy command-line tool, including
  local workflows, SSH-imported workflows, queue inspection, and async job control.
  This skill should be used when users ask to run ComfyUI workflows, inspect
  workflow inputs, manage aliases, discover or import workflows from remote hosts,
  check queue state, or monitor and cancel jobs from the terminal.
---

# cmfy CLI

Fast CLI for ComfyUI workflow execution and operations.

Binary: `cmfy`

## Before Implementation

Gather context to ensure successful implementation:

- Codebase: existing workflow JSONs, node IDs, naming conventions
- Conversation: requested workflow, desired prompt and flags, sync vs async execution
- User guidelines: repo conventions, output paths, config precedence
- Runtime environment: reachable ComfyUI server, SSH connectivity for remote workflows

Only ask for missing user-specific requirements.

## Core Commands

```bash
# Connectivity
cmfy server ping

# Workflow management
cmfy workflows list
cmfy workflows inspect <name-or-path>
cmfy workflows show <name-or-path>
cmfy workflows aliases
cmfy workflows assign <alias> <workflow>
cmfy workflows add <source.json> [name]

# Remote workflow discovery/import via SSH-configured servers
cmfy workflows ssh-list <server> [pattern]
cmfy workflows ssh-list <server> [pattern] --json
cmfy workflows ssh-import <server> <remote-workflow> [local-name]
```

## Run Workflows

```bash
# Blocking execution (default)
cmfy run -w txt2img --prompt "a minimal product photo" --steps 30 --cfg 5.0

# Async execution (returns prompt ID immediately)
cmfy run -w txt2img --prompt "a minimal product photo" --async

# Wait timeout override for blocking mode
cmfy run -w txt2img --prompt "a minimal product photo" --timeout 45m

# Path-based run
cmfy run -w ./workflows/custom_flow.json --set 12.inputs.steps=24
```

### Common runtime flags

- `--workflow` or `-w`
- `--prompt`, `--seed`, `--width`, `--height`, `--steps`, `--cfg`
- `--sampler`, `--scheduler`, `--denoise`, `--strength`
- `--refiner-sampler`, `--refiner-scheduler`, `--refiner-steps`, `--refiner-cfg`
- `--set <nodeID>.inputs.<name>=<value>`
- `--var KEY=VAL`
- `--image`, `--mask`, `--input`
- `--async`, `--timeout`

## Queue and Jobs

```bash
# Queue state
cmfy queue
cmfy queue --json

# Prompt job lifecycle
cmfy job status <prompt_id>
cmfy job status <prompt_id> --json
cmfy job wait <prompt_id>
cmfy job wait <prompt_id> --timeout 30m
cmfy job cancel <prompt_id>
```

## Config Notes

Config file path:

- `$XDG_CONFIG_HOME/cmfy/config.toml`
- fallback: `~/.config/cmfy/config.toml`

Initialize config:

```bash
cmfy config init
cmfy config path
cmfy config print
```

Important config sections:

- `server_url`, `output_dir`, `workflows_dir`
- `[vars]`
- `[workflows.<name>.vars]`
- `[standard_workflows]`
- `[standard_workflows_params.<alias>]`
- `[remote_servers.<name>]` for SSH workflow discovery/import

Remote server example:

```toml
[remote_servers.local_gpu]
ssh_config_host = "local-gpu"
workflows_dir = "~/ComfyUI/user/default/workflows"
# or use host/user/port/key_path
```

## Agent Patterns

### Async submit + wait

```bash
ID=$(cmfy run -w txt2img --prompt "studio portrait" --async | rg 'Prompt ID:' | awk '{print $3}')
cmfy job wait "$ID" --timeout 30m
```

### Queue-aware execution

```bash
cmfy queue --json | jq '.running, .pending'
cmfy run -w txt2img --prompt "clean UI icon" --async
```

### Inspect before patching

```bash
cmfy workflows inspect txt2img
cmfy run -w txt2img --set 12.inputs.steps=20 --set 12.inputs.cfg=4.5
```

## Error Handling

- If no workflow is specified, pass `-w` or set `default_workflow`.
- If alias is unset, assign with `cmfy workflows assign <alias> <workflow>`.
- If SSH listing/import fails, verify `remote_servers.<name>` and SSH access first.
- For long runs, prefer `--async` plus `cmfy job wait`.
