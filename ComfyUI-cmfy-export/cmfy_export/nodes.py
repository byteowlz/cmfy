"""
cmfy Export Nodes for ComfyUI

Provides nodes for exporting workflows to cmfy format with wildcard support.
"""

import json
import os
from typing import Any, Dict, List, Optional


# Wildcard detection rules.
# Each rule: (field, var_name, class_regex, enabled_by_default)
# var_name=None means derive from node title.
# class_regex is matched against the node's class_type.
import re

WILDCARD_RULES = [
    # Prompts / text - match any node with direct text values
    ("text",       "PROMPT",   r".*",                       True),
    ("prompt",     "PROMPT",   r".*",                       True),
    ("system",     "SYSTEM",   r"Ollama|LLM",              False),
    ("string_a",   None,       r"Concat|String",            False),
    ("string_b",   None,       r"Concat|String",            False),
    # Seeds
    ("seed",       "SEED",     r".*",                       True),
    ("noise_seed", "SEED",     r".*",                       True),
    # Dimensions
    ("width",      "WIDTH",    r".*",                       True),
    ("height",     "HEIGHT",   r".*",                       True),
    # Sampling
    ("steps",        "STEPS",     r".*",                    False),
    ("cfg",          "CFG",       r".*",                    False),
    ("denoise",      "DENOISE",   r".*",                    False),
    ("sampler_name", "SAMPLER",   r"Sampler|KSampler",      False),
    ("scheduler",    "SCHEDULER", r"Scheduler|KSampler",    False),
    ("guidance",     "GUIDANCE",  r".*",                    False),
    ("batch_size",   "BATCH",     r".*",                    False),
    # Images
    ("image",           "IMAGE",  r"LoadImage",             True),
    ("filename_prefix", "OUTPUT", r"Save",                  True),
    # Models / LoRA
    ("ckpt_name",      "CKPT",          r"Checkpoint",      False),
    ("unet_name",      "UNET",          r"UNET|Diffusion",  False),
    ("vae_name",       "VAE",           r"VAE",              False),
    ("lora_name",      "LORA",          r"Lora",             False),
    ("strength_model", "LORA_STRENGTH", r"Lora",             False),
    ("strength_clip",  "CLIP_STRENGTH", r"Lora",             False),
    ("clip_name",      "CLIP",          r"CLIP|Clip",        False),
    ("clip_name1",     "CLIP1",         r"CLIP|Clip",        False),
    ("clip_name2",     "CLIP2",         r"CLIP|Clip",        False),
    # Literals / primitives (varName=None derives from node title)
    ("value",  None, r"Primitive|Literal|String", True),
    ("int",    None, r"Literal|Primitive|Int",     True),
    ("float",  None, r"Literal|Primitive|Float",   False),
    ("string", None, r"Primitive|String",          True),
]


def detect_wildcard_fields(
    workflow: Dict[str, Any], custom_fields: Optional[Dict[str, List[str]]] = None
) -> Dict[str, Dict[str, Any]]:
    """
    Analyze a workflow and detect fields that should become wildcards.
    Uses regex matching on class_type so new node variants are picked up
    automatically (e.g. EmptySD3LatentImage, EmptyHunyuanLatentVideo, ...).
    """
    wildcards: Dict[str, Dict[str, Any]] = {}
    used_vars: set = set()
    used_node_fields: set = set()

    for field, var_name, class_re, enabled in WILDCARD_RULES:
        pattern = re.compile(class_re, re.IGNORECASE)

        for node_id, node_data in workflow.items():
            if not isinstance(node_data, dict):
                continue

            class_type = node_data.get("class_type", "")
            inputs = node_data.get("inputs", {})
            title = (node_data.get("_meta") or {}).get("title", class_type)
            field_value = inputs.get(field)

            if field_value is None or isinstance(field_value, list):
                continue
            # Skip values that are already wildcard placeholders
            if isinstance(field_value, str) and re.match(r'^\$\{.+\}$', field_value):
                continue
            if not pattern.search(class_type):
                continue

            # Skip if this node+field was already claimed
            nf_key = f"{node_id}:{field}"
            if nf_key in used_node_fields:
                continue

            # Determine variable name
            vn = var_name
            if vn is None:
                vn = re.sub(r"[^A-Z0-9]+", "_", title.upper()).strip("_")

            if vn in used_vars:
                continue
            used_vars.add(vn)
            used_node_fields.add(nf_key)

            key = f"${{{vn}}}"
            wildcards[key] = {
                "value": field_value,
                "field": field,
                "class_type": class_type,
                "node_id": str(node_id),
                "enabled": enabled,
            }

    return wildcards


def build_variables_block(
    wildcards: Dict[str, Dict[str, Any]],
) -> Dict[str, Dict[str, Any]]:
    """
    Build a 'variables' block from detected wildcards with default values
    taken from the current workflow values.
    """
    variables = {}
    for key, info in wildcards.items():
        # Strip ${...} wrapper if present
        var_name = key.removeprefix("${").removesuffix("}")
        if not info.get("enabled", False):
            continue
        variables[var_name] = {
            "default": str(info["value"]),
            "description": f"{info.get('field', '')} on {info.get('class_type', 'unknown')}",
        }
    return variables


def convert_to_cmfy_format(
    workflow: Dict[str, Any],
    wildcards: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """
    Convert a ComfyUI workflow to cmfy format with wildcards.
    Includes a 'variables' block with defaults from current workflow values.
    """
    cmfy_workflow = {}

    if wildcards is None:
        wildcards = detect_wildcard_fields(workflow)

    for node_id, node_data in workflow.items():
        if not isinstance(node_data, dict):
            continue

        class_type = node_data.get("class_type", "")
        inputs = node_data.get("inputs", {})
        meta = node_data.get("_meta", {})
        new_inputs = {}

        for field_name, field_value in inputs.items():
            wildcard_key = f"${field_name.upper()}"
            is_wildcard = wildcard_key in wildcards

            if is_wildcard:
                new_inputs[field_name] = wildcard_key
            elif isinstance(field_value, list):
                new_inputs[field_name] = field_value
            else:
                new_inputs[field_name] = field_value

        cmfy_workflow[str(node_id)] = {
            "inputs": new_inputs,
            "class_type": class_type,
            "_meta": meta,
        }

    return cmfy_workflow


class CmifyExport:
    """
    Main export node for ComfyUI.
    """

    @classmethod
    def INPUT_TYPES(cls):
        return {
            "required": {
                "workflow_json": ("STRING", {"multiline": True}),
                "filename": ("STRING", {"default": "exported_workflow.json"}),
            },
            "optional": {
                "auto_detect_wildcards": ("BOOLEAN", {"default": True}),
            },
        }

    RETURN_TYPES = ("STRING",)
    RETURN_NAMES = ("status",)
    FUNCTION = "export_workflow_node"
    CATEGORY = "cmfy"
    DESCRIPTION = "Export workflow to cmfy format with wildcard support"

    def export_workflow_node(
        self,
        workflow_json: str,
        filename: str,
        auto_detect_wildcards: bool = True,
    ) -> tuple:
        try:
            workflow = json.loads(workflow_json)
            wildcards = detect_wildcard_fields(workflow) if auto_detect_wildcards else None
            cmfy_workflow = convert_to_cmfy_format(workflow, wildcards)

            # Build variables block with defaults from current values
            if wildcards:
                variables = build_variables_block(wildcards)
                if variables:
                    cmfy_workflow["variables"] = variables

            output_dir = os.path.expanduser("~/cmfy/workflows")
            os.makedirs(output_dir, exist_ok=True)

            output_path = os.path.join(output_dir, filename)
            with open(output_path, "w") as f:
                json.dump(cmfy_workflow, f, indent=2)

            return (f"Exported to {output_path}",)
        except Exception as e:
            return (f"Error: {str(e)}",)


class CmifyWildcardPreview:
    """
    Preview which fields will become wildcards.
    """

    @classmethod
    def INPUT_TYPES(cls):
        return {
            "required": {
                "workflow_json": ("STRING", {"multiline": True}),
            },
        }

    RETURN_TYPES = ("STRING",)
    RETURN_NAMES = ("wildcards_json",)
    FUNCTION = "preview_wildcards"
    CATEGORY = "cmfy"
    DESCRIPTION = "Preview wildcard detection results"

    def preview_wildcards(self, workflow_json: str) -> tuple:
        try:
            workflow = json.loads(workflow_json)
            wildcards = detect_wildcard_fields(workflow)
            return (json.dumps(wildcards, indent=2),)
        except Exception as e:
            return (f"Error: {str(e)}",)


NODE_CLASS_MAPPINGS = {
    "CmifyExport": CmifyExport,
    "CmifyWildcardPreview": CmifyWildcardPreview,
}

NODE_DISPLAY_NAME_MAPPINGS = {
    "CmifyExport": "Cmify Export",
    "CmifyWildcardPreview": "Cmfy Wildcard Preview",
}
