# PowerShell 构建脚本
# 提供便捷的构建命令

param(
    [string]$Target = "build"
)

function Show-Help {
    Write-Host "可用的构建命令：" -ForegroundColor Green
    Write-Host "  .\scripts\build.ps1 build      - 构建当前平台"
    Write-Host "  .\scripts\build.ps1 clean     - 清理构建文件"
    Write-Host "  .\scripts\build.ps1 deps      - 安装依赖"
    Write-Host "  .\scripts\build.ps1 test      - 运行测试"
    Write-Host "  .\scripts\build.ps1 all       - 构建所有平台"
    Write-Host "  .\scripts\build.ps1 windows   - 构建 Windows 版本"
    Write-Host "  .\scripts\build.ps1 linux     - 构建 Linux 版本"
}

function Build-Current {
    Write-Host "构建当前平台..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    go build -o dist/acemcp-go-daemon.exe ./cmd/daemon
    go build -o dist/acemcp-go-mcp.exe ./cmd/mcp
    Write-Host "构建完成！可执行文件位于 dist/ 目录" -ForegroundColor Green
}

function Build-Windows {
    Write-Host "构建 Windows 版本..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -o dist/acemcp-go-daemon.exe ./cmd/daemon
    go build -o dist/acemcp-go-mcp.exe ./cmd/mcp
    Write-Host "Windows 构建完成！" -ForegroundColor Green
}

function Build-Linux {
    Write-Host "构建 Linux 版本..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    go build -o dist/acemcp-go-daemon-linux-amd64 ./cmd/daemon
    go build -o dist/acemcp-go-mcp-linux-amd64 ./cmd/mcp
    Write-Host "Linux 构建完成！" -ForegroundColor Green
}

function Build-All {
    Write-Host "开始多平台构建..." -ForegroundColor Yellow
    if (Test-Path "scripts/build-all.sh") {
        bash scripts/build-all.sh
    } else {
        Write-Host "build-all.sh 脚本不存在" -ForegroundColor Red
    }
}

function Clean {
    Write-Host "清理构建文件..." -ForegroundColor Yellow
    if (Test-Path "dist") {
        Remove-Item -Recurse -Force dist
    }
    Write-Host "清理完成！" -ForegroundColor Green
}

function Install-Deps {
    Write-Host "安装依赖..." -ForegroundColor Yellow
    go mod tidy
    go mod download
    Write-Host "依赖安装完成！" -ForegroundColor Green
}

function Run-Tests {
    Write-Host "运行测试..." -ForegroundColor Yellow
    go test ./...
}

# 主逻辑
switch ($Target.ToLower()) {
    "build" { Build-Current }
    "windows" { Build-Windows }
    "linux" { Build-Linux }
    "all" { Build-All }
    "clean" { Clean }
    "deps" { Install-Deps }
    "test" { Run-Tests }
    "help" { Show-Help }
    default { 
        Write-Host "未知的目标: $Target" -ForegroundColor Red
        Show-Help
    }
}
