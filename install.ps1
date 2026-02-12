# acemcp-go PowerShell installer
# Usage: powershell -c "iwr -useb https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.ps1 | iex"

param(
    [string]$Version = "latest",
    [string]$InstallDir = "$env:USERPROFILE\.acemcp"
)

# Color output
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

# Detect platform
function Get-Platform {
    $arch = $env:PROCESSOR_ARCHITECTURE.ToLower()
    
    switch ($arch) {
        "amd64" { return "windows-amd64" }
        "arm64" { return "windows-arm64" }
        default {
            Write-ColorOutput "Unsupported architecture: $arch" "Red"
            exit 1
        }
    }
}

# Get latest version
function Get-LatestVersion {
    try {
        $response = Invoke-RestMethod -Uri "https://api.github.com/repos/meimingqi222/acemcp-go/releases/latest" -UseBasicParsing
        return $response.tag_name
    }
    catch {
        Write-ColorOutput "Unable to fetch latest version: $_" "Red"
        exit 1
    }
}

# Download binaries
function Invoke-BinaryDownload {
    param(
        [string]$Version,
        [string]$Platform
    )
    
    $baseUrl = "https://github.com/meimingqi222/acemcp-go/releases/download/$Version"
    $binDir = Join-Path $InstallDir "bin"
    $tempDir = Join-Path $env:TEMP "acemcp-update-$(Get-Random)"
    
    Write-ColorOutput "Downloading acemcp-go $Version for $Platform..." "Green"
    
    # Create directories
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
    
    # Download daemon to temp dir
    $daemonFile = "acemcp-go-daemon-$Platform.exe"
    $tempDaemonPath = Join-Path $tempDir "acemcp-go-daemon.exe"
    
    try {
        Invoke-WebRequest -Uri "$baseUrl/$daemonFile" -OutFile $tempDaemonPath -UseBasicParsing
    }
    catch {
        Write-ColorOutput "Failed to download daemon: $_" "Red"
        Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    }
    
    # Download mcp server to temp dir
    $mcpFile = "acemcp-go-mcp-$Platform.exe"
    $tempMcpPath = Join-Path $tempDir "acemcp-go-mcp.exe"
    
    try {
        Invoke-WebRequest -Uri "$baseUrl/$mcpFile" -OutFile $tempMcpPath -UseBasicParsing
    }
    catch {
        Write-ColorOutput "Failed to download MCP server: $_" "Red"
        Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
        exit 1
    }
    
    Write-ColorOutput "Download complete, installing..." "Green"
    
    # Wait for processes to exit and install
    $daemonPath = Join-Path $binDir "acemcp-go-daemon.exe"
    $mcpPath = Join-Path $binDir "acemcp-go-mcp.exe"
    
    # Wait and replace daemon
    $maxWait = 10
    while ($maxWait -gt 0) {
        try {
            Move-Item -Path $tempDaemonPath -Destination $daemonPath -Force -ErrorAction Stop
            break
        }
        catch {
            Start-Sleep -Seconds 1
            $maxWait--
            if ($maxWait -eq 0) {
                Write-ColorOutput "Failed to replace daemon, file still in use: $_" "Red"
                Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
                exit 1
            }
        }
    }
    
    # Wait and replace mcp
    $maxWait = 10
    while ($maxWait -gt 0) {
        try {
            Move-Item -Path $tempMcpPath -Destination $mcpPath -Force -ErrorAction Stop
            break
        }
        catch {
            Start-Sleep -Seconds 1
            $maxWait--
            if ($maxWait -eq 0) {
                Write-ColorOutput "Failed to replace mcp, file still in use: $_" "Red"
                Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
                exit 1
            }
        }
    }
    
    # Cleanup temp dir
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
    
    Write-ColorOutput "Installation complete" "Green"
}

# Create configuration
function New-ConfigFile {
    $configPath = Join-Path $InstallDir "settings.toml"
    
    if (-not (Test-Path $configPath)) {
        $configContent = @'
# acemcp-go configuration
# settings.toml
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
        Write-ColorOutput "Configuration file created: $configPath" "Green"
        Write-ColorOutput "Please edit BASE_URL and TOKEN before starting" "Yellow"
    }
}

# Add to PATH
function Add-ToPath {
    $binDir = Join-Path $InstallDir "bin"
    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    
    if ($currentPath -notlike "*$binDir*") {
        $newPath = $currentPath + ";" + $binDir
        [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
        Write-ColorOutput "Added $binDir to user PATH" "Green"
        Write-ColorOutput "Please restart Command Prompt or PowerShell" "Yellow"
    }
}

# Create launcher
function New-Launcher {
    $binDir = Join-Path $InstallDir "bin"
    $launcherPath = Join-Path $binDir "acemcp.bat"
    
    $launcherContent = @"
@echo off
REM acemcp-go launcher

cd /d "$binDir"

REM check daemon
tasklist /FI "IMAGENAME eq acemcp-go-daemon.exe" 2>NUL | find /I /N "acemcp-go-daemon.exe">NUL
if errorlevel 1 (
    echo Starting acemcp-go daemon...
    start /B acemcp-go-daemon.exe
    timeout /t 2 /nobreak >nul
)

REM start MCP server
acemcp-go-mcp.exe %*
"@
    
    $launcherContent | Out-File -FilePath $launcherPath -Encoding ASCII
    Write-ColorOutput "Launcher created: $launcherPath" "Green"
    
    $binDir = Join-Path $InstallDir "bin"
    $launcherPath = Join-Path $binDir "acemcp.ps1"
    
    $psLauncherContent = @"
# acemcp-go PowerShell launcher
`$binDir = "`$env:USERPROFILE\.acemcp\bin"

# Check daemon status
`$daemon = Get-Process -Name "acemcp-go-daemon" -ErrorAction SilentlyContinue
if (`-not `$daemon) {
    Write-Host "Starting acemcp-go daemon..."
    Start-Process -FilePath (Join-Path `$binDir "acemcp-go-daemon.exe") -WindowStyle Hidden
    Start-Sleep -Seconds 2
}

# Start MCP server
& (Join-Path `$binDir "acemcp-go-mcp.exe") `$args
"@
    
    $psLauncherContent | Out-File -FilePath $launcherPath -Encoding UTF8
}

# Main
function Main {
    Write-ColorOutput "[acemcp-go] quick installer" "Green"
    Write-Host ""
    
    # Stop existing processes before updating
    Write-ColorOutput "Stopping existing acemcp processes..." "Yellow"
    $processes = @("acemcp-go-daemon", "acemcp-go-mcp")
    foreach ($proc in $processes) {
        $running = Get-Process -Name $proc -ErrorAction SilentlyContinue
        if ($running) {
            Write-Host "  Stopping $proc..."
            Stop-Process -Name $proc -Force -ErrorAction SilentlyContinue
            # Wait for process to fully exit
            $waited = 0
            while ((Get-Process -Name $proc -ErrorAction SilentlyContinue) -and $waited -lt 3000) {
                Start-Sleep -Milliseconds 100
                $waited += 100
            }
            # Force kill if still running
            if (Get-Process -Name $proc -ErrorAction SilentlyContinue) {
                Stop-Process -Name $proc -Force -ErrorAction SilentlyContinue
                Start-Sleep -Milliseconds 500
            }
        }
    }
    
    # Detect platform
    $platform = Get-Platform
    Write-ColorOutput "Detected platform: $platform" "Green"
    
    # Get version
    if ($Version -eq "latest") {
        $Version = Get-LatestVersion
    }
    Write-ColorOutput "Version: $Version" "Green"
    
    # Download
    Invoke-BinaryDownload -Version $Version -Platform $platform
    
    # Create configuration
    New-ConfigFile
    
    # Add to PATH
    Add-ToPath
    
    # Create launcher
    New-Launcher
    
    Write-Host ""
    Write-ColorOutput "[Installation complete!]" "Green"
    Write-Host ""
    Write-ColorOutput "Next steps:" "Yellow"
    Write-Host "1. Edit configuration: $InstallDir\settings.toml"
    Write-Host "2. Restart PowerShell"
    Write-Host "3. Configure Cursor MCP server with command: acemcp"
    Write-Host ""
    Write-ColorOutput "Cursor MCP configuration:" "Yellow"
    Write-Host "{"
    Write-Host "  `"mcpServers`": {"
    Write-Host "    `"acemcp`": {"
    Write-Host "      `"command`": `"acemcp`""
    Write-Host "    }"
    Write-Host "  }"
    Write-Host "}"
}

# Run main
Main
