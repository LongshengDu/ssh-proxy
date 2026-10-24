#!/usr/bin/env bash
# macOS desktop launcher; keep it in the installed scripts/ folder.
# Fully quit Codex before running it so the new process receives these settings.
SSH_PROXY_URL='http://127.0.0.1:8181' # Used when the running proxy's URL can't be read.
# Upgrades replace this script: set the fallback below in a renamed copy.
CODEX_APP='' # Fallback desktop executable or .app if automatic discovery finds nothing.

# Locate the installed desktop app.
candidates=(/Applications/ChatGPT.app /Applications/Codex.app
    "$HOME/Applications/ChatGPT.app" "$HOME/Applications/Codex.app")
# Launch through the shared helper.
exec /bin/bash "$(dirname -- "${BASH_SOURCE[0]}")/utils/launch-app.sh" Codex "$SSH_PROXY_URL" "$CODEX_APP" "${candidates[@]}"
