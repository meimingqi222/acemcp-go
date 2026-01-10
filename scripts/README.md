# 构建脚本说明

本目录包含了 acemcp-go 项目的各种构建脚本，用于将编译结果输出到 `dist` 目录。

## 脚本列表

### 1. build.bat
Windows 批处理脚本，用于构建当前平台的二进制文件。

```bash
# 使用方法
.\scripts\build.bat
```

### 2. build.sh  
Linux/macOS Shell 脚本，用于构建当前平台的二进制文件。

```bash
# 使用方法
chmod +x scripts/build.sh
./scripts/build.sh
```

### 3. build-all.sh
多平台构建脚本，支持构建多个平台的二进制文件。

支持的平台：
- linux/amd64
- linux/arm64  
- windows/amd64
- darwin/amd64 (macOS Intel)
- darwin/arm64 (macOS Apple Silicon)

```bash
# 使用方法
chmod +x scripts/build-all.sh
./scripts/build-all.sh
```

### 4. build-simple.ps1
简化版 PowerShell 脚本，支持基本构建功能。

```powershell
# 构建当前平台
powershell -ExecutionPolicy Bypass -File .\scripts\build-simple.ps1 build

# 清理构建文件
powershell -ExecutionPolicy Bypass -File .\scripts\build-simple.ps1 clean
```

### 5. Makefile
提供了 make 命令支持（需要安装 make 工具）。

```bash
# 构建当前平台
make build

# 构建 Windows 版本
make build-windows

# 构建 Linux 版本  
make build-linux

# 多平台构建
make build-all

# 清理构建文件
make clean

# 安装依赖
make deps

# 运行测试
make test

# 显示帮助
make help
```

## 构建输出

所有构建脚本都会将编译结果输出到项目根目录的 `dist` 文件夹中：

- `acemcp-go-daemon[.exe]` - 守护进程程序
- `acemcp-go-mcp[.exe]` - MCP 服务器程序

多平台构建会生成带平台后缀的文件，例如：
- `acemcp-go-daemon-linux-amd64`
- `acemcp-go-mcp-windows-amd64.exe`

## 使用建议

1. **Windows 用户**：推荐使用 `build.bat` 或 `build-simple.ps1`
2. **Linux/macOS 用户**：推荐使用 `build.sh` 或 `build-all.sh`
3. **需要多平台构建**：使用 `build-all.sh`
4. **开发者**：可以使用 Makefile 获得更便捷的命令

## 注意事项

- 构建前请确保已安装 Go 1.22 或更高版本
- 首次构建前建议运行 `go mod tidy` 更新依赖
- 多平台构建需要相应的 Go 编译器支持
