# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Claude before running it so the new process receives these settings.
# -Proxy overrides the running proxy's HTTP address, for example a different listener.
param(
    [string]$Proxy = '',
    [string]$AppPath = ''
)
$ErrorActionPreference = 'Stop'

# Locate the packaged app or an older per-user desktop installation.
if (-not $AppPath) {
    $pkg = Get-AppxPackage -Name Claude | Select-Object -First 1
    $candidates = @()
    if ($pkg) { $candidates += Join-Path $pkg.InstallLocation 'app\Claude.exe' }
    $candidates += Join-Path $env:LOCALAPPDATA 'AnthropicClaude\claude.exe'
    $candidates += Join-Path $env:LOCALAPPDATA 'Programs\Claude\Claude.exe'
    $AppPath = $candidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
}
if (-not $AppPath -or -not (Test-Path -LiteralPath $AppPath -PathType Leaf)) {
    throw 'Claude desktop app not found. Install it or pass -AppPath with its executable (not the Claude Code CLI).'
}

# Reuse the bundled CLI; it keeps running after this launcher exits.
$cli = Join-Path (Split-Path -Parent $PSScriptRoot) 'ssh_proxy_cli.exe'
& $cli start
if ($LASTEXITCODE -ne 0) { throw 'Proxy startup failed. Check config.toml, ssh_config and logs/.' }
$statusText = & $cli status
if ($LASTEXITCODE -ne 0) { throw 'Could not read proxy status.' }
$status = $statusText | ConvertFrom-Json
if ($status.phase -notin @('running', 'degraded')) {
    throw "Proxy is not ready ($($status.phase)): $($status.last_error). Check the proxy log and retry."
}
if (-not $Proxy) { $Proxy = "http://$($status.http)" }

$env:HTTP_PROXY = $Proxy
$env:HTTPS_PROXY = $Proxy
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue

Start-Process -FilePath $AppPath -WindowStyle Normal -ArgumentList @(
    "--proxy-server=$Proxy",
    '--proxy-bypass-list=localhost;127.0.0.1;[::1]',
    '--disable-quic'
)
