#!/bin/bash

# 构建脚本
# 将编译结果放到 dist 目录

set -e

echo "开始构建 acemcp-go 项目..."

# 创建 dist 目录
mkdir -p dist

echo "清理旧的构建文件..."
rm -f dist/acemcp-go-daemon
rm -f dist/acemcp-go-daemon.exe
rm -f dist/acemcp-go-mcp
rm -f dist/acemcp-go-mcp.exe

echo "构建 daemon..."
go build -o dist/acemcp-go-daemon ./cmd/daemon

echo "构建 mcp..."
go build -o dist/acemcp-go-mcp ./cmd/mcp

echo "构建完成！"
echo "构建结果位于 dist/ 目录："
ls -la dist/

echo ""
echo "可执行文件："
echo "- dist/acemcp-go-daemon (守护进程)"
echo "- dist/acemcp-go-mcp (MCP 服务器)"
