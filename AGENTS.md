# Development workflow

- Every agent, on Windows, macOS and Linux, runs builds and tests through `python scripts/visible.py --name NAME -- COMMAND [ARGS...]` so the user can watch them. It opens a new terminal window titled NAME (PowerShell on Windows, Terminal on macOS, the desktop's terminal emulator on Linux) that closes automatically when the command finishes, waits, and returns the command's exit code; on failure it also prints the last output lines. Output and completion status stay in `output.txt` and `completion.json` under `build/test-results/NAME-<stamp>/` for the agent to inspect. For runs longer than your tool timeout, add `--no-wait` and read `completion.json` when it appears. Without a desktop it runs in the current terminal.
- Use `python scripts/run-unit-tests.py` for unit tests and `python scripts/build.py` to refresh the native development build. Do not produce releases routinely.
- Script-only or documentation-only changes do not require application builds or Go unit/system test suites. Keep validation scoped to the changed files.
- Select system tests by the affected component: `python scripts/run-sys-tests.py --cli-only` for CLI/backend changes, `--app-only` for tray changes. Shared changes and test-runner changes need both scopes. With no selection, the runner tests all platform-supported components.
- Never auto run system tests. Only run system tests when asked to.
- When system tests fails, only fix the failed one and run the failed one after fix.
- Use native tools only: no WSL, no cgo, and no macOS application builds during Windows development unless explicitly requested.
- Keep `.version` unchanged unless the user requests a version change.
- When your tray, proxy or another program is running from `build/ssh-proxy-<os>-<arch>/`, build into another folder (`--out build/scratch`) and test it with `--build-dir`.

# Repository rules

- All compiled application Go code belongs under `src/`; keep the one-way package imports described in README. Ship only `ssh-proxy-cli` and `ssh-proxy-app`; new packages must not add installed programs.
- Tests stay in the flat `tests/unit` and `tests/system` suites with `*_test.go` names; reusable helpers belong in `tests/helpers/`. Do not add tests of the build/test scripts themselves.
- Keep all code, scripts and tests ready to run natively on Windows, macOS and Linux at all times, even when you can test only one OS: put OS-specific code in `_windows`/`_darwin`/`_linux` files or behind build tags, and give every test that covers one OS's behavior a counterpart for the others where that behavior exists.
- Keep system tests fast: don't repeat a check that another test or the unit suite already covers, wait for conditions instead of fixed sleeps, keep `--wait` values and timeouts as short as the check allows, and mark independent tests `t.Parallel()` (they must not use `t.Setenv`).
- Keep real hosts, usernames, keys and logs out of public defaults.
- Update both language sections of `src/packaging/defaults/README.md` for user-visible changes. Do not add chat history, changing verification dates or a second version value to the READMEs.
- Keep CLI flag descriptions/defaults in `src/cli/cli.go` synchronized with behavior and keep the `status`/`check` JSON contracts. After an intended help or error-text change, regenerate `tests/unit/testdata/cli/expected.txt` with `SSH_PROXY_UPDATE_EXPECTED=1` and review its diff.
- The icon's design lives in `scripts/render-icons.py`; after changing it, rerun the script (packages from `requirements.txt`, see README) to rewrite `src/ui/icon.svg`, `src/ui/icons/` and `src/ui/icon.icns`.
