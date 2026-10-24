# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Claude before running it so the new process receives these settings.
$SSH_PROXY_URL = 'http://127.0.0.1:8181' # Used when the running proxy's URL can't be read.
# Upgrades replace this script: set the fallback below in a renamed copy.
$CLAUDE_APP = '' # Fallback desktop executable (not the Claude Code CLI) if automatic discovery finds nothing.

# Locate the packaged app or an older per-user desktop installation.
$pkg = Get-AppxPackage -Name Claude | Select-Object -First 1
$candidates = @()
if ($pkg) { $candidates += Join-Path $pkg.InstallLocation 'app\Claude.exe' }
$candidates += Join-Path $env:LOCALAPPDATA 'AnthropicClaude\claude.exe'
$candidates += Join-Path $env:LOCALAPPDATA 'Programs\Claude\Claude.exe'
# Launch through the shared helper.
& (Join-Path $PSScriptRoot 'utils\launch-app.ps1') -Name Claude -ProxyUrl $SSH_PROXY_URL -Fallback $CLAUDE_APP -Candidates $candidates
