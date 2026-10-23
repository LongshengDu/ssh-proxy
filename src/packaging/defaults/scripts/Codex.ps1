# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Codex before running it so the new process receives these settings.
$ErrorActionPreference = 'Stop'
$SSH_PROXY_URL = 'http://127.0.0.1:8181'
# Upgrades replace this script: set the fallback below in a renamed copy.
$CODEX_APP = '' # Fallback desktop executable if automatic discovery finds nothing.

# Locate the Microsoft Store app.
$pkg = Get-AppxPackage -Name OpenAI.Codex
$candidates = @()
if ($pkg) { $candidates += Join-Path $pkg.InstallLocation 'app\ChatGPT.exe' }
if ($CODEX_APP) { $candidates += $CODEX_APP }
$app = $candidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
if (-not $app) {
    throw 'Codex desktop app not found. Install it or set CODEX_APP in a renamed copy of this script to its executable.'
}

# Use the running proxy's URL when available; otherwise keep the default.
$cli = Join-Path (Split-Path -Parent $PSScriptRoot) 'ssh-proxy-cli.exe'
try {
    $url = & $cli status --proxy_http 2>$null
    if ($LASTEXITCODE -eq 0 -and $url) { $SSH_PROXY_URL = $url }
} catch {}

# For the backend processes launched by the app
$env:HTTP_PROXY = $SSH_PROXY_URL
$env:HTTPS_PROXY = $SSH_PROXY_URL
$env:NO_PROXY = "localhost,127.0.0.1,::1"
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue

# Launch the desktop app with its own proxy setting
Start-Process -FilePath $app -WindowStyle Normal -ArgumentList @(
    "--proxy-server=$SSH_PROXY_URL",
    "--proxy-bypass-list=localhost;127.0.0.1;[::1]",
    "--disable-quic"
)
