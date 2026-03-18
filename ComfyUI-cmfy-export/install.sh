#!/usr/bin/env bash
# cmfy Export Plugin Installer
# Run this script to install the cmfy export plugin to your ComfyUI instance

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_DIR="$SCRIPT_DIR"

echo "=========================================="
echo "cmfy Export Plugin Installer"
echo "=========================================="

# Detect ComfyUI installation
detect_comfyui() {
    local locations=(
        "$HOME/ComfyUI"
        "/workspace/ComfyUI"
        "$HOME/Development/ComfyUI"
        "/opt/ComfyUI"
    )

    for loc in "${locations[@]}"; do
        if [ -d "$loc" ]; then
            echo "$loc"
            return 0
        fi
    done
    return 1
}

# Find custom_nodes directory
find_custom_nodes() {
    local comfyui_path="$1"
    local custom_nodes="$comfyui_path/custom_nodes"
    
    if [ -d "$custom_nodes" ]; then
        echo "$custom_nodes"
        return 0
    fi
    return 1
}

# Main installation logic
install_plugin() {
    local custom_nodes="$1"
    local link_name="$custom_nodes/cmfy_export"
    
    # Check if already installed
    if [ -e "$link_name" ]; then
        echo -e "${YELLOW}Plugin already exists at $link_name${NC}"
        read -p "Replace existing installation? (y/N) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            echo "Installation cancelled."
            exit 0
        fi
        
        # Remove existing
        rm -rf "$link_name"
    fi
    
    # Create symlink
    echo "Creating symlink..."
    ln -s "$PLUGIN_DIR" "$link_name"
    
    echo -e "${GREEN}Plugin installed successfully!${NC}"
    echo ""
    echo "To use the plugin:"
    echo "  1. Restart ComfyUI"
    echo "  2. Look for 'Export to cmfy' button in the menu"
    echo ""
    echo "To uninstall:"
    echo "  rm $link_name"
}

# Main
echo ""

# Try to detect ComfyUI
COMFYUI_PATH=$(detect_comfyui)

if [ -z "$COMFYUI_PATH" ]; then
    echo "ComfyUI not found in standard locations."
    read -p "Enter the full path to your ComfyUI installation: " COMFYUI_PATH
fi

# Verify the path
if [ ! -d "$COMFYUI_PATH" ]; then
    echo -e "${RED}Error: Directory does not exist: $COMFYUI_PATH${NC}"
    exit 1
fi

# Find custom_nodes
CUSTOM_NODES=$(find_custom_nodes "$COMFYUI_PATH")

if [ -z "$CUSTOM_NODES" ]; then
    echo -e "${RED}Error: Could not find custom_nodes directory in $COMFYUI_PATH${NC}"
    exit 1
fi

echo "Found ComfyUI at: $COMFYUI_PATH"
echo "Custom nodes directory: $CUSTOM_NODES"
echo ""

# Confirm installation
read -p "Install cmfy export plugin? (Y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Nn]$ ]]; then
    echo "Installation cancelled."
    exit 0
fi

install_plugin "$CUSTOM_NODES"