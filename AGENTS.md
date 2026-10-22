# Development workflow

- Run builds and tests in a visible native terminal so the user can watch the output. On Windows, launch PowerShell with `Start-Process -WindowStyle Normal`; leave the window open after completion. Save output and completion status under `build/test-results/` so the agent can also inspect results.
- Use `python scripts/run_unit_tests.py` for unit tests and `python scripts/build.py` to refresh the native development build. Do not produce releases routinely.
- Script-only or documentation-only changes do not require application builds or Go unit/system test suites. Keep validation scoped to the changed files.
- Select system tests by the affected component: `python scripts/run_sys_tests.py --cli-only` for CLI/backend changes, `--app-only` for tray changes. Shared changes and test-runner changes need both scopes. With no selection, the runner tests all platform-supported components.
- Use native tools only: no WSL, no cgo, and no macOS application builds during Windows development unless explicitly requested.
- Keep `.version` unchanged unless the user requests a version change.
