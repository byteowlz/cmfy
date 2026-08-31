set shell := ["bash", "-cu"]

default:
    @just --list

help:
    @just --list

# === Install ===
install:
    ./scripts/install.sh

install-default:
    go install ./cmd/cmfy

install-all:
    go install ./cmd/cmfy

# === Build ===
build:
    go build -o cmfy ./cmd/cmfy

build-release:
    go build -trimpath -ldflags "-s -w" -o cmfy ./cmd/cmfy

# === Test ===
test:
    go test ./...

test-all:
    go test ./...

# === Quality ===
fmt:
    gofmt -w ./cmd ./internal

clippy:
    go vet ./...

lint: clippy

fix:
    just fmt

check:
    go test -run '^$' ./...

# === Maintenance ===
clean:
    go clean -cache -testcache
    rm -f ./cmfy

update:
    go get -u ./...
    go mod tidy

docs:
    go doc ./cmd/cmfy

# Publish cmfy's agent skill to the canonical byteowlz skills repository.
sync-skills:
    #!/usr/bin/env bash
    set -euo pipefail
    target="${SKILLISSUES:-$HOME/byteowlz/skillissues}"
    test -d "$target/skills" || { echo "skillissues repo not found: $target" >&2; exit 1; }
    rm -rf "$target/skills/cmfy-cli"
    cp -a skills/cmfy-cli "$target/skills/"
    just --justfile "$target/Justfile" update-readme
    git -C "$target" add skills/cmfy-cli README.md
    if [[ -n "$(git -C "$target" status --porcelain -- skills/cmfy-cli README.md)" ]]; then
        git -C "$target" commit -m "skills/cmfy-cli: sync from cmfy" -- skills/cmfy-cli README.md
    else
        echo "cmfy-cli is already up to date"
    fi
