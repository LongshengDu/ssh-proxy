# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Claude before running it so the new process receives these settings.
$ErrorActionPreference = 'Stop'
$SSH_PROXY_URL = 'http://127.0.0.1:8181'
# Upgrades replace this script: set the fallback below in a renamed copy.
$CLAUDE_APP = '' # Fallback desktop executable if automatic discovery finds nothing.

# Locate the packaged app or an older per-user desktop installation.
$pkg = Get-AppxPackage -Name Claude | Select-Object -First 1
$candidates = @()
if ($pkg) { $candidates += Join-Path $pkg.InstallLocation 'app\Claude.exe' }
$candidates += Join-Path $env:LOCALAPPDATA 'AnthropicClaude\claude.exe'
$candidates += Join-Path $env:LOCALAPPDATA 'Programs\Claude\Claude.exe'
if ($CLAUDE_APP) { $candidates += $CLAUDE_APP }
$app = $candidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
if (-not $app) {
    throw 'Claude desktop app not found. Install it or set CLAUDE_APP in a renamed copy of this script to its executable (not the Claude Code CLI).'
}

# Use the running proxy's URL when available; otherwise keep the default.
$cli = Join-Path (Split-Path -Parent $PSScriptRoot) 'ssh-proxy-cli.exe'
try {
    $url = & $cli status --proxy_http 2>$null
    if ($LASTEXITCODE -eq 0 -and $url) { $SSH_PROXY_URL = $url }
} catch {}

$env:HTTP_PROXY = $SSH_PROXY_URL
$env:HTTPS_PROXY = $SSH_PROXY_URL
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue

Start-Process -FilePath $app -WindowStyle Normal -ArgumentList @(
    "--proxy-server=$SSH_PROXY_URL",
    '--proxy-bypass-list=localhost;127.0.0.1;[::1]',
    '--disable-quic'
)
