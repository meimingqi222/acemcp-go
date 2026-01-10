# acemcp-go PowerShell 安装器
# 用法: powershell -c "iwr -useb https://raw.githubusercontent.com/yourorg/acemcp-go/main/install.ps1 | iex"

param(
    [string]$Version = "latest",
    [string]$InstallDir = "$env:USERPROFILE\.acemcp"
)

# 颜色输出
function Write-ColorOutput {
    param(
        [string]$Message,
        [string]$Color = "White"
    )
    
    $colors = @{
        "Red" = "Red"
        "Green" = "Green"
        "Yellow" = "Yellow"
        "Blue" = "Blue"
    }
    
    Write-Host $Message -ForegroundColor $colors[$Color]
}

# 检测平台
function Get-Platform {
    $arch = $env:PROCESSOR_ARCHITECTURE.ToLower()
    
    switch ($arch) {
        "amd64" { return "windows-amd64" }
        "arm64" { return "windows-arm64" }
        default {
            Write-ColorOutput "不支持的架构: $arch" "Red"
            exit 1
        }
    }
}

# 获取最新版本
function Get-LatestVersion {
    try {
        $response = Invoke-RestMethod -Uri "https://api.github.com/repos/meimingqi222/acemcp-go/releases/latest" -UseBasicParsing
        return $response.tag_name
    }
    catch {
        Write-ColorOutput "无法获取最新版本: $_" "Red"
        exit 1
    }
}

# 下载二进制文件
function Invoke-BinaryDownload {
    param(
        [string]$Version,
        [string]$Platform
    )
    
    $baseUrl = "https://github.com/meimingqi222/acemcp-go/releases/download/$Version"
    $binDir = Join-Path $InstallDir "bin"
    
    Write-ColorOutput "正在下载 acemcp-go $Version for $Platform..." "Green"
    
    # 创建目录
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    
    # 下载 daemon
    $daemonFile = "acemcp-go-daemon-$Platform.exe"
    $daemonPath = Join-Path $binDir "acemcp-go-daemon.exe"
    
    try {
        Invoke-WebRequest -Uri "$baseUrl/$daemonFile" -OutFile $daemonPath -UseBasicParsing
    }
    catch {
        Write-ColorOutput "下载 daemon 失败: $_" "Red"
        exit 1
    }
    
    # 下载 mcp
    $mcpFile = "acemcp-go-mcp-$Platform.exe"
    $mcpPath = Join-Path $binDir "acemcp-go-mcp.exe"
    
    try {
        Invoke-WebRequest -Uri "$baseUrl/$mcpFile" -OutFile $mcpPath -UseBasicParsing
    }
    catch {
        Write-ColorOutput "下载 MCP 服务器失败: $_" "Red"
        exit 1
    }
    
    Write-ColorOutput "下载完成" "Green"
}

# 创建配置文件
function New-ConfigFile {
    $configPath = Join-Path $InstallDir "settings.toml"
    
    if (-not (Test-Path $configPath)) {
        $configContent = @'
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
'@
        
        $configContent | Out-File -FilePath $configPath -Encoding UTF8
        Write-ColorOutput "配置文件已创建: $configPath" "Green"
        Write-ColorOutput "请编辑配置文件设置您的 BASE_URL 和 TOKEN" "Yellow"
    }
}

# 添加到 PATH
function Add-ToPath {
    $binDir = Join-Path $InstallDir "bin"
    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    
    if ($currentPath -notlike "*$binDir*") {
        $newPath = $currentPath + ";" + $binDir
        [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
        Write-ColorOutput "已将 $binDir 添加到用户 PATH" "Green"
        Write-ColorOutput "请重新启动命令提示符或 PowerShell" "Yellow"
    }
}

# 创建启动器
function New-Launcher {
    $binDir = Join-Path $InstallDir "bin"
    $launcherPath = Join-Path $binDir "acemcp.bat"
    
    $launcherContent = @"
@echo off
REM acemcp-go 启动器

cd /d "$binDir"

REM 检查守护进程是否运行
tasklist /FI "IMAGENAME eq acemcp-go-daemon.exe" 2>NUL | find /I /N "acemcp-go-daemon.exe">NUL
if errorlevel 1 (
    echo 启动 acemcp-go 守护进程...
    start /B acemcp-go-daemon.exe
    timeout /t 2 /nobreak >nul
)

REM 启动 MCP 服务器
acemcp-go-mcp.exe %*
"@
    
    $launcherContent | Out-File -FilePath $launcherPath -Encoding ASCII
    Write-ColorOutput "创建启动器: $launcherPath" "Green"
    
    # 创建 PowerShell 启动器
    $psLauncherPath = Join-Path $binDir "acemcp.ps1"
    $psLauncherContent = @"
# acemcp-go PowerShell 启动器
$binDir = "$binDir"

# 检查守护进程是否运行
\$daemon = Get-Process -Name "acemcp-go-daemon" -ErrorAction SilentlyContinue
if (-not \$daemon) {
    Write-Host "启动 acemcp-go 守护进程..."
    Start-Process -FilePath (Join-Path \$binDir "acemcp-go-daemon.exe") -WindowStyle Hidden
    Start-Sleep -Seconds 2
}

# 启动 MCP 服务器
& (Join-Path \$binDir "acemcp-go-mcp.exe") \$args
"@
    
    $psLauncherContent | Out-File -FilePath $psLauncherPath -Encoding UTF8
}

# 主函数
function Main {
    Write-ColorOutput "🚀 acemcp-go 快速安装器" "Green"
    Write-Host ""
    
    # 检测平台
    $platform = Get-Platform
    Write-ColorOutput "检测到平台: $platform" "Green"
    
    # 获取版本
    if ($Version -eq "latest") {
        $Version = Get-LatestVersion
    }
    Write-ColorOutput "版本: $Version" "Green"
    
    # 下载
    Invoke-BinaryDownload -Version $Version -Platform $platform
    
    # 创建配置
    New-ConfigFile
    
    # 添加到 PATH
    Add-ToPath
    
    # 创建启动器
    New-Launcher
    
    Write-Host ""
    Write-ColorOutput "✅ 安装完成！" "Green"
    Write-Host ""
    Write-ColorOutput "下一步:" "Yellow"
    Write-Host "1. 编辑配置文件: $InstallDir\settings.toml"
    Write-Host "2. 重新启动命令提示符或 PowerShell"
    Write-Host "3. 在 Cursor 中配置 MCP 服务器，使用命令: acemcp"
    Write-Host ""
    Write-ColorOutput "Cursor MCP 配置:" "Yellow"
    Write-Host "{"
    Write-Host "  `"mcpServers`": {"
    Write-Host "    `"acemcp`": {"
    Write-Host "      `"command`": `"acemcp`""
    Write-Host "    }"
    Write-Host "  }"
    Write-Host "}"
}

# 运行主函数
Main
