"""
cmfy HTTP API handlers for workflow export.
"""

import json
import os
from aiohttp import web


async def handle_export(request):
    """
    Handle workflow export to cmfy format.
    """
    try:
        data = await request.json()
        workflow = data.get("workflow", {})
        filename = data.get("filename", "exported_workflow.json")

        if not filename.endswith(".json"):
            filename += ".json"

        output_dir = os.path.expanduser("~/cmfy/workflows")
        os.makedirs(output_dir, exist_ok=True)

        output_path = os.path.join(output_dir, filename)
        with open(output_path, "w") as f:
            json.dump(workflow, f, indent=2)

        return web.json_response({
            "success": True,
            "path": output_path,
            "filename": filename
        })
    except Exception as e:
        return web.json_response({
            "success": False,
            "error": str(e)
        }, status=500)


async def handle_list_workflows(request):
    """
    List all cmfy workflows.
    """
    try:
        workflows = []
        return web.json_response({"workflows": workflows})
    except Exception as e:
        return web.json_response({
            "workflows": [],
            "error": str(e)
        }, status=500)


def setup_routes(app):
    """Add cmfy routes to the app."""
    app.router.add_post("/cmfy/export", handle_export)
    app.router.add_get("/cmfy/workflows", handle_list_workflows)
