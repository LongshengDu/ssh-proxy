# Windows desktop launcher. Keep this script in the installed scripts/ folder.
# Fully quit Codex before running it so the new process receives these settings.
$SSH_PROXY_URL = 'http://127.0.0.1:8181' # Used when the running proxy's URL can't be read.
# Upgrades replace this script: set the fallback below in a renamed copy.
$CODEX_APP = '' # Fallback desktop executable if automatic discovery finds nothing.

# Locate the Microsoft Store app.
$pkg = Get-AppxPackage -Name OpenAI.Codex | Select-Object -First 1
$candidates = @()
if ($pkg) { $candidates += Join-Path $pkg.InstallLocation 'app\ChatGPT.exe' }
# Launch through the shared helper.
& (Join-Path $PSScriptRoot 'utils\launch-app.ps1') -Name Codex -ProxyUrl $SSH_PROXY_URL -Fallback $CODEX_APP -Candidates $candidates
