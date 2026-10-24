#!/usr/bin/env bash
# macOS desktop launcher; keep it in the installed scripts/ folder.
# Fully quit Claude before running it so the new process receives these settings.
SSH_PROXY_URL='http://127.0.0.1:8181' # Used when the running proxy's URL can't be read.
# Upgrades replace this script: set the fallback below in a renamed copy.
CLAUDE_APP='' # Fallback desktop executable or .app (not the Claude Code CLI) if automatic discovery finds nothing.

# Locate the installed desktop app.
candidates=(/Applications/Claude.app "$HOME/Applications/Claude.app")
# Launch through the shared helper.
exec /bin/bash "$(dirname -- "${BASH_SOURCE[0]}")/utils/launch-app.sh" Claude "$SSH_PROXY_URL" "$CLAUDE_APP" "${candidates[@]}"
