#!/usr/bin/env bash
# macOS and Linux: runs a program with its HTTP and HTTPS proxy set to the running SSH Proxy.
# Usage: ./run-via-proxy.sh [--proxy-http URL] APP [ARGS...]
# --proxy-http overrides the address that ssh-proxy-cli status --proxy-http reports.
set -euo pipefail
proxy=''
if [[ "${1:-}" == --proxy-http && $# -ge 3 ]]; then
    proxy="$2"
    shift 2
fi
if [[ $# == 0 || "$1" == --proxy-http ]]; then
    echo 'Usage: run-via-proxy.sh [--proxy-http URL] APP [ARGS...]' >&2
    exit 2
fi
if [[ -z "$proxy" ]]; then
    proxy="$("$(dirname -- "${BASH_SOURCE[0]}")/ssh-proxy-cli" status --proxy-http)"
fi
if [[ -z "$proxy" ]]; then
    echo 'The proxy is not running; start it with ./ssh-proxy-cli start, or pass --proxy-http URL.' >&2
    exit 1
fi
export HTTP_PROXY="$proxy" HTTPS_PROXY="$proxy" NO_PROXY='localhost,127.0.0.1,::1'
export http_proxy="$proxy" https_proxy="$proxy" no_proxy="$NO_PROXY"
unset ALL_PROXY all_proxy
exec "$@"
