#!/bin/bash
# 首次在 macOS 使用前运行一次（每次下载新版本后也要运行）。
# Run once before first use on macOS, and again after each new download:
#     bash run-on-mac.sh
# SSH Proxy 未签名，macOS 会阻止从网络下载的程序；此脚本解除本目录的下载限制。
# SSH Proxy is unsigned, so macOS blocks it when downloaded; this removes the download block from this folder.
set -euo pipefail
folder="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ "$(uname -s)" != Darwin ]]; then
    echo "This script is for macOS only. / 此脚本仅用于 macOS。" >&2
    exit 1
fi
xattr -dr com.apple.quarantine "$folder"
echo "Done / 完成: $folder"
echo "Now open ssh-proxy-app.app, or use ./ssh-proxy-cli in Terminal."
echo "现在可以打开 ssh-proxy-app.app, 或在终端使用 ./ssh-proxy-cli"
