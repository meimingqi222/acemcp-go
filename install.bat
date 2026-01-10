@echo off
REM acemcp-go Windows installer
REM Usage: powershell -c "iwr -useb https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.ps1 | iex"

setlocal enabledelayedexpansion

echo 🚀 acemcp-go quick installer
echo.

REM Check PowerShell version
powershell -Command "if ($PSVersionTable.PSVersion.Major -lt 5) { exit 1 }"
if errorlevel 1 (
    echo ERROR: PowerShell 5.0 or higher is required
    exit /b 1
)

REM Run PowerShell installer
powershell -ExecutionPolicy Bypass -Command "& { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; iwr -useb https://raw.githubusercontent.com/meimingqi222/acemcp-go/master/install.ps1 | iex }"

if errorlevel 1 (
    echo Install failed
    exit /b 1
)

echo.
echo ✅ Installation complete!
echo.
echo Next steps:
echo 1. Edit config: %USERPROFILE%\.acemcp\settings.toml
echo 2. Restart Command Prompt or PowerShell
echo 3. Configure Cursor MCP server with command: acemcp
echo.
echo Cursor MCP configuration:
echo {
echo   "mcpServers": {
echo     "acemcp": {
echo       "command": "acemcp"
echo     }
echo   }
echo }
pause
