# SSH Proxy

**Looking for installation and usage? Read the [user guide — 中文 / English](src/packaging/defaults/README.md).**

SSH Proxy runs local HTTP/HTTPS and SOCKS proxies through an existing OpenSSH client. The cross-platform CLI owns the proxy; Windows and macOS also provide a small tray/menu-bar app. Linux has CLI commands only. Distributions are portable ZIPs with editable configuration, not installers.

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

Both listeners run locally. The server needs SSH access with TCP forwarding enabled; it needs no HTTP proxy installation. OpenSSH owns SSH encryption, authentication, host trust and SOCKS serving. The Go bridge sends destination names through SOCKS for remote DNS and forwards HTTPS using CONNECT without TLS interception. It has no direct-connection fallback.

The two listeners require distinct ports and numeric loopback addresses. A shared HTTP/SOCKS port would need a protocol-dispatching listener; this implementation has no mixed-port mode. It provides TCP proxying, not a VPN or system-wide traffic interception.

The tray invokes the CLI for validation, trust and lifecycle, and reads status in-process from the running supervisor's authenticated loopback API, the same API the CLI uses. Closing the tray does not stop the supervisor or its SSH child.

| Responsibility | Implementation |
| --- | --- |
| SSH and SOCKS server | System `ssh` / `ssh.exe`; native `-F`, `-G` and `-D` |
| HTTP, CONNECT, WebSockets and streaming | `github.com/elazarl/goproxy` with small bridge adapters |
| SOCKS client | `golang.org/x/net/proxy` |
| Configuration | `BurntSushi/toml`, `trzsz/ssh_config`; OpenSSH resolves effective host settings |
| OS services | `kardianos/service`; Windows SCM, macOS launchd, Linux user systemd |
| Tray/menu | `gogpu/systray`; native dialogs and file openers |
| Process ownership | Windows mutex/Job Object; Unix `gofrs/flock`, sessions and guardian process |
| Test SSH server | `gliderlabs/ssh` and `x/crypto/ssh`, used only by tests |

Keep protocol and OS behavior in existing libraries. Application builds use `CGO_ENABLED=0`; do not add cgo, WSL workflows, another SSH stack or a custom tray framework.

## Source map

```text
src/
  cmd/ssh_proxy_cli/       CLI entry point, all platforms
  cmd/ssh_proxy_app/       Tray entry point, Windows/macOS
  cli/                    Commands, flags, reports and aligned help (help.go)
  config/                 TOML validation, paths/identity, SSH aliases, ssh -G and command arguments
  bridge/                 HTTP-to-SOCKS integration, connection tracking and health probes
  supervisor/             SSH lifecycle, readiness, health, failover and the background run
  control/                Authenticated local API, runtime state and status
  platform/               Platform locks, process containment, detach and the Unix SSH guardian
  service/                OS service registration and lifecycle
  logs/                   Continuous run logs, retention and size limit
  ui/                     Tray state, setup terminal, errors, login and scripts
  packaging/defaults/     Public TOML, SSH template, bilingual guide and launchers
scripts/
  build.py                Toolchain bootstrap, build, resources and ZIP packaging
  run_unit_tests.py        Unit tests and static checks
  run_sys_tests.py         Tests against already-built native programs
  visible.py               Run a command in a visible window and record its result
tests/
  unit/                   Flat unit tests
  system/                 Flat CLI/native app system tests
  helpers/                Shared fixtures, local SSH server and UI automation
.version                  Sole version source; changed only when requested
requirements.txt          macOS UI-test Python dependency
build/                    Ignored builds, private settings, reports and caches
.build-tools/             Ignored downloaded Go toolchain
```

All compiled application Go code belongs under `src/`. Imports flow one way: `config`, `logs`, `platform` and `bridge` at the bottom, then `control` and `supervisor`, then `service`, then `cli`; the tray (`ui`) uses only `config`, `control`, `platform` and `logs`. The packages build into just two programs, `ssh_proxy_cli` and `ssh_proxy_app`; services and internal helper modes (`_run`, `_ssh`, `_service`) reuse the CLI executable. Test files use `test_*_test.go` names in the two flat suites; reusable helpers belong in `tests/helpers/`. Do not add tests of the build/test scripts themselves. Keep real hosts, usernames, keys and logs out of public defaults. Read [AGENTS.md](AGENTS.md) before working.

## Configuration and SSH contracts

The complete public settings are in [config.toml](src/packaging/defaults/config.toml); the annotated [ssh_config](src/packaging/defaults/ssh_config) explains endpoint and key setup.

- The default TOML is beside the CLI, independent of the working directory. `-c` / `--config` selects another file before or after the command. TOML-relative paths resolve from its directory; `~/` is supported. Logs remain beside the CLI. Native SSH runs from the SSH config's directory.
- Endpoints come only from concrete `Host` aliases in the selected SSH file, in first-appearance order with duplicates removed. Wildcards, exclusions and `Match` blocks do not declare candidates. `Include`, `Match`, `ProxyJump` and authentication settings are resolved by native OpenSSH; candidate aliases must still be declared in the selected file.
- Unknown TOML keys fail validation. Ports must differ and be in `1..65535`; SSH/health durations and counts are in `1..3600`; `logging.max_log_files` is at least 1; `logging.max_size_mb` is in `1..1024`. Defaults for omitted shared SSH timing fields and log limits are applied in `LoadConfig`.
- The legacy keys `health.target` and `logging.backups` were removed and now fail validation as unknown settings. There is no TOML endpoint list or application-specific known_hosts setting.

Managed tunnels use native `ssh -F <file> -N -D <address> <alias>` with enforced command-line policy: batch authentication, strict host-key checking, no TTY/agent forwarding, no SSH backgrounding or connection multiplexing, and failure on forwarding errors. Shared connect/keepalive settings come from TOML. `ssh -G` supplies effective host/user/port and checks `DynamicForward`: a single matching forward is reused; conflicting/multiple dynamic forwards and `ClearAllForwardings yes` are rejected. The application does not replace OpenSSH's trust files.

`trust [ENDPOINT_NAME]` enables interactive authentication and host confirmation, disables forwarding, and runs remote `exit`. Without a name it tries aliases until the first success. It does not install keys or save credentials. SSH owns the terminal handles and password hiding; `-n` prevents forwarding terminal input to the remote `exit` command. Optional `--log FILE` appends CLI setup messages and native SSH `-E` diagnostics to an existing log without piping interactive input/output. `trust --batch` is the tray's silent check: the tunnel's noninteractive policy (batch authentication, strict host keys, no input) with remote `exit`, in the same endpoint order; it never prompts or accepts a new host key and fails when setup is needed.

## Supervisor, control and logs

`start` creates a detached `_run` supervisor unless a registered service handles the request. Repeated starts reuse an authenticated running instance. `start --foreground` runs attached; Ctrl+C stops it. `restart` validates, stops and starts with the current configuration. An active standalone instance takes priority over a stopped service; service-access errors never trigger an unmanaged duplicate.

The supervisor locks the config identity, reserves the HTTP port, checks SOCKS port availability and tries SSH aliases in order. An endpoint becomes ready only when TCP forwarding through SOCKS reaches at least one configured health target. HTTP requests receive 503 while the bridge is not ready.

| Event | Behavior |
| --- | --- |
| Endpoint fails to start or SSH exits | Close its connections and try the next alias |
| Every endpoint fails | Wait `ssh.retry_delay_seconds`, then retry the list |
| An established tunnel fails a health probe | Mark `degraded`; preserve the session by default |
| Probe failover explicitly enabled | Replace the tunnel after `health.failures_before_failover` consecutive failures |
| A backup stays healthy | Keep using it; no periodic failback to the first endpoint |
| Stop or tunnel replacement | Close existing streams; clients must reconnect |

`start/restart --wait` bounds the caller's readiness wait (default 20 seconds); it does not bound the supervisor's lifetime. A successful command may still report `connecting` or `reconnecting`. Consumers must inspect the resulting phase. The tray uses a short wait and keeps polling.

Preserve bridge regression behavior when changing libraries: CONNECT buffered bytes, streaming flushes, WebSocket upgrades, hop-by-hop header handling, half-closes, cancellation, and generation checks that reject connections opened against a replaced tunnel. Tracked/idle upstream connections must close when readiness is withdrawn.

Config identity is the first eight SHA-256 bytes of the cleaned absolute TOML path, case-folded on Windows. It scopes locks, service/login names and runtime state. State lives under `%LOCALAPPDATA%/ssh_proxy/<id>` or the user cache directory; `SSH_PROXY_RUNTIME` overrides it for isolated tests/services. `state.json` records the PID, config, log, ephemeral loopback API address and a random bearer token. Only authenticated `GET /status` and `POST /stop` exist. Control calls bypass environment proxies and validate identity; a stale PID never authorizes terminating another process.

Windows contains the SSH child in a Job Object with kill-on-close. The background supervisor itself is started with `CREATE_BREAKAWAY_FROM_JOB` so it leaves a terminal's or IDE's kill-on-close job; a job that forbids breakaway keeps it, and closing that host then stops the proxy. Unix uses a guardian attached to a supervisor-owned pipe. Preserve cleanup on supervisor crashes and avoid process-name-wide termination.

Each tray or proxy run creates one continuous file under `logs/`: `tray_<id>_...txt` or `logs_<id>_...txt`. Errors append to that file. `max_log_files` retains the newest files independently for each kind across identities in that directory, including the current file; default 10. Unrelated files and symlinks are untouched. A run is never split into several files: when a file would exceed `logging.max_size_mb` (default 10), its older half is removed in place and a marker line notes the cut, so other appenders such as child output and native `ssh -E` continue in the same file. `logs --follow` reports the cut and continues from the new end. There is no per-error log. Logging starts early enough to record configuration errors; unreadable TOML uses the default limits. Foreground output also reaches the terminal.

## CLI and tray boundaries

| Interface | Contract |
| --- | --- |
| `status` | JSON state, endpoint, listener addresses, health/error and log information |
| `check` | JSON with `valid`; invalid configuration may include `error` and `edit_file`. Producing this report exits 0 even when `valid` is false |
| Other public commands | Text output; operational errors go to stderr with a nonzero exit |
| Help/version | `-h` / `--help`, including command-specific help; `-v` / `--version` |

There is no `--json` switch or plain `help`/`version` command. Help returns before reading configuration or accessing services. `src/cli/help.go` shares flag/default definitions with runtime parsing and renders aligned, plain-text help within 80 columns.

The tray has no network supervisor of its own. `src/ui/controller.go` holds its state: it reads status about every two seconds, serializes user actions and updates native menu items only when their values change. This avoids menu flicker. The menu shows status/endpoint/ports, Start/Stop, scripts, login startup, launch auto-start, configs, logs/location and Exit.

At launch, `check` identifies invalid configuration. Missing/invalid SSH configuration opens for editing once per tray run. Other failures use a native error dialog and the current tray log; repeated identical polling errors are suppressed until recovery. File actions use OS associations.

Every tray Start validates the configuration with `check` (opening a failing SSH file once) and then runs the hidden `trust --batch`. When that succeeds, it starts the proxy without opening a terminal. Only when it fails does the tray run interactive `trust --log <tray-log>` in a real terminal; after exit 0 it repeats `trust --batch`, because a password login alone cannot run the background proxy, and starts only when that passes. Windows creates a console process with native handles; macOS controls Terminal through AppleScript and a temporary exit-status file, which may need Automation permission. The setup window closes after completion. A cancelled or failed setup, or a failed recheck, leaves the proxy stopped with the reason in an error dialog and the tray log; nothing retries until the user chooses Start again. Closing the tray preserves an already-running proxy.

**Start Proxy When App Launches** stores `app.start_proxy_on_launch` in `config.toml` (default `false`). The tray changes only that line, adding an `[app]` table when missing, keeps comments and line endings, replaces the file atomically and refuses read-only or unparsable files. At launch, with the setting on, a stopped proxy goes through the same flow as Start; a running proxy is reused, and invalid configuration only opens its file and reports the reason. Status polls never repeat setup. The CLI and OS services ignore the setting and never open an editor or terminal.

The app locates the adjacent CLI, including from inside its macOS bundle. Its `--config PATH` and `--backend PATH` flags support alternate configurations and development binaries; `--smoke-test` briefly creates the native tray then exits. These are app flags, not CLI commands.

The app owns **Launch App at Login**, which opens only the tray:

| OS | Registration |
| --- | --- |
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, value `SSHProxy-<id>`: the app path (quoted when needed), plus `--config PATH` for a non-default config. Only a command over the 260-character Run limit falls back to a hidden PowerShell launcher that reads stored executable/config paths |
| macOS | `~/Library/LaunchAgents/com.sshproxy.login.<id>.plist` with `RunAtLoad` |

The checkbox currently checks registration existence, not external OS disablement. Keep this limitation visible in the user guide. The CLI has no tray/login-startup management commands.

At tray startup, `scripts/` is scanned in filename order for `.ps1` on Windows or `.sh` on macOS; names starting with `_` are shared helpers, not menu entries. Entries are enabled only when the proxy is `running` and no action is pending. Script output joins the tray log; failure uses the normal error dialog. Scripts are noninteractive, and restarting the tray refreshes the list.

Bundled Codex/Claude launchers share `scripts/_proxy.ps1` or `_proxy.sh`: they start or reuse the CLI, verify readiness, take the proxy URL from the `http` field of `status`, set HTTP proxy environment variables and Electron proxy arguments, clear ALL_PROXY and disable QUIC. Windows scripts accept `-AppPath`/`-Proxy`; macOS scripts accept `CODEX_APP` or `CLAUDE_APP` and `PROXY_URL`; the proxy option overrides the running proxy's address, and keep desktop output in `logs/codex_*.txt` or `logs/claude_*.txt`. They do not install apps, stop existing apps or modify TOML. Linux users configure their clients directly; the Bash launchers are macOS-only.

## Optional OS services

Services are part of `src/service/` and every build. They run the same supervisor. Registration uses the current user; ordinary `start`, `stop` and `restart` control the proxy. Service subcommands only install/uninstall, inspect status and enable/disable automatic startup.

| OS | Integration and requirements |
| --- | --- |
| Windows | SCM service; install/uninstall/enable/disable need elevation. Install infers the current account and prompts for its Windows password, which SCM stores for service logon. It configures delayed automatic start, recovery and owner control permissions |
| macOS | Per-user launchd agent, without sudo; automatic start at login. A launchctl-disabled service must be re-enabled before manual start |
| Linux | Per-user systemd unit, without sudo; requires a working user manager. Uses `default.target`, restart-on-failure and control-group cleanup |

Install enables future startup without starting immediately. Uninstall stops and removes the registration. Automatic service startup is independent of tray login startup. User services must have noninteractive SSH credentials available; Linux can import an agent socket with `systemctl --user import-environment SSH_AUTH_SOCK`. Boot/login credentials, recovery and privileged registration require native platform verification beyond the ordinary isolated suite.

## Build and distribution

Use Python 3.10+ and native OpenSSH. The builder discovers a Go version satisfying [go.mod](go.mod), or downloads an official project-local toolchain with SHA-256 verification. Go modules supply application dependencies. Normal builds require no pip packages or C compiler. A missing Windows OpenSSH Client can be installed through Optional Features with elevation; other hosts need their native OpenSSH client installed.

From the repository root, use `python` on Windows or `python3` on macOS/Linux:

```sh
python scripts/build.py
python scripts/build.py --target-dir build/scratch
python scripts/build.py --release
python scripts/build.py --release --os linux --arch arm64 --target-dir ./packages
```

| Option | Result |
| --- | --- |
| No options | Current OS/architecture into `build/<os>-<arch>/` |
| `--release` | Folder plus sibling `<os>-<arch>.zip`, under `build/release/` by default |
| `--target-dir PATH` | Override the output parent |
| `--os windows\|darwin\|linux` | Target OS, default current |
| `--arch amd64\|arm64` | Target architecture, default current |
| `--go PATH` | Explicit compatible Go executable |

All six OS/architecture combinations are supported build targets. The CLI is included everywhere; Windows also has `ssh_proxy_app.exe`, and macOS has `ssh_proxy_app.app`. The macOS bundle stays beside the CLI/configs; `Info.plist` and ICNS are staged by the builder. Cross-building does not validate runtime behavior. Do not build macOS from Windows unless explicitly requested. Current packages are unsigned/not notarized.

Development builds preserve existing public-default files—including TOML, SSH config, scripts and README—and add missing ones. Binaries/resources, notices, build information and hashes refresh. Close programs using the target executables before replacing them, or use another target directory. Inspect edited documentation/scripts in source; rebuilding does not overwrite their existing development copies.

Releases replace packaged files with public defaults and produce a ZIP from an explicit payload plus an empty `logs/`. Private development settings are never the release source. Existing unrelated destination files/logs are left on disk but excluded from the ZIP. Payload metadata includes `.version`, `BUILDINFO.txt`, `THIRD_PARTY_NOTICES.txt` and `SHA256SUMS`. The notices cover only the modules linked into that target's binaries, as listed by `go version -m` (the same data as `BUILDINFO.txt`), not test or tool dependencies. Extraction changes no system proxy, PATH, startup or service settings.

The root [.version](.version) is the sole version source. The builder injects it into binaries and bundle metadata and never increments it. Change it only when requested. Build caches/temp files live in `build/`; downloaded tools live in `.build-tools/`.

## Development and verification

Choose work by the component changed:

| Change | Validation |
| --- | --- |
| Go CLI/backend | Unit/static checks, native build, CLI-only system tests |
| Go tray/UI | Unit/static checks, native build, app-only system tests |
| Shared Go behavior or system-test runner | Both system-test scopes |
| Packaging behavior | Exercise the affected build/package path; use release mode when that path changes |
| Scripts or documentation only | Review the changed files; no application rebuild or Go test suites |

For Go changes:

```sh
python scripts/run_unit_tests.py
python scripts/build.py
python scripts/run_sys_tests.py --cli-only
# Choose --app-only for tray changes, or omit the selection to run all supported tests.
```

Run builds/tests in a visible native terminal and retain output/completion status under `build/test-results/`. On Windows use a normal visible PowerShell window and leave it open for review; `python scripts/visible.py --name NAME -- COMMAND` does this and records `output.txt` and `completion.json`. The builder does not run tests or vet. Unit validation verifies modules, fails on unformatted files (`gofmt -l`), runs `go vet` and `staticcheck` (pinned as a go.mod tool; settings in `staticcheck.conf`) for Windows, macOS and Linux, then executes `tests/unit/` on the host. On Windows the runners put the native `System32\OpenSSH` client ahead of other `ssh` builds on PATH.

System tests use the existing build, compile with the `systemtest` tag, and stage public defaults with the built programs. They use isolated config identities, ephemeral keys, loopback SSH/HTTP servers and native OpenSSH. They cover CLI configuration/help/reports, tunnel lifecycle, health/failover, HTTP/HTTPS/streaming/WebSockets, logging and crash cleanup; app tests cover actual menus, scripts, setup terminals, file opening, error dialogs, login registration and single-instance behavior. They do not install real proxy services or alter user SSH trust/keys.

Useful runner options:

```sh
python scripts/run_sys_tests.py --app-only --build-dir build/scratch/windows-amd64
python scripts/run_sys_tests.py --cli-only --live-config build/windows-amd64/config.toml
```

`--live-config` opts into real endpoint/Internet checks with isolated listener ports and is incompatible with app-only. App-only still needs the CLI. Linux rejects app-only; its default suite is the CLI suite.

App tests need an unlocked desktop and a working default text editor. On macOS first run `python3 -m pip install -r requirements.txt`. The runner requests Accessibility and Automation access for Python/Terminal/System Events, opens System Settings when needed, and waits for the user to grant permission. CLI-only does not need these UI permissions. No Windows/Linux Python packages are required.

Each system run writes `output.txt`, `events.jsonl` and `summary.json` under `build/test-results/system-<timestamp>/`. A pass requires every system test compiled for the host in the chosen scope (discovered with `go test -list`) to pass, with no skips except `TestLiveEndpoints` without `--live-config`. Scope is recorded; Windows tests or a cross-build do not certify macOS/Linux. Live network endurance, OS boot/login behavior and real service credentials need separate native verification.

## Maintaining assets and documentation

Keep the developer guide here and the Chinese-first/English user guide in `src/packaging/defaults/README.md`. Update both language sections for user-visible changes. Keep CLI flag descriptions/defaults in `src/cli/help.go` synchronized with behavior; `tests/unit/testdata/cli/expected.txt` records the exact help and error output and retain structured JSON contracts for the tray. Do not add chat history, changing verification dates or a second version value to the READMEs.

`src/ui/icon.svg` is the canonical artwork; keep a matching PNG and macOS ICNS. Preserve `icon.bak.svg`. The tray embeds the PNG; the Windows builder embeds it into the executable through pure-Go `go-winres` (pinned as a go.mod tool and run with `go tool go-winres`) using `src/packaging/windows-icon.json`. Apply resources before hashes/signing. Optional asset regeneration tools:

```sh
uv run --with resvg-py==0.5.0 python -c "from pathlib import Path; import resvg_py; p=Path('src/ui/icon.svg'); p.with_suffix('.png').write_bytes(resvg_py.svg_to_bytes(svg_path=str(p), skip_system_fonts=True))"
uv run --with pillow==12.0.0 python -c "from PIL import Image; Image.open('src/ui/icon.png').save('src/ui/icon.icns', format='ICNS')"
```
