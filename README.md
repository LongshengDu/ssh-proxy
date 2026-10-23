# SSH Proxy

**Installation and usage: [user guide — 中文 / English](src/packaging/defaults/README.md).**

SSH Proxy provides local HTTP/HTTPS and SOCKS5 proxies through native OpenSSH. The CLI owns the proxy on Windows, macOS and Linux; Windows/macOS also have a small tray app. Ship only `ssh_proxy_cli` and `ssh_proxy_app`, as portable ZIPs. Prefer existing libraries, small adapters and sensible defaults over new abstractions or settings.

## Architecture

```text
HTTP/HTTPS client -> Go HTTP listener :8181 -> SOCKS5 dialer --+
                                                            |
SOCKS client -----------------------------------------------+
                                                            v
                                             native OpenSSH -D :1081
                                                            |
                                                       SSH server
                                                            |
                                                       destination
```

OpenSSH owns encryption, authentication, host trust and SOCKS serving. The remote server only needs SSH TCP forwarding. The Go bridge uses remote DNS and forwards HTTPS through CONNECT without TLS interception or a direct-connection fallback. Listeners use distinct ports on numeric loopback addresses; mixed-port mode, UDP and VPN routing are outside this design.

| Responsibility | Library / implementation |
| --- | --- |
| SSH and SOCKS server | Native `ssh -F`, `-G`, `-D` |
| HTTP, CONNECT, WebSockets, streaming | `elazarl/goproxy` |
| SOCKS client | `golang.org/x/net/proxy` |
| Configuration | `BurntSushi/toml`, `trzsz/ssh_config`; OpenSSH resolves effective settings |
| CLI | `spf13/cobra` |
| Login startup | Per-user Windows Run entries, macOS LaunchAgents, Linux systemd user units |
| Tray | `gogpu/systray`; native dialogs and file openers |
| Process ownership | Windows mutex/Job Object; Unix `gofrs/flock`, sessions and guardian |
| Test SSH server | `gliderlabs/ssh`, `x/crypto/ssh`; no production SSH implementation |

Application builds use `CGO_ENABLED=0`. Use native tools, without WSL. Keep networking in these libraries; adapters should preserve protocol behavior rather than replace it.

## Source map

| Path | Responsibility |
| --- | --- |
| `src/cmd/ssh_proxy_cli`, `src/cmd/ssh_proxy_app` | Two executable entry points |
| `src/cli` | Cobra commands, flags, help and reports |
| `src/config` | TOML, paths/identity, SSH aliases, effective settings and arguments |
| `src/bridge` | HTTP-to-SOCKS integration, connection tracking and probes |
| `src/supervisor` | SSH lifecycle, readiness, health, failover and background run |
| `src/control` | Authenticated local API and runtime state |
| `src/platform` | Locks, detach, child containment, Unix SSH guardian and generic login registration |
| `src/logs` | Continuous run logs, retention and size limits |
| `src/ui` | Tray controller, setup terminal, errors, login and scripts |
| `src/packaging` | Public defaults, bilingual guide, launchers and executable resources |
| `scripts` | `build.py`, `run_unit_tests.py`, `run_sys_tests.py`, `visible.py` |
| `tests/unit`, `tests/system` | Flat `*_test.go` suites; reusable fixtures in `tests/helpers` |
| `build`, `.build-tools` | Ignored outputs/private settings/reports/caches and downloaded Go |

Compiled application code stays under `src/`. Imports flow from `platform` through `config`/`logs`/`bridge`, then `control`/`supervisor` and `cli`. The tray uses `config`, `control`, `platform` and `logs`; it calls the CLI for validation, trust and lifecycle, and reads the same authenticated status API in-process. Internal `_run` and `_ssh` modes reuse the CLI executable. See [AGENTS.md](AGENTS.md) for repository rules.

## Contracts to preserve

### Configuration and SSH

- `config.toml` is always beside the CLI, independent of the working directory. TOML-relative paths resolve from its directory; logs stay beside the CLI. Native SSH runs from the SSH config directory.
- Concrete `Host` aliases in the selected SSH file define candidate order, with duplicates removed. Wildcards, exclusions and `Match` do not declare candidates. Native `ssh -G` resolves `Include`, `Match`, `ProxyJump` and authentication settings; do not duplicate its parser.
- Reject unknown TOML keys and invalid values. Connect-timeout, attempt, keepalive and logging defaults are supplied by `LoadConfig`; readiness, retry and health settings are required. Public settings live in [config.toml](src/packaging/defaults/config.toml).
- Require OpenSSH 8.7+, with clear missing/outdated-client errors. Managed tunnels enforce batch authentication, strict host checking, no TTY/agent forwarding, no SSH backgrounding/multiplexing, and exit on forwarding failure. Use OpenSSH's normal trust files.
- Reuse one matching `DynamicForward`; reject conflicting/multiple forwards and `ClearAllForwardings yes`. Endpoint details belong in `ssh_config`, not TOML.
- `trust` runs an interactive SSH login with remote `exit`, without forwarding. `trust --check` uses noninteractive tunnel authentication policy. `--log` appends setup diagnostics without piping terminal input/output; native SSH owns password hiding.

### Forwarding and lifecycle

`start` detaches the same supervisor used by `start --foreground`. Only the supervisor holding the installation lock creates a run log. Concurrent background starters join the winner through its control API; a second foreground start fails without touching its state or logs. `restart` validates before stopping; foreground runs terminate on Ctrl+C. There is no OS-service routing.

The supervisor locks its identity, reserves listeners and tries aliases in order. Readiness requires SOCKS forwarding to at least one health target. Until ready, HTTP requests receive 503. SSH exit triggers cleanup and ordered failover; exhausting the list waits for the configured retry delay. A live tunnel with failed probes becomes `degraded` and stays connected by default. Probe-triggered failover is opt-in; healthy backup endpoints do not periodically fail back.

HTTP forwarding preserves the client's encoding and response bytes. A small transport adapter aborts incomplete upstream responses; headers for SSE and responses without Content-Length flush before the first data. The transport retains up to 100 idle connections total/per host. User connection setup has a 30-second budget; health probes use their own configured deadline. Established transfers and tunnels have no lifetime timeout. Preserve CONNECT buffered bytes and half-close, streaming flushes/trailers, WebSocket cleanup, cancellation and the generation check that rejects dials completed after tunnel replacement. Stop/failover closes tracked and idle connections; clients reconnect.

`start/restart --wait` bounds the caller's wait, not the supervisor's lifetime. A successful command can still report `connecting`/`reconnecting`; consumers inspect the phase. The tray uses the default wait in a worker while continuing to poll.

Config identity is the first eight SHA-256 bytes of the cleaned absolute TOML path, case-folded on Windows. It scopes instance locks, registration names and runtime state under the user cache (`%LOCALAPPDATA%/ssh_proxy/<id>` on Windows). `SSH_PROXY_RUNTIME` isolates tests. `state.json` contains the PID, config/log paths, ephemeral loopback API address and random token. Only authenticated `GET /status` and `POST /stop` exist. Control calls bypass environment proxies; a stale PID never authorizes killing a process.

Windows contains SSH in a kill-on-close Job Object. Background startup tries to leave the caller's job; if the parent forbids breakaway, closing that job can still stop the proxy. Unix uses a detached session and a guardian attached to a supervisor-owned pipe. Preserve cleanup after supervisor crashes; never terminate by process name.

### CLI, tray and autostart

Plain `status` and `check` return JSON. `status --proxy_http` / `--proxy_socks` return only an `http://` / `socks5h://` URL when the phase is `running`, otherwise empty stdout with exit 0; the flags are mutually exclusive. Other commands return text. `check` exits 0 after producing a validation report, even when `valid` is false; `error`/`edit_file` describe failures. Operational errors use stderr and nonzero exit. Help/version return before configuration or OS access. Flags/defaults live in `src/cli/cli.go`; keep unique command flags in root help and login startup details under `autostart --help`. Plain `help`/`version`, tray-management commands, `--json` and all `service` commands remain rejected.

The tray controller polls about every two seconds, serializes actions and changes native menu items only when their values change. Keep menu structure stable to avoid flicker. Stop-and-exit waits for successful CLI stop; failure keeps the app open. Keeping the proxy running exits only the tray.

Configs and logs open as text regardless of extension: Windows uses the `.txt` association with a Notepad fallback; macOS uses `open -t`. Folders use the normal OS opener. Do not prompt users to choose an application for `ssh_config`.

Start validates configuration, then tries silent trust. Only failed silent trust opens interactive setup; success is rechecked before starting because password-only login cannot run unattended. Invalid SSH config opens once per run; errors show their reason and the same app log, with repeated polling errors suppressed. Windows uses a native console; macOS uses Terminal through AppleScript and may require Automation permission. Setup closes after completion and does not retry until another Start action.

**Start Proxy When App Launches** reuses this Start flow; the tray stores it as `start_proxy_on_launch` in `app.json` beside `config.toml` and rewrites that file whole. The CLI ignores this app setting. Login startup opens only the tray: Windows uses a per-user Run value (a hidden launcher handles the OS length limit); macOS uses a LaunchAgent. Registration existence does not detect external OS disablement.

The app takes no command-line arguments and finds the adjacent CLI, including from its macOS bundle. Script discovery reads `scripts/` once at startup (`.ps1` on Windows, `.sh` on macOS). Scripts run noninteractively with output in the app log. Each bundled Codex/Claude launcher sets `SSH_PROXY_URL` to `http://127.0.0.1:8181`, replacing it only when the adjacent CLI's `status --proxy_http` succeeds with a nonempty URL. Launchers automatically find the installed desktop app, falling back to the script-local `CODEX_APP` or `CLAUDE_APP` path (empty by default). They query status without starting the proxy, then set desktop proxy arguments/environment. Linux uses the user-guide instructions.

`autostart enable|disable|status` belongs to the CLI and never launches the app. Enable validates config and warns, but still registers, if silent SSH login fails. It does not connect immediately; disable leaves the current proxy running. Windows registers `start --wait 1` in HKCU Run; macOS and Linux register `start --foreground` with no restart policy. No elevation, stored account password or pre-login startup is supported. The app's login entry and fallback settings are separate. Old app entry formats at the same location are repaired on app launch. Folder moves change identity: disable both entries before moving and enable afterwards.

`status.autostart` reports the CLI registration without starting another process. Registration existence does not guarantee OS-enabled state (for example, disabling it in Windows Task Manager). The retained `service` JSON field is always false for compatibility.

Only the instance-lock holder writes or deletes `state.json`. An intentional stop removes it; failures replace it with an exit record (time, reason, run log), and hard kills leave dead-PID state. Both report `stopped` with `last_error`; a successful start replaces old diagnostics. Parents reuse child diagnostics; only failures before the child owns a log allocate a parent-written failure log. Bootstrap output uses a temporary runtime file, removed after launch. Panics go to the run log through `debug.SetCrashOutput`; recovered panics explicitly log `debug.Stack`. The tray alerts once when a supervisor it observed alive becomes an unclean stopped run, opening that proxy log. Old failures and normal external stops do not trigger alerts. Hung supervisors require manual recovery; PID/path guesses never authorize killing.


### Logs

Each app/proxy run owns one continuous file under `logs/`. Retention applies independently to tray/proxy logs across config identities; active files are protected and unrelated files are untouched. Instance locks precede app/proxy log creation, including background runs.

When a log exceeds its size limit, remove its older half in place and mark the cut. Only the owner truncates; hold truncation while interactive trust/native `ssh -E` append. The cut marker carries a cumulative byte offset so `logs --follow` neither skips nor repeats retained text. Logging must capture configuration failures using default limits when TOML is unreadable.

## Build and distribution

Use Python 3.10+ and OpenSSH 8.7+. `build.py` discovers a Go version satisfying [go.mod](go.mod), or downloads a project-local toolchain with SHA-256 verification. No C compiler or pip packages are needed for builds. Runtime targets are Windows 10+, macOS 13+ and Linux, on amd64/arm64; the app is Windows/macOS only. Cross-builds do not validate runtime behavior. Do not build macOS during Windows development unless requested.

```sh
python scripts/build.py
python scripts/build.py --target-dir build/scratch
python scripts/build.py --release
python scripts/build.py --release --os linux --arch arm64 --target-dir ./packages
```

Use `python3` on macOS/Linux. Default builds target the current OS/architecture in `build/<os>-<arch>/`; release mode produces a full folder and sibling ZIP under `build/release/`. `--target-dir`, `--os`, `--arch` override these; `--go` selects a toolchain.

Development builds preserve existing config, SSH file, scripts and README, adding missing defaults and replacing binaries/metadata. Build into another folder while the normal build is running. Releases use public defaults and an explicit payload; private settings/unrelated files never enter ZIPs. The macOS `.app` stays beside CLI/configs, and its plist declares the OS minimum. Packages are unsigned/not notarized.

[.version](.version) is the sole version source; never increment automatically. Packaging generates build info, hashes and notices for modules actually linked into each target. Text metadata/defaults use LF. `go-winres`, pinned in `go.mod`, embeds Windows resources before hashing. The builder never runs tests.

## Development and verification

All four Python scripts support Windows, macOS and Linux with Python 3.10+. Run them from the repository root in a visible native terminal. `visible.py` opens PowerShell on Windows and closes that window when the command finishes; on macOS/Linux it uses the current terminal. Output and completion are saved under `build/test-results/`. Use `python3` instead of `python` on macOS/Linux if needed.

```sh
python scripts/visible.py --name unit -- python scripts/run_unit_tests.py
python scripts/visible.py --name build -- python scripts/build.py
python scripts/visible.py --name cli -- python scripts/run_sys_tests.py --cli-only
python scripts/visible.py --name app -- python scripts/run_sys_tests.py --app-only
```

Run system tests only when requested; the table identifies the relevant scope.

| Change | Checks |
| --- | --- |
| CLI/backend Go | Unit/static checks, native build, CLI system tests |
| Tray Go | Unit/static checks, native build, app system tests |
| Shared Go or system-test runner | Both system-test scopes |
| Packaging | Exercise the affected build/release path |
| Scripts/docs only | Scoped checks; no application rebuild or Go suites |

The unit runner verifies modules/formatting, runs vet/staticcheck for Windows/macOS/Linux, then executes host unit tests. Staticcheck is pinned in `go.mod` and cached under `build/.cache/bin`. The system runner tests existing binaries; use `--build-dir build/scratch/<os>-<arch>` for a separate build. No selection runs all platform-supported tests. App-only needs the CLI; Linux runs CLI tests and rejects app-only. After a failure, rerun only that test through the same runner, for example:

```sh
python scripts/visible.py --name retry -- python scripts/run_sys_tests.py --app-only --test TestTrayNativeWorkflow
```

`--test NAME` selects one top-level test, including its subtests, and still performs platform permission checks and writes a result summary. A misspelled or out-of-scope name fails instead of reporting success with no tests.

System tests use isolated configs/runtime paths, ephemeral SSH keys, local servers and native UI automation. They must not stop existing user proxies or modify user trust. `--live-config PATH` opts into real endpoint checks and is incompatible with app-only. Autostart tests use isolated registrations and launch through launchd/systemd or the Windows Run command. Windows also requires a manual real sign-in check; OS-manager environments and SSH-agent access need native validation.

App tests need an unlocked desktop and default text editor. On macOS, install the permission-request dependency once (a virtual environment avoids changing the system Python):

```sh
python3 -m venv build/venv
source build/venv/bin/activate
python -m pip install -r requirements.txt
```

Then use the build/test commands above from Terminal, without sudo. Before app tests, the runner requests Accessibility and Automation for System Events, Terminal and TextEdit, opens System Settings when needed, and waits for you to grant access. Allow any SSH Proxy → Terminal prompt as well. The app opens configs/logs with `open -t`; opening text does not require UI automation. Tests use TextEdit automation to verify full document paths and close only their own documents, even when TextEdit was already running. Other default editors must expose document names in their window titles.

Windows/Linux need no Python packages; Linux needs the native OpenSSH client and runs no tray automation. Reports contain output, JSON events and a summary; every selected test must pass, with only the unrequested live test allowed to skip. Run on each native OS to verify its behavior: Windows results and static checks do not certify macOS/Linux runtime behavior.

CLI snapshots record each distinct help/error output once; equivalent invocations compare complete stdout, stderr and exit codes. Keep forbidden-command coverage. After intended output changes, regenerate with `SSH_PROXY_UPDATE_EXPECTED=1` and review the diff.

## Assets and documentation

Keep developer design here and user instructions in the Chinese-first/English [user guide](src/packaging/defaults/README.md). Update both user-guide languages for user-visible changes. Public defaults must contain no private hosts, usernames, keys or logs.

`src/ui/icon.svg` is canonical; keep PNG/ICNS synchronized. The tray embeds PNG; Windows uses `src/packaging/windows-icon.json`; macOS packages ICNS. Optional regeneration:

```sh
uv run --with resvg-py==0.5.0 python -c "from pathlib import Path; import resvg_py; p=Path('src/ui/icon.svg'); p.with_suffix('.png').write_bytes(resvg_py.svg_to_bytes(svg_path=str(p), skip_system_fonts=True))"
uv run --with pillow==12.0.0 python -c "from PIL import Image; Image.open('src/ui/icon.png').save('src/ui/icon.icns', format='ICNS')"
```
