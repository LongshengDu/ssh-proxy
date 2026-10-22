# Development workflow

- Run builds and tests in a visible native terminal so the user can watch the output. On Windows, launch PowerShell with `Start-Process -WindowStyle Normal`; leave the window open after completion. Save output and completion status under `build/test-results/` so the agent can also inspect results. `python scripts/visible.py --name NAME -- COMMAND [ARGS...]` does all of this: it opens the window, writes `output.txt` and `completion.json` under `build/test-results/NAME-<stamp>/`, waits, and returns the command's exit code.
- Use `python scripts/run_unit_tests.py` for unit tests and `python scripts/build.py` to refresh the native development build. Do not produce releases routinely.
- Script-only or documentation-only changes do not require application builds or Go unit/system test suites. Keep validation scoped to the changed files.
- Select system tests by the affected component: `python scripts/run_sys_tests.py --cli-only` for CLI/backend changes, `--app-only` for tray changes. Shared changes and test-runner changes need both scopes. With no selection, the runner tests all platform-supported components.
- Use native tools only: no WSL, no cgo, and no macOS application builds during Windows development unless explicitly requested.
- Keep `.version` unchanged unless the user requests a version change.
- When your tray, proxy or another program is running from `build/<os>-<arch>/`, build into another folder (`--target-dir build/scratch`) and test it with `--build-dir`.

# Repository rules

- All compiled application Go code belongs under `src/`; keep the one-way package imports described in README. Ship only `ssh_proxy_cli` and `ssh_proxy_app`; new packages must not add installed programs.
- Tests stay in the flat `tests/unit` and `tests/system` suites with `test_*_test.go` names; reusable helpers belong in `tests/helpers/`. Do not add tests of the build/test scripts themselves.
- Keep real hosts, usernames, keys and logs out of public defaults.
- Update both language sections of `src/packaging/defaults/README.md` for user-visible changes. Do not add chat history, changing verification dates or a second version value to the READMEs.
- Keep CLI flag descriptions/defaults in `src/cli/help.go` synchronized with behavior and keep the `status`/`check` JSON contracts. After an intended help or error-text change, regenerate `tests/unit/testdata/cli/expected.txt` with `SSH_PROXY_UPDATE_EXPECTED=1` and review its diff.
- `src/ui/icon.svg` is the canonical artwork; keep the PNG and ICNS in sync with it. Preserve `src/ui/icon.bak.svg`.
