# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Codex before running it so the new process receives these settings.
# -Proxy overrides the running proxy's HTTP address, for example a different listener.
param(
    [string]$Proxy = '',
    [string]$AppPath = ''
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '_proxy.ps1')

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
$running = Start-SshProxy
if (-not $Proxy) { $Proxy = $running }

# For the backend processes launched by the app
Set-ProxyEnvironment $Proxy

# Launch the desktop app with its own proxy setting
Start-Process -FilePath $AppPath -WindowStyle Hidden -ArgumentList @(
    "--proxy-server=$Proxy",
    "--proxy-bypass-list=localhost;127.0.0.1;[::1]",
    "--disable-quic"
)
