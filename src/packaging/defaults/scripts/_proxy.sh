# Shared by the macOS desktop launchers in this folder; source it, do not run it.
# The tray does not list files whose names start with an underscore.

# start_ssh_proxy ROOT: start or reuse the proxy in ROOT and print its HTTP proxy URL.
# Command output goes to stderr so the caller can capture only the URL.
start_ssh_proxy() {
    local root="$1" status http
    "$root/ssh_proxy_cli" start >&2
    status="$("$root/ssh_proxy_cli" status)"
    if ! printf '%s\n' "$status" | grep -Eq '"phase"[[:space:]]*:[[:space:]]*"(running|degraded)"'; then
        printf 'Proxy is not ready. Check its log and retry.\n%s\n' "$status" >&2
        return 1
    fi
    # status prints indented JSON with one field per line.
    http="$(printf '%s\n' "$status" | sed -nE 's/^[[:space:]]*"http"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p')"
    if [[ -z "$http" ]]; then
        printf 'Proxy status has no HTTP address.\n%s\n' "$status" >&2
        return 1
    fi
    printf 'http://%s\n' "$http"
}

# set_proxy_environment URL: proxy settings inherited by the desktop app's processes.
set_proxy_environment() {
    export HTTP_PROXY="$1" HTTPS_PROXY="$1" NO_PROXY='localhost,127.0.0.1,::1'
    export http_proxy="$1" https_proxy="$1" no_proxy="$NO_PROXY"
    unset ALL_PROXY all_proxy
}
