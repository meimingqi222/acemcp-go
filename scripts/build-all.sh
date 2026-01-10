#!/bin/bash

# 多平台构建脚本
# 支持构建不同平台的二进制文件

set -e

echo "开始多平台构建 acemcp-go 项目..."

# 创建 dist 目录
mkdir -p dist

# 清理旧的构建文件
echo "清理旧的构建文件..."
rm -f dist/*

# 定义平台和架构
PLATFORMS="linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64"

for PLATFORM in $PLATFORMS; do
    PLATFORM_SPLIT=(${PLATFORM//\// })
    GOOS=${PLATFORM_SPLIT[0]}
    GOARCH=${PLATFORM_SPLIT[1]}
    
    echo "构建 $GOOS/$GOARCH..."
    
    # 设置输出文件名
    OUTPUT_DAEMON="dist/acemcp-go-daemon-$GOOS-$GOARCH"
    OUTPUT_MCP="dist/acemcp-go-mcp-$GOOS-$GOARCH"
    
    # Windows 平台添加 .exe 扩展名
    if [ $GOOS = "windows" ]; then
        OUTPUT_DAEMON="$OUTPUT_DAEMON.exe"
        OUTPUT_MCP="$OUTPUT_MCP.exe"
    fi
    
    # 构建
    GOOS=$GOOS GOARCH=$GOARCH go build -o $OUTPUT_DAEMON ./cmd/daemon
    GOOS=$GOOS GOARCH=$GOARCH go build -o $OUTPUT_MCP ./cmd/mcp
    
    echo "  ✓ $OUTPUT_DAEMON"
    echo "  ✓ $OUTPUT_MCP"
done

echo ""
echo "多平台构建完成！"
echo "构建结果位于 dist/ 目录："
ls -la dist/
