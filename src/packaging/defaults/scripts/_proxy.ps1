# Shared by the Windows desktop launchers in this folder. The tray does not list
# files whose names start with an underscore.

# Start or reuse the bundled proxy and return its HTTP proxy URL.
function Start-SshProxy {
    $cli = Join-Path (Split-Path -Parent $PSScriptRoot) 'ssh_proxy_cli.exe'
    & $cli start
    if ($LASTEXITCODE -ne 0) { throw 'Proxy startup failed. Check config.toml, ssh_config and logs/.' }
    $statusText = & $cli status
    if ($LASTEXITCODE -ne 0) { throw 'Could not read proxy status.' }
    $status = $statusText | ConvertFrom-Json
    if ($status.phase -notin @('running', 'degraded')) {
        throw "Proxy is not ready ($($status.phase)): $($status.last_error). Check the proxy log and retry."
    }
    return "http://$($status.http)"
}

# Proxy settings inherited by the processes that the desktop app starts.
function Set-ProxyEnvironment([string]$Proxy) {
    $env:HTTP_PROXY = $Proxy
    $env:HTTPS_PROXY = $Proxy
    $env:NO_PROXY = 'localhost,127.0.0.1,::1'
    Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
}
