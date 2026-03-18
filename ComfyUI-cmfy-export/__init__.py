"""
cmfy Export Plugin for ComfyUI

A plugin that exports ComfyUI workflows to cmfy-compatible JSON format
with automatic wildcard detection and customization.
"""

from .cmfy_export.nodes import NODE_CLASS_MAPPINGS, NODE_DISPLAY_NAME_MAPPINGS

__all__ = ["NODE_CLASS_MAPPINGS", "NODE_DISPLAY_NAME_MAPPINGS"]

WEB_DIRECTORY = "./cmfy_export/web"
__version__ = "1.0.0"
