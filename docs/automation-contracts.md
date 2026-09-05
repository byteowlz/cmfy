# cmfy automation contracts

cmfy is a deterministic execution mechanism. It resolves declared workflow inputs; it does not rewrite prompts, rank workflows, score aesthetics, or invoke an embedded model.

## Execution lifecycle

All callers use the same library lifecycle:

1. **Resolve** loads the immutable workflow JSON, applies exact `--set` paths and declared parameter mappings, hashes inputs and the graph, and returns `cmfy/execution-plan-v1`.
2. **Submit** reserves an idempotent request in SQLite before contacting ComfyUI, reuses verified content-hash uploads, submits the resolved graph, and records the returned prompt ID.
3. **Observe** reconciles durable state with ComfyUI history and queue truth.
4. **Collect** validates output descriptors, streams bounded responses, resumes partial responses, hashes files, and atomically publishes collision-safe outputs.

`run`, workflow aliases, batch child jobs, `jobs retry`, `jobs watch`, and compatibility job commands all enter this substrate. Cobra handlers render typed results; they do not implement execution logic.

## Durable authority

State path precedence is:

1. `--state-dir` (stores `<dir>/history.sqlite3`)
2. `CMFY_STATE_DIR`
3. `$XDG_STATE_HOME/cmfy`
4. `~/.local/state/cmfy`

SQLite uses WAL and a `0600` database. Records include request and prompt IDs, stable server identity, workflow identity and SHA-256 digest, exact user prompt, explicit parameters, input paths/hashes/sizes, state transitions, output descriptors/paths/hashes/sizes, timestamps, error text, and revision.

The store does not contain credentials or URL userinfo. `jobs prune` deletes only old terminal history and stale upload-cache rows; it never deletes collected media.

Oqto must pass an exact workspace-scoped `--state-dir`, for example:

```text
<workspace>/oqto-apps-state/comfy-studio/cmfy
```

It must not mount a user's global cmfy state directory into an App operation.

## Framing

### One-result commands

Global `--json` writes exactly one JSON value to stdout. A failed command writes one `cmfy/error-v1` envelope and exits non-zero. Human prose is not mixed into machine stdout or stderr.

Representative schemas:

- `cmfy/execution-plan-v1`
- durable job record (`id`, `request_id`, `prompt_id`, `status`, provenance, outputs)
- `cmfy/workflow-description-v1`
- `cmfy/workflow-validation-v1`
- `cmfy/server-inspection-v1`
- `cmfy/queue-v1`
- `cmfy/cancel-v1`
- `cmfy/error-v1`

### Streaming commands

`jobs watch --jsonl` emits one bounded `cmfy/job-event-v1` object per line. Types include `running`, `executing`, `progress`, `preview`, `node_completed`, `cached`, `completed`, `failed`, and `cancelled`. WebSocket events are filtered by prompt ID. If the WebSocket handshake or stream fails before a terminal event, watch reconciles through bounded polling.

A preview event reports media type and byte count. `--include-preview` explicitly adds bounded base64 media to the JSONL event; without it, preview bytes are not written to logs. Collected files remain the durable media surface.

Schema changes are additive within a version. Removing a field or changing meaning requires a new schema version. Consumers must ignore unknown fields and must never scrape human output.

## Workflow preflight

- `workflows describe` exposes graph digest, variable locations/defaults, required assets, node classes, and connected output kinds.
- `workflows validate` rejects unresolved placeholders and disconnected/absent output contracts.
- `run --plan` additionally compares required node classes with bounded target-server `/object_info` data and reports missing dependencies.
- First-class parameters require an exact configured path or exactly one matching input. Ambiguity fails; there is no best-effort node selection.

## Limits and path guarantees

Positive config keys control transport and output bounds:

```toml
max_json_bytes = 8388608
max_upload_bytes = 1073741824
max_output_bytes = 536870912
max_total_output_bytes = 1073741824
max_output_files = 256
```

HTTP(S) base URLs must have no userinfo, query, fragment, or path. Redirects must remain same-origin. JSON, error bodies, uploads, event frames, individual outputs, aggregate outputs, and output counts are bounded.

Output filenames and subfolders must be safe relative paths. Absolute paths, traversal, separator variants, symlink destinations, and non-directory path components fail closed. Complete files are published without overwriting unrelated content. Equal content reuses the existing file; unequal collisions use a deterministic SHA-256 suffix.

## Reliability controls

- `--request-id` deduplicates concurrent and repeated submissions.
- `jobs retry` reconstructs an explicit request but generates a new request ID unless one is supplied.
- Upload-cache entries are keyed by server identity and SHA-256 and are reused only after probing the remote input.
- Partial downloads remain resumable; final paths never contain partial content.
- `batch run --concurrency 1..32` bounds local workers. `--submit-delay` bounds start rate. `--stop-on-error` stops new scheduling while allowing already-started operations to settle or cancel through context.
- `jobs list` is newest-first, filters exact status/server identity, limits pages to 200, and uses opaque cursors.

## Credential boundary

cmfy rejects endpoint URLs with embedded userinfo. Named profiles store only endpoint URLs. Credentials and authorization headers belong in an external credential-aware transport or host mediation layer and must never be returned in plans, events, history, logs, or App payloads.
