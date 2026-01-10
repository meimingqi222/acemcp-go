#!/bin/bash

# acemcp-go installer - 快速安装和运行 MCP 服务器
# 用法: curl -sSL https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.sh | bash

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置
REPO="meimingqi222/acemcp-go"
INSTALL_DIR="$HOME/.acemcp"
BIN_DIR="$INSTALL_DIR/bin"
CONFIG_DIR="$HOME/.acemcp"

# 检测平台
detect_platform() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)
    
    case $arch in
        x86_64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) echo -e "${RED}不支持的架构: $arch${NC}"; exit 1 ;;
    esac
    
    case $os in
        linux|darwin) ;;
        *) echo -e "${RED}不支持的操作系统: $os${NC}"; exit 1 ;;
    esac
    
    echo "${os}-${arch}"
}

# 获取最新版本
get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4
    else
        echo -e "${RED}需要 curl 或 wget${NC}"
        exit 1
    fi
}

# 下载二进制文件
download_binary() {
    local version=$1
    local platform=$2
    local base_url="https://github.com/$REPO/releases/download/$version"
    
    echo -e "${GREEN}正在下载 acemcp-go $version for $platform...${NC}"
    
    # 创建目录
    mkdir -p "$BIN_DIR"
    mkdir -p "$CONFIG_DIR"
    
    # 下载 daemon
    local daemon_file="acemcp-go-daemon-${platform}"
    if [[ "$platform" == *"windows"* ]]; then
        daemon_file="${daemon_file}.exe"
    fi
    
    if command -v curl >/dev/null 2>&1; then
        curl -L "$base_url/$daemon_file" -o "$BIN_DIR/acemcp-go-daemon"
    else
        wget -O "$BIN_DIR/acemcp-go-daemon" "$base_url/$daemon_file"
    fi
    
    # 下载 mcp
    local mcp_file="acemcp-go-mcp-${platform}"
    if [[ "$platform" == *"windows"* ]]; then
        mcp_file="${mcp_file}.exe"
    fi
    
    if command -v curl >/dev/null 2>&1; then
        curl -L "$base_url/$mcp_file" -o "$BIN_DIR/acemcp-go-mcp"
    else
        wget -O "$BIN_DIR/acemcp-go-mcp" "$base_url/$mcp_file"
    fi
    
    # 设置执行权限
    chmod +x "$BIN_DIR/acemcp-go-daemon"
    chmod +x "$BIN_DIR/acemcp-go-mcp"
}

# 创建配置文件
create_config() {
    local config_file="$CONFIG_DIR/settings.toml"
    
    if [[ ! -f "$config_file" ]]; then
        cat > "$config_file" << 'EOF'
# acemcp-go 配置文件
LISTEN = "127.0.0.1:7033"
HTTP_ADDR = "127.0.0.1:7034"
LOG_LEVEL = "info"
BASE_URL = "https://api.example.com"
TOKEN = ""
BATCH_SIZE = 10
MAX_LINES_PER_BLOB = 800
TEXT_EXTENSIONS = [".py", ".js", ".ts", ".go", ".rs", ".java", ".md", ".txt"]
EXCLUDE_PATTERNS = [".git", "node_modules", "vendor", ".venv", "venv", "__pycache__"]
EOF
        echo -e "${GREEN}配置文件已创建: $config_file${NC}"
        echo -e "${YELLOW}请编辑配置文件设置您的 BASE_URL 和 TOKEN${NC}"
    fi
}

# 添加到 PATH
add_to_path() {
    local shell_rc=""
    
    case $SHELL in
        */bash) shell_rc="$HOME/.bashrc" ;;
        */zsh) shell_rc="$HOME/.zshrc" ;;
        */fish) shell_rc="$HOME/.config/fish/config.fish" ;;
        *) shell_rc="$HOME/.profile" ;;
    esac
    
    if ! grep -q "$BIN_DIR" "$shell_rc" 2>/dev/null; then
        echo "export PATH=\"$BIN_DIR:\$PATH\"" >> "$shell_rc"
        echo -e "${GREEN}已将 $BIN_DIR 添加到 PATH${NC}"
        echo -e "${YELLOW}请运行 'source $shell_rc' 或重新打开终端${NC}"
    fi
}

# 创建启动脚本
create_launcher() {
    local launcher="$BIN_DIR/acemcp"
    cat > "$launcher" << 'EOF'
#!/bin/bash

# acemcp-go 启动器
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 检查守护进程是否运行
if ! pgrep -f "acemcp-go-daemon" > /dev/null; then
    echo "启动 acemcp-go 守护进程..."
    "$SCRIPT_DIR/acemcp-go-daemon" &
    sleep 2
fi

# 启动 MCP 服务器
exec "$SCRIPT_DIR/acemcp-go-mcp" "$@"
EOF
    
    chmod +x "$launcher"
    echo -e "${GREEN}创建启动器: $BIN_DIR/acemcp${NC}"
}

# 主函数
main() {
    echo -e "${GREEN}🚀 acemcp-go 快速安装器${NC}"
    echo
    
    # 检测平台
    local platform=$(detect_platform)
    echo -e "${GREEN}检测到平台: $platform${NC}"
    
    # 获取版本
    local version=$(get_latest_version)
    if [[ -z "$version" ]]; then
        echo -e "${RED}无法获取最新版本${NC}"
        exit 1
    fi
    echo -e "${GREEN}最新版本: $version${NC}"
    
    # 下载
    download_binary "$version" "$platform"
    
    # 创建配置
    create_config
    
    # 添加到 PATH
    add_to_path
    
    # 创建启动器
    create_launcher
    
    echo
    echo -e "${GREEN}✅ 安装完成！${NC}"
    echo
    echo -e "${YELLOW}下一步:${NC}"
    echo "1. 编辑配置文件: $CONFIG_DIR/settings.toml"
    echo "2. 重新加载 shell: source $HOME/.bashrc (或对应的配置文件)"
    echo "3. 在 Cursor 中配置 MCP 服务器，使用命令: acemcp"
    echo
    echo -e "${YELLOW}Cursor MCP 配置:${NC}"
    echo '{'
    echo '  "mcpServers": {'
    echo '    "acemcp": {'
    echo '      "command": "acemcp"'
    echo '    }'
    echo '  }'
    echo '}'
}

# 运行主函数
main "$@"
