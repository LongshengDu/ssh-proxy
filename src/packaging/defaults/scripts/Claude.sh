#!/usr/bin/env bash
# macOS desktop launcher; keep it in the installed scripts/ folder.
# Fully quit Claude first. CLAUDE_APP selects a desktop app, never the Code CLI.
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
proxy="${PROXY_URL:-http://127.0.0.1:8181}"
app="${CLAUDE_APP:-}"

if [[ "$(uname -s)" != Darwin ]]; then
    echo 'This launcher is for macOS. Use Claude.ps1 on Windows; see README.md for Linux proxy setup.' >&2
    exit 1
fi
if [[ -z "$app" ]]; then
    for candidate in /Applications/Claude.app "$HOME/Applications/Claude.app"; do
        if [[ -d "$candidate" ]]; then app="$candidate"; break; fi
    done
fi
if [[ -d "$app/Contents" ]]; then
    executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Contents/Info.plist")"
    app="$app/Contents/MacOS/$executable"
fi
if [[ -z "$app" || ! -x "$app" ]]; then
    echo 'Claude desktop app not found. Install it or set CLAUDE_APP to its executable (or macOS .app), not the Claude Code CLI.' >&2
    exit 1
fi

# start is idempotent; neither the proxy nor the desktop app needs this terminal.
"$root/ssh_proxy_cli" start
status="$("$root/ssh_proxy_cli" status)"
if ! printf '%s\n' "$status" | grep -Eq '"phase"[[:space:]]*:[[:space:]]*"(running|degraded)"'; then
    printf 'Proxy is not ready. Check its log and retry.\n%s\n' "$status" >&2
    exit 1
fi
export HTTP_PROXY="$proxy" HTTPS_PROXY="$proxy" NO_PROXY='localhost,127.0.0.1,::1'
export http_proxy="$proxy" https_proxy="$proxy" no_proxy="$NO_PROXY"
unset ALL_PROXY all_proxy
mkdir -p "$root/logs"
log="$root/logs/claude_$(date -u +%Y%m%d_%H%M%S)_$$.txt"
nohup "$app" "--proxy-server=$proxy" \
    '--proxy-bypass-list=localhost;127.0.0.1;[::1]' --disable-quic >"$log" 2>&1 < /dev/null &
pid=$!
sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
    if wait "$pid"; then
        echo 'The launcher exited; if Claude was already open, quit it fully and rerun this script.'
    else
        echo "Desktop app failed to start. Read: $log" >&2
        exit 1
    fi
fi
printf 'Desktop launch requested through %s. Log: %s\n' "$proxy" "$log"
