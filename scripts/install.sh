#!/usr/bin/env bash
set -euo pipefail

echo "cmfy install"
echo "1) default (go install ./cmd/cmfy)"
echo "2) local release binary (go build -o cmfy ./cmd/cmfy)"
read -r -p "Choose install mode [1/2] (default: 1): " choice
choice="${choice:-1}"

case "$choice" in
  1)
    go install ./cmd/cmfy
    echo "Installed via go install"
    ;;
  2)
    go build -trimpath -ldflags "-s -w" -o cmfy ./cmd/cmfy
    echo "Built local binary: ./cmfy"
    ;;
  *)
    echo "Invalid choice: $choice" >&2
    exit 1
    ;;
esac
