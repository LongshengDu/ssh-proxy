# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Codex before running it so the new process receives these settings.
param(
    [string]$Proxy = 'http://127.0.0.1:8181',
    [string]$AppPath = ''
)
$ErrorActionPreference = 'Stop'

# Locate the Microsoft Store app, or accept an explicit desktop executable.
if (-not $AppPath) {
    $pkg = Get-AppxPackage -Name OpenAI.Codex
    if (-not $pkg) { throw 'Codex Store package not found. Install the desktop app or pass -AppPath.' }
    $AppPath = Join-Path $pkg.InstallLocation 'app\ChatGPT.exe'
}
if (-not (Test-Path -LiteralPath $AppPath -PathType Leaf)) {
    throw "Desktop executable not found: $AppPath. Pass -AppPath with its installed location."
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

# For the backend processes launched by the app
$env:HTTP_PROXY = $Proxy
$env:HTTPS_PROXY = $Proxy
$env:NO_PROXY = "localhost,127.0.0.1,::1"
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue

# Launch the desktop app with its own proxy setting
Start-Process -FilePath $AppPath -WindowStyle Hidden -ArgumentList @(
    "--proxy-server=$Proxy",
    "--proxy-bypass-list=localhost;127.0.0.1;[::1]",
    "--disable-quic"
)
