@echo off
REM 构建脚本 - Windows版本
REM 将编译结果放到 dist 目录

echo 开始构建 acemcp-go 项目...

REM 创建 dist 目录
if not exist dist mkdir dist

echo 清理旧的构建文件...
if exist dist\acemcp-go-daemon.exe del dist\acemcp-go-daemon.exe
if exist dist\acemcp-go-mcp.exe del dist\acemcp-go-mcp.exe

echo 构建 daemon...
go build -o dist\acemcp-go-daemon.exe .\cmd\daemon

echo 构建 mcp...
go build -o dist\acemcp-go-mcp.exe .\cmd\mcp

echo 构建完成！
echo 构建结果位于 dist\ 目录：
dir dist\

echo.
echo 可执行文件：
echo - dist\acemcp-go-daemon.exe (守护进程)
echo - dist\acemcp-go-mcp.exe (MCP 服务器)
