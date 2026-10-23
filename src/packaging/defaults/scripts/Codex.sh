#!/usr/bin/env bash
# macOS desktop launcher; keep it in the installed scripts/ folder.
# Fully quit Codex before running it so the new process receives these settings.
set -euo pipefail
SSH_PROXY_URL='http://127.0.0.1:8181'
# Upgrades replace this script: set the fallback below in a renamed copy.
CODEX_APP='' # Fallback desktop executable or .app if automatic discovery finds nothing.
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$(uname -s)" != Darwin ]]; then
    echo 'This launcher is for macOS. Use Codex.ps1 on Windows; see README.md for Linux proxy setup.' >&2
    exit 1
fi
# Locate the installed desktop app.
app="$CODEX_APP"
for candidate in /Applications/ChatGPT.app /Applications/Codex.app \
    "$HOME/Applications/ChatGPT.app" "$HOME/Applications/Codex.app"; do
    if [[ -d "$candidate" ]]; then app="$candidate"; break; fi
done
if [[ -d "$app/Contents" ]]; then
    executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Contents/Info.plist")"
    app="$app/Contents/MacOS/$executable"
fi
if [[ -z "$app" || ! -x "$app" ]]; then
    echo 'Codex desktop app not found. Install it or set CODEX_APP in a renamed copy of this script to its executable or .app.' >&2
    exit 1
fi

# Use the running proxy's URL when available; otherwise keep the default.
if url="$("$root/ssh-proxy-cli" status --proxy_http 2>/dev/null)" && [[ -n "$url" ]]; then
    SSH_PROXY_URL="$url"
fi
export HTTP_PROXY="$SSH_PROXY_URL" HTTPS_PROXY="$SSH_PROXY_URL" NO_PROXY='localhost,127.0.0.1,::1'
export http_proxy="$SSH_PROXY_URL" https_proxy="$SSH_PROXY_URL" no_proxy="$NO_PROXY"
unset ALL_PROXY all_proxy
mkdir -p "$root/logs"
log="$root/logs/codex_$(date -u +%Y%m%d_%H%M%S)_$$.txt"
nohup "$app" "--proxy-server=$SSH_PROXY_URL" \
    '--proxy-bypass-list=localhost;127.0.0.1;[::1]' --disable-quic >"$log" 2>&1 < /dev/null &
pid=$!
sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
    if wait "$pid"; then
        echo 'The launcher exited; if Codex was already open, quit it fully and rerun this script.'
    else
        echo "Desktop app failed to start. Read: $log" >&2
        exit 1
    fi
fi
printf 'Desktop launch requested through %s. Log: %s\n' "$SSH_PROXY_URL" "$log"
