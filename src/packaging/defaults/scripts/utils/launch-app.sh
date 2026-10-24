#!/usr/bin/env bash
# Shared by the macOS desktop launchers in scripts/; upgrades replace this file.
# Usage: launch-app.sh NAME DEFAULT_PROXY_URL FALLBACK_APP [CANDIDATE_APP...]
# Starts the first existing candidate, else the fallback (an executable or .app),
# through the running proxy. Fully quit the app first so it receives the settings.
set -euo pipefail
name="$1" SSH_PROXY_URL="$2" app="$3"
shift 3
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
lower="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')"
upper="$(printf '%s' "$name" | tr '[:lower:]' '[:upper:]')"

if [[ "$(uname -s)" != Darwin ]]; then
    echo "This launcher is for macOS. Use $name.ps1 on Windows; see README.md for Linux proxy setup." >&2
    exit 1
fi
for candidate in "$@"; do
    if [[ -e "$candidate" ]]; then app="$candidate"; break; fi
done
if [[ -d "$app/Contents" ]]; then
    executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Contents/Info.plist")"
    app="$app/Contents/MacOS/$executable"
fi
if [[ -z "$app" || ! -x "$app" ]]; then
    echo "$name desktop app not found. Install it or set ${upper}_APP in a renamed copy of $name.sh to its executable or .app." >&2
    exit 1
fi

# Use the running proxy's URL when available; otherwise keep the default.
if url="$("$root/ssh-proxy-cli" status --proxy-http 2>/dev/null)" && [[ -n "$url" ]]; then
    SSH_PROXY_URL="$url"
fi
export HTTP_PROXY="$SSH_PROXY_URL" HTTPS_PROXY="$SSH_PROXY_URL" NO_PROXY='localhost,127.0.0.1,::1'
export http_proxy="$SSH_PROXY_URL" https_proxy="$SSH_PROXY_URL" no_proxy="$NO_PROXY"
unset ALL_PROXY all_proxy
mkdir -p "$root/logs"
# One log, overwritten at each launch; appending keeps a still-running earlier launch's writes in order.
log="$root/logs/$lower.txt"
: >"$log"
nohup "$app" "--proxy-server=$SSH_PROXY_URL" \
    '--proxy-bypass-list=localhost;127.0.0.1;[::1]' --disable-quic >>"$log" 2>&1 < /dev/null &
pid=$!
sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
    if wait "$pid"; then
        echo "The launcher exited; if $name was already open, quit it fully and rerun this script."
    else
        echo "Desktop app failed to start. Read: $log" >&2
        exit 1
    fi
fi
printf 'Desktop launch requested through %s. Log: %s\n' "$SSH_PROXY_URL" "$log"
