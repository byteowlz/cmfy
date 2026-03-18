/**
 * cmfy Export Plugin for ComfyUI
 *
 * Adds "Export to cmfy" to the File menu below "Export (API Format)",
 * allowing users to export the current workflow as cmfy-compatible
 * JSON with wildcard variables.
 */

import { app } from "../../scripts/app.js";
import { api } from "../../scripts/api.js";

// Wildcard detection rules.
const WILDCARD_RULES = [
    // Prompts / text
    { field: "text",     varName: "PROMPT",   match: /.*/,                       enabled: true  },
    { field: "prompt",   varName: "PROMPT",   match: /.*/,                       enabled: true  },
    { field: "system",   varName: "SYSTEM",   match: /Ollama|LLM/i,             enabled: false },
    { field: "string_a", varName: null,       match: /Concat|String/i,           enabled: false },
    { field: "string_b", varName: null,       match: /Concat|String/i,           enabled: false },

    // Seeds
    { field: "seed",       varName: "SEED",   match: /.*/,                       enabled: true  },
    { field: "noise_seed", varName: "SEED",   match: /.*/,                       enabled: true  },

    // Dimensions
    { field: "width",      varName: "WIDTH",  match: /.*/,                       enabled: true  },
    { field: "height",     varName: "HEIGHT", match: /.*/,                       enabled: true  },

    // Sampling parameters
    { field: "steps",        varName: "STEPS",     match: /.*/,                  enabled: false },
    { field: "cfg",          varName: "CFG",       match: /.*/,                  enabled: false },
    { field: "denoise",      varName: "DENOISE",   match: /.*/,                  enabled: false },
    { field: "sampler_name", varName: "SAMPLER",   match: /Sampler|KSampler/i,   enabled: false },
    { field: "scheduler",    varName: "SCHEDULER", match: /Scheduler|KSampler/i, enabled: false },
    { field: "guidance",     varName: "GUIDANCE",  match: /.*/,                  enabled: false },
    { field: "batch_size",   varName: "BATCH",     match: /.*/,                  enabled: false },

    // Images
    { field: "image",           varName: "IMAGE",  match: /LoadImage/,           enabled: true  },
    { field: "filename_prefix", varName: "OUTPUT", match: /Save/i,              enabled: true  },

    // Models / LoRA
    { field: "ckpt_name",      varName: "CKPT",          match: /Checkpoint/i,    enabled: false },
    { field: "unet_name",      varName: "UNET",          match: /UNET|Diffusion/i,enabled: false },
    { field: "vae_name",       varName: "VAE",           match: /VAE/i,           enabled: false },
    { field: "lora_name",      varName: "LORA",          match: /Lora/i,          enabled: false },
    { field: "strength_model", varName: "LORA_STRENGTH", match: /Lora/i,          enabled: false },
    { field: "strength_clip",  varName: "CLIP_STRENGTH", match: /Lora/i,          enabled: false },
    { field: "clip_name",      varName: "CLIP",          match: /CLIP|Clip/,      enabled: false },
    { field: "clip_name1",     varName: "CLIP1",         match: /CLIP|Clip/,      enabled: false },
    { field: "clip_name2",     varName: "CLIP2",         match: /CLIP|Clip/,      enabled: false },

    // Literals / primitives
    { field: "value",  varName: null, match: /Primitive|Literal|String/i, enabled: true  },
    { field: "int",    varName: null, match: /Literal|Primitive|Int/i,    enabled: true  },
    { field: "float",  varName: null, match: /Literal|Primitive|Float/i,  enabled: false },
    { field: "string", varName: null, match: /Primitive|String/i,         enabled: true  },
];

// Group fields into semantic types so that compatible fields can be
// swapped in the dropdown (e.g. "text" and "value" are both strings).
const FIELD_GROUPS = {
    string: ["text", "prompt", "value", "string", "string_a", "string_b", "system"],
    integer: ["seed", "noise_seed", "steps", "width", "height", "batch_size", "int", "cfg"],
    float: ["denoise", "guidance", "strength_model", "strength_clip", "float"],
    file: ["image", "ckpt_name", "unet_name", "vae_name", "lora_name",
           "clip_name", "clip_name1", "clip_name2", "filename_prefix"],
};

function fieldGroup(field) {
    for (const [group, fields] of Object.entries(FIELD_GROUPS)) {
        if (fields.includes(field)) return group;
    }
    return field; // fallback: exact field match
}

/**
 * Build candidates grouped by semantic type.
 * Returns { groupName: [{nodeId, field, title, classType, value}, ...] }
 */
function buildGroupedCandidates(workflow) {
    const groups = {};
    for (const [nodeId, node] of Object.entries(workflow)) {
        if (!node?.inputs) continue;
        const cls   = node.class_type || "";
        const title = node._meta?.title || cls;
        for (const [field, value] of Object.entries(node.inputs)) {
            if (Array.isArray(value)) continue;
            if (typeof value === "string" && /^\$\{.+\}$/.test(value)) continue;
            const group = fieldGroup(field);
            if (!groups[group]) groups[group] = [];
            groups[group].push({
                nodeId: String(nodeId), field, title, classType: cls, value
            });
        }
    }
    return groups;
}

function detectWildcards(workflow) {
    const wildcards = {};
    const usedVars = new Set();
    const usedNodeFields = new Set();

    for (const rule of WILDCARD_RULES) {
        for (const [nodeId, node] of Object.entries(workflow)) {
            if (!node?.inputs) continue;
            const cls   = node.class_type || "";
            const title = node._meta?.title || cls;
            const value = node.inputs[rule.field];

            if (value === undefined || Array.isArray(value)) continue;
            if (typeof value === "string" && /^\$\{.+\}$/.test(value)) continue;
            if (!rule.match.test(cls)) continue;

            const nfKey = nodeId + ":" + rule.field;
            if (usedNodeFields.has(nfKey)) continue;

            let varName = rule.varName;
            if (!varName) {
                varName = title
                    .toUpperCase()
                    .replace(/[^A-Z0-9]+/g, "_")
                    .replace(/^_|_$/g, "");
            }

            if (usedVars.has(varName)) continue;
            usedVars.add(varName);
            usedNodeFields.add(nfKey);

            wildcards[varName] = {
                value, field: rule.field, classType: cls,
                nodeId: String(nodeId), title, enabled: rule.enabled
            };
        }
    }
    return wildcards;
}

function applyWildcards(workflow, entries) {
    const replacements = {};
    for (const e of entries) {
        if (!e.enabled) continue;
        const key = e.nodeId + ":" + e.field;
        replacements[key] = "${" + e.varName + "}";
    }

    const out = {};
    for (const [nodeId, node] of Object.entries(workflow)) {
        if (!node?.inputs) continue;
        const inputs = {};
        for (const [field, value] of Object.entries(node.inputs)) {
            const key = String(nodeId) + ":" + field;
            inputs[field] = replacements[key] ?? value;
        }
        out[String(nodeId)] = { inputs, class_type: node.class_type, _meta: node._meta };
    }
    return out;
}

function downloadJson(obj, filename) {
    const blob = new Blob([JSON.stringify(obj, null, 2)], { type: "application/json" });
    const url  = URL.createObjectURL(blob);
    const a    = Object.assign(document.createElement("a"), { href: url, download: filename, style: "display:none" });
    document.body.appendChild(a);
    a.click();
    setTimeout(() => { a.remove(); URL.revokeObjectURL(url); }, 0);
}

function escapeHtml(s) {
    return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");
}

async function showExportDialog() {
    const p = await app.graphToPrompt();
    const workflow = p.output;
    const wildcards = detectWildcards(workflow);
    const grouped = buildGroupedCandidates(workflow);
    const entries = Object.entries(wildcards); // [ [varName, {field, nodeId, ...}], ... ]

    const overlay = document.createElement("div");
    overlay.innerHTML = `
    <style>
        .cmfy-overlay {
            position:fixed; inset:0; background:rgba(0,0,0,.65);
            display:flex; align-items:center; justify-content:center; z-index:99999;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        }
        .cmfy-dialog {
            background:#1e1e1e; border:1px solid #444; border-radius:8px;
            padding:24px; width:680px; max-height:80vh; overflow-y:auto; color:#ddd;
        }
        .cmfy-dialog h2  { margin:0 0 4px; font-size:18px; color:#fff; }
        .cmfy-dialog .sub { margin:0 0 16px; font-size:13px; color:#888; }
        .cmfy-wc-list {
            border:1px solid #333; border-radius:6px; max-height:400px;
            overflow-y:auto; margin-bottom:16px;
        }
        .cmfy-wc-row {
            display:flex; align-items:center; gap:8px;
            padding:7px 12px; border-bottom:1px solid #2a2a2a;
        }
        .cmfy-wc-row:last-child { border-bottom:none; }
        .cmfy-wc-row input[type="checkbox"] { flex-shrink:0; cursor:pointer; }
        .cmfy-wc-name {
            font-family:monospace; font-size:13px; width:130px; flex-shrink:0;
            padding:3px 6px; background:#262626; border:1px solid #444;
            border-radius:4px; color:#7dd3fc; box-sizing:border-box;
        }
        .cmfy-wc-name:focus { border-color:#3b82f6; outline:none; }
        .cmfy-wc-name:disabled { opacity:.4; border-color:#333; color:#666; }
        .cmfy-wc-arrow { color:#555; font-size:12px; flex-shrink:0; }
        .cmfy-wc-select {
            flex:1; min-width:0;
            padding:4px 6px; background:#262626; border:1px solid #444;
            border-radius:4px; color:#bbb; font-size:12px;
            cursor:pointer; box-sizing:border-box;
        }
        .cmfy-wc-select:focus { border-color:#3b82f6; outline:none; }
        .cmfy-wc-select:disabled { opacity:.4; cursor:default; }
        .cmfy-fname { width:100%; padding:8px 10px; background:#262626; border:1px solid #444; border-radius:6px; color:#ddd; font-size:14px; margin-bottom:16px; box-sizing:border-box; }
        .cmfy-btns { display:flex; gap:10px; justify-content:flex-end; }
        .cmfy-btns button { padding:8px 20px; border:none; border-radius:6px; cursor:pointer; font-size:14px; }
        .cmfy-btn-cancel { background:#333; color:#ccc; }
        .cmfy-btn-export { background:#3b82f6; color:#fff; font-weight:600; }
        .cmfy-btn-cancel:hover { background:#444; }
        .cmfy-btn-export:hover { background:#2563eb; }
        .cmfy-empty { padding:20px; text-align:center; color:#666; }
    </style>
    <div class="cmfy-overlay">
      <div class="cmfy-dialog">
        <h2>Export to cmfy</h2>
        <p class="sub">Assign \${WILDCARD} names and pick which node each one binds to:</p>
        ${entries.length === 0
            ? '<div class="cmfy-empty">No wildcard candidates detected in this workflow.</div>'
            : `<div class="cmfy-wc-list">${entries.map(([varName, w], i) => {
                // Build options: all nodes in the same field group
                const group = fieldGroup(w.field);
                const opts = (grouped[group] || []);
                const optionsHtml = opts.map(c => {
                    const sel = (c.nodeId === w.nodeId && c.field === w.field) ? "selected" : "";
                    const preview = String(c.value).substring(0, 45);
                    // Encode both nodeId and field in the value so we can extract them on export
                    const optVal = c.nodeId + "|" + c.field;
                    return `<option value="${escapeHtml(optVal)}" ${sel}>[${escapeHtml(c.nodeId)}] ${escapeHtml(c.title)} .${escapeHtml(c.field)} = ${escapeHtml(preview)}</option>`;
                }).join("");

                return `
                <div class="cmfy-wc-row">
                  <input type="checkbox" data-idx="${i}" ${w.enabled ? "checked" : ""}>
                  <input type="text" class="cmfy-wc-name" data-idx="${i}"
                         value="${escapeHtml(varName)}" ${w.enabled ? "" : "disabled"}>
                  <span class="cmfy-wc-arrow">&larr;</span>
                  <select class="cmfy-wc-select" data-idx="${i}"
                          ${w.enabled ? "" : "disabled"}>
                    ${optionsHtml}
                  </select>
                </div>`;
              }).join("")}</div>`
        }
        <input class="cmfy-fname" type="text" value="workflow.json" placeholder="filename.json">
        <div class="cmfy-btns">
          <button class="cmfy-btn-cancel">Cancel</button>
          <button class="cmfy-btn-export">Export</button>
        </div>
      </div>
    </div>`;

    document.body.appendChild(overlay);

    // Toggle checkbox enables/disables the name input and dropdown
    overlay.querySelectorAll('input[type="checkbox"][data-idx]').forEach(cb => {
        cb.addEventListener("change", () => {
            const idx = cb.dataset.idx;
            const nameInput = overlay.querySelector(`.cmfy-wc-name[data-idx="${idx}"]`);
            const selectEl  = overlay.querySelector(`.cmfy-wc-select[data-idx="${idx}"]`);
            nameInput.disabled = !cb.checked;
            selectEl.disabled  = !cb.checked;
        });
    });

    const close = () => overlay.remove();
    overlay.querySelector(".cmfy-overlay").addEventListener("click", e => { if (e.target === e.currentTarget) close(); });
    overlay.querySelector(".cmfy-btn-cancel").addEventListener("click", close);

    overlay.querySelector(".cmfy-btn-export").addEventListener("click", async () => {
        const finalEntries = entries.map(([varName, w], i) => {
            const cb     = overlay.querySelector(`input[type="checkbox"][data-idx="${i}"]`);
            const name   = overlay.querySelector(`.cmfy-wc-name[data-idx="${i}"]`);
            const select = overlay.querySelector(`.cmfy-wc-select[data-idx="${i}"]`);
            // Value is "nodeId|field"
            const [selectedNodeId, selectedField] = select.value.split("|");
            return {
                field:   selectedField || w.field,
                nodeId:  selectedNodeId || w.nodeId,
                varName: name.value.trim().toUpperCase().replace(/[^A-Z0-9_]/g, "_") || varName,
                enabled: cb.checked,
            };
        });

        const cmfyWf  = applyWildcards(workflow, finalEntries);
        let filename = overlay.querySelector(".cmfy-fname").value.trim() || "workflow.json";
        if (!filename.endsWith(".json")) filename += ".json";

        try {
            const resp = await api.fetchApi("/cmfy/export", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ workflow: cmfyWf, filename })
            });
            if (resp.ok) {
                const r = await resp.json();
                alert("Saved to: " + r.path);
            } else {
                downloadJson(cmfyWf, filename);
            }
        } catch {
            downloadJson(cmfyWf, filename);
        }
        close();
    });
}

// ─── Register extension ─────────────────────────────────────────────
app.registerExtension({
    name: "cmfy.Export",

    commands: [
        {
            id: "Cmfy.ExportWorkflow",
            label: "Export to cmfy",
            icon: "pi pi-download",
            function: showExportDialog,
        }
    ],

    menuCommands: [
        {
            path: ["File"],
            commands: ["Cmfy.ExportWorkflow"],
        }
    ],

    async setup() {
        console.log("[cmfy] Export plugin loaded");
    }
});
