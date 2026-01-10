# acemcp-go

一个用 Go 语言实现的高性能 MCP (Model Context Protocol) 服务器，专为智能代码库检索和语义搜索而设计。

## 🚀 功能特性

- **智能代码索引**: 自动扫描和索引项目文件，支持增量更新
- **语义搜索**: 基于自然语言查询的代码上下文检索
- **文件监控**: 实时监控文件变化，自动更新索引
- **MCP 协议支持**: 完全兼容 MCP 2025-11-25 协议规范
- **多平台支持**: 支持 Windows、Linux、macOS 等多个平台
- **高性能**: 基于 Go 语言的并发处理能力
- **灵活配置**: 支持通过配置文件自定义行为

## 📁 项目结构

```
acemcp-go/
├── cmd/                    # 主程序入口
│   ├── daemon/            # 守护进程服务
│   └── mcp/               # MCP 服务器代理
├── internal/              # 内部包
│   ├── config/           # 配置管理
│   ├── indexer/          # 索引和搜索服务
│   ├── logging/          # 日志系统
│   ├── rpc/              # JSON-RPC 服务
│   └── state/            # 状态管理
├── scripts/              # 构建脚本
├── dist/                 # 构建输出目录
├── go.mod               # Go 模块定义
├── Makefile             # 构建工具
└── README.md            # 项目说明
```

## 🚀 快速接入 Cursor

### 一键安装 (推荐)

**Linux/macOS:**
```bash
curl -sSL https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.sh | bash
```

**Windows:**
```powershell
powershell -c "iwr -useb https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.ps1 | iex"
```

### 配置 Cursor

安装完成后，在 Cursor 中配置 MCP 服务器：

1. 打开 Cursor 设置
2. 找到 MCP 服务器配置
3. 添加以下配置：

```json
{
  "mcpServers": {
    "acemcp": {
      "command": "acemcp"
    }
  }
}
```

4. 重启 Cursor

### 开始使用

现在你可以在 Cursor 中使用自然语言搜索代码上下文了：

- "找到处理用户认证的代码"
- "显示文件上传功能的实现"
- "定位数据库连接相关的代码"

### 安装器特性

- **自动检测平台** - 支持 Linux、macOS、Windows
- **自动下载最新版本** - 从 GitHub Release 获取
- **自动配置环境** - 添加到 PATH，创建配置文件
- **智能启动** - 自动管理守护进程
- **一键更新** - 重新运行安装脚本即可更新

## 🛠️ 安装和构建

### 环境要求

- Go 1.22 或更高版本
- Git (用于版本控制)

### 构建方法

#### 方法 1: 一键快速安装 (推荐)

**Linux/macOS:**
```bash
curl -sSL https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.sh | bash
```

**Windows:**
```powershell
powershell -c "iwr -useb https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.ps1 | iex"
```

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

#### 方法 2: 直接下载预编译版本

访问 [Releases 页面](https://github.com/meimingqi222/acemcp-go/releases) 下载适合您平台的预编译二进制文件。

#### 方法 3: 使用 Makefile

```bash
# 克隆项目
git clone <repository-url>
cd acemcp-go

# 安装依赖
make deps

# 构建当前平台
make build

# 多平台构建
make build-all

# 清理构建文件
make clean
```

#### 方法 2: 使用构建脚本

**Windows:**
```bash
# 使用批处理脚本
.\scripts\build.bat

# 或使用 PowerShell
powershell -ExecutionPolicy Bypass -File .\scripts\build-simple.ps1 build
```

**Linux/macOS:**
```bash
chmod +x scripts/build.sh
./scripts/build.sh

# 多平台构建
chmod +x scripts/build-all.sh
./scripts/build-all.sh
```

#### 方法 3: 直接使用 Go

```bash
go mod tidy
go build -o dist/acemcp-go-daemon ./cmd/daemon
go build -o dist/acemcp-go-mcp ./cmd/mcp
```

## 🚀 快速开始

### 1. 配置

创建配置文件 `~/.acemcp/settings.toml`:

```toml
# 服务监听地址
LISTEN = "127.0.0.1:7033"
HTTP_ADDR = "127.0.0.1:7034"

# 日志级别 (debug|info|warn|error)
LOG_LEVEL = "info"

# API 配置
BASE_URL = "https://api.example.com"
TOKEN = "your-api-token"

# 索引配置
BATCH_SIZE = 10
MAX_LINES_PER_BLOB = 800

# 支持的文件扩展名
TEXT_EXTENSIONS = [".py", ".js", ".ts", ".go", ".rs", ".java", ".md", ".txt"]

# 排除的目录模式
EXCLUDE_PATTERNS = [".git", "node_modules", "vendor", ".venv", "venv", "__pycache__"]
```

### 2. 启动服务

```bash
# 启动守护进程
./dist/acemcp-go-daemon

# 或指定配置
./dist/acemcp-go-daemon --listen 127.0.0.1:7033 --http 127.0.0.1:7034 --log-level info
```

### 3. 使用 MCP 服务器

```bash
# 启动 MCP 代理 (会自动启动守护进程)
./dist/acemcp-go-mcp --daemon-addr 127.0.0.1:7033

# 在 MCP 客户端中配置使用 search_context 工具
```

## 📖 使用指南

### MCP 工具

acemcp-go 提供了一个主要的 MCP 工具:

#### `search_context`

根据自然语言查询在指定项目中搜索相关的代码上下文。

**参数:**
- `project_root_path` (string, 必需): 项目的绝对根目录路径
- `query` (string, 必需): 描述要查找代码的完整自然语言句子

**示例:**
```json
{
  "name": "search_context",
  "arguments": {
    "project_root_path": "/path/to/your/project",
    "query": "Find where the server handles chunk merging during file upload"
  }
}
```

### HTTP API

守护进程还提供 HTTP 管理接口:

- `GET /health` - 健康检查
- `GET /status` - 服务状态
- `GET /metrics` - 运行指标
- `POST /reload` - 重新加载配置

## ⚙️ 配置选项

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `LISTEN` | `127.0.0.1:7033` | JSON-RPC 服务监听地址 |
| `HTTP_ADDR` | `127.0.0.1:7034` | HTTP 管理服务地址 |
| `LOG_LEVEL` | `info` | 日志级别 |
| `BASE_URL` | `https://api.example.com` | API 基础 URL |
| `TOKEN` | `""` | API 认证令牌 |
| `BATCH_SIZE` | `10` | 批量上传大小 |
| `MAX_LINES_PER_BLOB` | `800` | 每个 blob 的最大行数 |
| `TEXT_EXTENSIONS` | 见默认配置 | 支持索引的文件扩展名 |
| `EXCLUDE_PATTERNS` | 见默认配置 | 排除的目录模式 |

## 🔧 开发

### 本地开发

```bash
# 克隆项目
git clone <repository-url>
cd acemcp-go

# 安装依赖
go mod tidy

# 运行测试
go test ./...

# 本地构建
go build -o dist/acemcp-go-daemon ./cmd/daemon
go build -o dist/acemcp-go-mcp ./cmd/mcp
```

### 项目架构

- **cmd/daemon**: 主守护进程，提供索引和搜索服务
- **cmd/mcp**: MCP 协议代理，处理 MCP 客户端请求
- **internal/indexer**: 核心索引和搜索逻辑
- **internal/rpc**: JSON-RPC 服务实现
- **internal/config**: 配置管理
- **internal/logging**: 结构化日志系统

## 📊 性能特性

- **并发处理**: 基于 Go 协程的高并发处理
- **增量索引**: 只处理变更的文件，避免重复索引
- **文件监控**: 使用 fsnotify 实时监控文件变化
- **批量上传**: 支持批量上传以提高效率
- **缓存机制**: 智能缓存减少重复计算

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

1. Fork 项目
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 打开 Pull Request

## 📄 许可证

本项目采用 MIT 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🔗 相关链接

- [MCP 协议规范](https://modelcontextprotocol.io/)
- [Go 语言官网](https://golang.org/)
- [项目问题反馈](https://github.com/yourorg/acemcp-go/issues)

## 🚀 CI/CD

### GitHub Actions

项目配置了自动化 CI/CD 流程：

- **自动构建**: 推送 tag 时自动构建多平台二进制文件
- **自动发布**: 创建 GitHub Release 并上传构建产物
- **手动触发**: 支持手动触发构建流程

### 支持的平台

- **Linux**: x86_64, ARM64
- **Windows**: x86_64  
- **macOS**: Intel, Apple Silicon

### 手动触发构建

在 GitHub Actions 页面可以手动触发构建：

1. 进入 Actions 页面
2. 选择 "Build and Release" 工作流
3. 点击 "Run workflow"
4. 输入版本号 (如 `v1.0.0`)
5. 等待构建完成

## 📝 更新日志

### v1.0.0
- 初始版本发布
- 实现基本的索引和搜索功能
- 支持 MCP 协议
- 多平台构建支持
- GitHub Actions 自动化发布
