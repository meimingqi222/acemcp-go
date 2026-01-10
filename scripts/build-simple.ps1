param(
    [string]$Target = "build"
)

function Build-Current {
    Write-Host "构建当前平台..."
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    go build -o dist/acemcp-go-daemon.exe ./cmd/daemon
    go build -o dist/acemcp-go-mcp.exe ./cmd/mcp
    Write-Host "构建完成！"
}

function Clean {
    Write-Host "清理构建文件..."
    if (Test-Path "dist") {
        Remove-Item -Recurse -Force dist
    }
    Write-Host "清理完成！"
}

switch ($Target.ToLower()) {
    "build" { Build-Current }
    "clean" { Clean }
    default { 
        Write-Host "未知的目标: $Target"
        Write-Host "可用命令: build, clean"
    }
}
