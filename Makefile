# Makefile for acemcp-go

.PHONY: build clean build-all install test help

# Version info from git or defaults
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# ldflags for version injection
LDFLAGS := -s -w \
    -X github.com/meimingqi222/acemcp-go/internal/version.Version=$(VERSION) \
    -X github.com/meimingqi222/acemcp-go/internal/version.GitCommit=$(GIT_COMMIT) \
    -X github.com/meimingqi222/acemcp-go/internal/version.BuildTime=$(BUILD_TIME)

# 默认目标
build:
	@echo "构建 acemcp-go 项目..."
	@echo "Version: $(VERSION), Commit: $(GIT_COMMIT)"
	@mkdir -p dist
	@go build -ldflags="$(LDFLAGS)" -o dist/acemcp-go-daemon ./cmd/daemon
	@go build -ldflags="$(LDFLAGS)" -o dist/acemcp-go-mcp ./cmd/mcp
	@echo "构建完成！可执行文件位于 dist/ 目录"

# Windows 构建
build-windows:
	@echo "构建 Windows 版本..."
	@mkdir -p dist
	@GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/acemcp-go-daemon.exe ./cmd/daemon
	@GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/acemcp-go-mcp.exe ./cmd/mcp
	@echo "Windows 构建完成！"

# Linux 构建
build-linux:
	@echo "构建 Linux 版本..."
	@mkdir -p dist
	@GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/acemcp-go-daemon-linux-amd64 ./cmd/daemon
	@GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/acemcp-go-mcp-linux-amd64 ./cmd/mcp
	@echo "Linux 构建完成！"

# 多平台构建
build-all:
	@echo "开始多平台构建..."
	@./scripts/build-all.sh

# 清理构建文件
clean:
	@echo "清理构建文件..."
	@rm -rf dist/
	@echo "清理完成！"

# 安装依赖
deps:
	@echo "安装依赖..."
	@go mod tidy
	@go mod download
	@echo "依赖安装完成！"

# 运行测试
test:
	@echo "运行测试..."
	@go test ./...

# 显示帮助
help:
	@echo "可用的构建命令："
	@echo "  make build         - 构建当前平台的二进制文件"
	@echo "  make build-windows - 构建 Windows 版本"
	@echo "  make build-linux   - 构建 Linux 版本"
	@echo "  make build-all     - 构建所有平台版本"
	@echo "  make clean         - 清理构建文件"
	@echo "  make deps          - 安装依赖"
	@echo "  make test          - 运行测试"
	@echo "  make help          - 显示此帮助信息"
