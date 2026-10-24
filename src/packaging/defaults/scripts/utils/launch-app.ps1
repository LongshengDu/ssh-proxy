# Shared by the Windows desktop launchers in scripts/; upgrades replace this file.
# Starts the first existing candidate, else the fallback executable, through the
# running proxy. Fully quit the app first so it receives these settings.
param(
    [Parameter(Mandatory)] [string] $Name,
    [Parameter(Mandatory)] [string] $ProxyUrl,
    [string] $Fallback = '',
    [string[]] $Candidates = @()
)
$ErrorActionPreference = 'Stop'

$app = @($Candidates) + @($Fallback) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
if (-not $app) {
    throw "$Name desktop app not found. Install it or set $($Name.ToUpper())_APP in a renamed copy of $Name.ps1 to its executable."
}

# Use the running proxy's URL when available; otherwise keep the default.
$cli = Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'ssh-proxy-cli.exe'
try {
    $url = & $cli status --proxy-http 2>$null
    if ($LASTEXITCODE -eq 0 -and $url) { $ProxyUrl = $url }
} catch {}

# For the backend processes launched by the app
$env:HTTP_PROXY = $ProxyUrl
$env:HTTPS_PROXY = $ProxyUrl
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue

# Launch the desktop app with its own proxy setting
Start-Process -FilePath $app -WindowStyle Normal -ArgumentList @(
    "--proxy-server=$ProxyUrl",
    '--proxy-bypass-list=localhost;127.0.0.1;[::1]',
    '--disable-quic'
)
