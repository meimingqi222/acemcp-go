## acemcp-go v1.0.0

🎉 首次发布！这是一个用 Go 语言实现的高性能 MCP (Model Context Protocol) 服务器，专为智能代码库检索和语义搜索而设计。

### 🚀 主要功能

- **智能代码索引**: 自动扫描和索引项目文件，支持增量更新
- **语义搜索**: 基于自然语言查询的代码上下文检索  
- **文件监控**: 实时监控文件变化，自动更新索引
- **MCP 协议支持**: 完全兼容 MCP 2025-11-25 协议规范
- **多平台支持**: 支持 Windows、Linux、macOS 等多个平台
- **一键安装**: 类似 uvx 的快速安装体验

### 📦 快速开始

**Linux/macOS:**
```bash
curl -sSL https://raw.githubusercontent.com/meimingqi222/acemcp-go/main/install.sh | bash
```

**Windows:**
```powershell
powershell -c "iwr -useb https://raw.githubusercontent.com/meimingqi222/acemcp-go/main/install.ps1 | iex"
```

### 🔧 Cursor 配置

安装完成后，在 Cursor 中配置 MCP 服务器：

```json
{
  "mcpServers": {
    "acemcp": {
      "command": "acemcp"
    }
  }
}
```

### 📋 下载

选择适合您平台的二进制文件：

- **Linux**: `acemcp-go-daemon-linux-amd64`, `acemcp-go-mcp-linux-amd64`
- **Windows**: `acemcp-go-daemon-windows-amd64.exe`, `acemcp-go-mcp-windows-amd64.exe`
- **macOS**: `acemcp-go-daemon-darwin-amd64`, `acemcp-go-mcp-darwin-amd64`

### 🙏 致谢

感谢所有为这个项目做出贡献的开发者和用户！

---

🤖 此版本由 GitHub Actions 自动构建和发布。
