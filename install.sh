#!/bin/bash

# acemcp-go installer - quick install and run MCP server
# Usage: curl -sSL https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.sh | bash

set -e

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Config
REPO="meimingqi222/acemcp-go"
INSTALL_DIR="$HOME/.acemcp"
BIN_DIR="$INSTALL_DIR/bin"
CONFIG_DIR="$HOME/.acemcp"

# Detect platform
detect_platform() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)
    
    case $arch in
        x86_64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) echo -e "${RED}Unsupported architecture: $arch${NC}"; exit 1 ;;
    esac
    
    case $os in
        linux|darwin) ;;
        *) echo -e "${RED}Unsupported operating system: $os${NC}"; exit 1 ;;
    esac
    
    echo "${os}-${arch}"
}

# Get latest version
get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4
    else
        echo -e "${RED}curl or wget is required${NC}"
        exit 1
    fi
}

# Download binaries
download_binary() {
    local version=$1
    local platform=$2
    local base_url="https://github.com/$REPO/releases/download/$version"
    
    echo -e "${GREEN}Downloading acemcp-go $version for $platform...${NC}"
    
    # Prepare directories
    mkdir -p "$BIN_DIR"
    mkdir -p "$CONFIG_DIR"
    
    # Download daemon
    local daemon_file="acemcp-go-daemon-${platform}"
    if [[ "$platform" == *"windows"* ]]; then
        daemon_file="${daemon_file}.exe"
    fi
    
    if command -v curl >/dev/null 2>&1; then
        curl -L "$base_url/$daemon_file" -o "$BIN_DIR/acemcp-go-daemon"
    else
        wget -O "$BIN_DIR/acemcp-go-daemon" "$base_url/$daemon_file"
    fi
    
    # Download mcp server
    local mcp_file="acemcp-go-mcp-${platform}"
    if [[ "$platform" == *"windows"* ]]; then
        mcp_file="${mcp_file}.exe"
    fi
    
    if command -v curl >/dev/null 2>&1; then
        curl -L "$base_url/$mcp_file" -o "$BIN_DIR/acemcp-go-mcp"
    else
        wget -O "$BIN_DIR/acemcp-go-mcp" "$base_url/$mcp_file"
    fi
    
    # Make executable
    chmod +x "$BIN_DIR/acemcp-go-daemon"
    chmod +x "$BIN_DIR/acemcp-go-mcp"
}

# Create config file
create_config() {
    local config_file="$CONFIG_DIR/settings.toml"
    
    if [[ ! -f "$config_file" ]]; then
        cat > "$config_file" << 'EOF'
# acemcp-go configuration
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
        echo -e "${GREEN}Configuration file created: $config_file${NC}"
        echo -e "${YELLOW}Please edit BASE_URL and TOKEN before starting${NC}"
    fi
}

# Add to PATH
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
        echo -e "${GREEN}Added $BIN_DIR to PATH${NC}"
        echo -e "${YELLOW}Run 'source $shell_rc' or reopen your shell${NC}"
    fi
}

# Create launcher
create_launcher() {
    local launcher="$BIN_DIR/acemcp"
    cat > "$launcher" << 'EOF'
#!/bin/bash

# acemcp-go launcher
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Check daemon status
if ! pgrep -f "acemcp-go-daemon" > /dev/null; then
    echo "Starting acemcp-go daemon..."
    "$SCRIPT_DIR/acemcp-go-daemon" &
    sleep 2
fi

# Start MCP server
exec "$SCRIPT_DIR/acemcp-go-mcp" "$@"
EOF
    
    chmod +x "$launcher"
    echo -e "${GREEN}Launcher created: $BIN_DIR/acemcp${NC}"
}

# Main
main() {
    echo -e "${GREEN}[acemcp-go] quick installer${NC}"
    echo
    
    # Detect platform
    local platform=$(detect_platform)
    echo -e "${GREEN}Detected platform: $platform${NC}"
    
    # Get version
    local version=$(get_latest_version)
    if [[ -z "$version" ]]; then
        echo -e "${RED}Failed to get latest version${NC}"
        exit 1
    fi
    echo -e "${GREEN}Latest version: $version${NC}"
    
    # Download
    download_binary "$version" "$platform"
    
    # Create config
    create_config
    
    # Add to PATH
    add_to_path
    
    # Create launcher
    create_launcher
    
    echo
    echo -e "${GREEN}[Installation complete!]${NC}"
    echo
    echo -e "${YELLOW}Next steps:${NC}"
    echo "1. Edit configuration: $CONFIG_DIR/settings.toml"
    echo "2. Reload your shell: source $HOME/.bashrc (or equivalent)"
    echo "3. In Cursor, configure MCP server with command: acemcp"
    echo
    echo -e "${YELLOW}Cursor MCP configuration:${NC}"
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
