@echo off
REM acemcp-go Windows 安装器
REM 用法: powershell -c "iwr -useb https://raw.githubusercontent.com/yourorg/acemcp-go/main/install.ps1 | iex"

setlocal enabledelayedexpansion

echo 🚀 acemcp-go 快速安装器
echo.

REM 检测 PowerShell 版本
powershell -Command "if ($PSVersionTable.PSVersion.Major -lt 5) { exit 1 }"
if errorlevel 1 (
    echo 错误: 需要 PowerShell 5.0 或更高版本
    exit /b 1
)

REM 运行 PowerShell 安装脚本
powershell -ExecutionPolicy Bypass -Command "& { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; iwr -useb https://raw.githubusercontent.com/yourorg/acemcp-go/main/install.ps1 | iex }"

if errorlevel 1 (
    echo 安装失败
    exit /b 1
)

echo.
echo ✅ 安装完成！
echo.
echo 下一步:
echo 1. 编辑配置文件: %USERPROFILE%\.acemcp\settings.toml
echo 2. 重新启动命令提示符或 PowerShell
echo 3. 在 Cursor 中配置 MCP 服务器，使用命令: acemcp
echo.
echo Cursor MCP 配置:
echo {
echo   "mcpServers": {
echo     "acemcp": {
echo       "command": "acemcp"
echo     }
echo   }
echo }
pause
