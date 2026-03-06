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
