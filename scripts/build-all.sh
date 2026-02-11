#!/bin/bash
# Build script for all platforms with version injection

set -e

# Get version from git tag, or use "dev" if not on a tag
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# ldflags for version injection
LDFLAGS="-s -w \
    -X github.com/meimingqi222/acemcp-go/internal/version.Version=${VERSION} \
    -X github.com/meimingqi222/acemcp-go/internal/version.GitCommit=${GIT_COMMIT} \
    -X github.com/meimingqi222/acemcp-go/internal/version.BuildTime=${BUILD_TIME}"

echo "Building acemcp-go..."
echo "Version: ${VERSION}"
echo "Commit: ${GIT_COMMIT}"
echo "Build Time: ${BUILD_TIME}"
echo ""

mkdir -p dist

# Build for current platform
echo "Building for current platform..."
go build -ldflags="${LDFLAGS}" -o dist/acemcp-go-daemon ./cmd/daemon
go build -ldflags="${LDFLAGS}" -o dist/acemcp-go-mcp ./cmd/mcp
echo "Done!"
