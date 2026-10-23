# SSH Proxy

[中文使用指南](#中文使用指南) · [English guide](#english-guide)

## 中文使用指南

### 使用前必读

- **升级或移动目录：** 先停止代理并退出托盘，备份 `config.toml`、`ssh_config` 及自定义脚本。升级时保留自己的配置和日志，不要用 ZIP 中的默认配置覆盖。升级或移动前取消 **Launch App at Login**，并卸载已注册的系统服务；完成后按需重新启用或安装。
- **完整解压到可写的固定目录：** 保留应用、CLI、配置和 `scripts/` 在一起；macOS 不要只移动 `.app`。Windows/macOS 从 `ssh_proxy_app` 打开托盘；Linux 使用 CLI。
- **先准备 SSH：** 本机需要 OpenSSH，服务器需允许 TCP 转发。后台连接需要免交互的密钥认证；有口令的私钥先加载到 `ssh-agent`。首次连接需核对服务器指纹。详细步骤见 [ssh_config](ssh_config)。
- **代理就绪后再使用：** 等菜单显示 `running`，再设置客户端代理。默认 HTTP/HTTPS 为 `http://127.0.0.1:8181`，SOCKS5 为 `socks5h://127.0.0.1:1081`；程序不会自动修改系统代理。用启动脚本打开 Codex/Claude 前，先完全退出已运行的桌面应用。
- **退出时选择是否保留代理：** **Stop proxy and exit** 停止代理并退出；**Keep proxy running and exit** 保留代理、只退出托盘。代理未运行时用 **Exit app**。**Launch App at Login** 只启动托盘；要在托盘启动时自动连接，再勾选 **Start Proxy When App Launches**，无需托盘时可安装系统服务。
- **出错时查看日志：** 应用会显示错误并打开本次日志；也可选择 **Open Current Log**。所有运行日志位于解压目录的 `logs/`。

SSH Proxy 通过你的 SSH 服务器，在本机提供 HTTP/HTTPS 和 SOCKS5 代理。Windows、macOS 可使用托盘/菜单栏应用；Windows、macOS、Linux 都可使用命令行。服务器只需允许 SSH TCP 转发，无需安装 HTTP 代理。

### 1. 解压并打开应用

解压对应系统和架构的 `<os>-<arch>.zip` 到可写的固定目录即可使用，无需安装。`os` 为 `windows`、`darwin`（macOS）或 `linux`；`amd64` 对应 Intel/AMD 64 位，`arm64` 对应 Apple Silicon 等 ARM64 设备。Windows 要求 Windows 10 或更新版本；macOS 应用和 CLI 要求 macOS 13 或更新版本。

**Windows：** 双击解压目录中的 `ssh_proxy_app.exe`，在任务栏通知区域找到图标（可能在隐藏图标中）。

**macOS：** 双击 `ssh_proxy_app.app`，在菜单栏找到图标。应用没有主窗口，右键图标操作。

**Linux：** 没有托盘应用，填写下节的 SSH 配置后，按[命令行使用](#4-命令行使用windowsmacoslinux)启动。

| 文件 | 用途 |
| --- | --- |
| `ssh_proxy_app.exe` / `ssh_proxy_app.app` | Windows 托盘 / macOS 菜单栏应用，日常使用入口 |
| `ssh_proxy_cli.exe` / `ssh_proxy_cli` | 代理运行程序；应用会调用它，请保留 |
| [config.toml](config.toml) | 代理端口、SSH 超时、健康检查和日志设置 |
| [ssh_config](ssh_config) | 你的 SSH 连接信息，含免密码登录说明 |
| `scripts/`、`logs/` | 桌面启动示例、运行日志 |

保留整个目录；macOS 不要只把 `.app` 移走。需要 **OpenSSH 8.7 或更新版本**，未打包在内：先运行 `ssh -V` 检查。Windows 10 和较早的 Windows 11 内置版本可能过旧；可安装新版 [Win32-OpenSSH](https://github.com/PowerShell/Win32-OpenSSH/releases)，在 `config.toml` 的 `ssh.executable` 填写新客户端完整路径。macOS/Linux 使用满足版本要求的系统客户端。发行包未签名或公证；macOS 若阻止首次打开，可对你信任的下载在“系统设置 → 隐私与安全性”允许打开。

### 2. 配置 SSH 并通过托盘连接

首次打开应用时，缺失或无效的 SSH 配置会自动打开；也可从菜单选择 **Open SSH Config**。

编辑 `ssh_config`，取消示例前的 `#` 并填入自己的信息：

```sshconfig
Host my-proxy
    HostName ssh.example.com
    User my-user
    Port 22
    IdentityFile ~/.ssh/id_ed25519
```

可添加多个明确的 `Host` 别名，程序按文件顺序尝试，断线后尝试下一个。候选别名需直接写在此文件，`Host *` 不作为连接目标，无需在 TOML 中重复填写。使用默认密钥或 agent 时可省略 `IdentityFile`。

**后台运行需要免交互的密钥认证。** 按 `ssh_config` 中的说明配置公钥及 `ssh-agent`，有口令的私钥应先加载到 agent。OpenSSH 没有 `Password` 配置项。首次连接需向管理员核对服务器指纹；信任记录使用 OpenSSH 默认文件。设置终端不会自动安装公钥或保存密码。

保存文件后，右键图标选择 **Start proxy**。

每次菜单启动都会先在后台静默检查：主机已信任且免交互登录可用时，直接启动代理，不打开任何窗口。只有检查失败时才打开 SSH 设置终端（Windows 使用原生控制台，macOS 使用 Terminal，可能需允许 Automation（自动化）权限），按提示核对指纹并登录。终端关闭后会再静默检查一次，通过后才启动代理；仅密码登录成功不够，需配置密钥或 agent。失败或取消会显示原因并打开本次应用日志，代理保持停止，可修正后再次点击 **Start proxy**。

等待菜单顶部显示 **running**，即可使用代理。**Stop proxy** 同时停止 SSH 隧道和 HTTP 代理。退出时，**Stop proxy and exit** 先停止代理再关闭托盘；停止失败会显示错误并保留托盘。**Keep proxy running and exit** 只关闭托盘。代理未运行时 **Exit app** 直接退出，停止并退出选项不可用。

菜单显示状态、SSH 用户/主机/端口及代理地址，提供启动/停止、脚本和文件入口。配置、日志和目录使用系统默认应用打开。**Open Current Log** 打开本次托盘日志；代理日志用 CLI 的 `logs` 查看。

**Launch App at Login 只在登录时启动托盘，不自动连接代理。** Windows 可在任务管理器 → 启动应用，或设置 → 应用 → 启动查看（安装路径过长时可能显示为 Windows PowerShell）；macOS 可在系统设置 → 通用 → 登录项与扩展查看，可能列在后台项目中。菜单勾选表示已注册，不反映在系统设置中另外禁用的状态。

**Start Proxy When App Launches** 让托盘每次启动时自动启动代理，流程与 **Start proxy** 相同：需要时打开配置文件或 SSH 设置终端；代理已在运行时直接沿用。与 **Launch App at Login** 同时勾选即可在登录后自动连接；系统服务不受此设置影响。

### 3. 使用代理

| 客户端设置 | 默认地址 |
| --- | --- |
| HTTP / HTTPS 代理 | `http://127.0.0.1:8181` |
| SOCKS5，远端解析域名 | `socks5h://127.0.0.1:1081` |

两个代理均运行在本机，端口必须不同。HTTPS 客户端也使用上面的 `http://` 地址。SSH Proxy 不修改系统代理。在需要代理的应用设置中填写对应地址。

#### Codex / Claude 桌面应用

先安装相应桌面应用，**完全退出已运行的应用**。代理显示 `running` 后，在托盘菜单点击 **Run: Codex** 或 **Run: Claude**，即可通过 HTTP 代理启动。退出桌面应用不会停止代理。更多用法见[启动脚本](#6-启动脚本与自定义)。

#### 命令行客户端的代理环境变量

对于支持环境变量的客户端，设置以下变量，再从同一终端启动它：

```powershell
# Windows / PowerShell
$env:HTTP_PROXY = 'http://127.0.0.1:8181'
$env:HTTPS_PROXY = 'http://127.0.0.1:8181'
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
```

```bash
# macOS / Linux — bash 或 zsh
export HTTP_PROXY=http://127.0.0.1:8181 HTTPS_PROXY=http://127.0.0.1:8181
export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY"
export NO_PROXY=localhost,127.0.0.1,::1 no_proxy=localhost,127.0.0.1,::1
unset ALL_PROXY all_proxy
```

这也适用于读取这些变量的 Codex / Claude Code 命令行客户端。已运行的程序不会自动获得新变量。Linux 仅提供 CLI，使用上述设置或客户端自己的代理选项；桌面 Bash 脚本只用于 macOS。

### 4. 命令行使用（Windows/macOS/Linux）

在解压目录打开终端，首次依次执行：

```powershell
# Windows / PowerShell
.\ssh_proxy_cli.exe check
.\ssh_proxy_cli.exe trust
.\ssh_proxy_cli.exe start
.\ssh_proxy_cli.exe status
```

```bash
# macOS / Linux
./ssh_proxy_cli check
./ssh_proxy_cli trust
./ssh_proxy_cli start
./ssh_proxy_cli status
```

`check` 的 `valid` 应为 `true`；`status` 的 `phase` 为 `running` 表示转发就绪。`start` 默认等待最多 20 秒，返回时仍可能是 `connecting` / `reconnecting`，后台会继续重试。`trust my-proxy` 可设置指定主机；省略名称则按顺序尝试，首个成功后退出。

后台启动后可关闭终端，或选择 **Keep proxy running and exit** 退出托盘，代理会继续运行。使用 **Stop proxy** 或 CLI 的 `stop` 同时停止 SSH 隧道和 HTTP 代理。调试用 `start --foreground`，按 Ctrl+C 停止。

命令前加 Windows 的 `.\ssh_proxy_cli.exe` 或 macOS/Linux 的 `./ssh_proxy_cli`：

| 命令 | 用途 |
| --- | --- |
| `start` / `stop` / `restart` | 后台启动、停止、重新读取配置并重启 |
| `start --foreground` | 前台调试，Ctrl+C 停止 |
| `status` / `check` | JSON 运行状态 / 配置检查 |
| `status --proxy_http` / `status --proxy_socks` | 运行时仅输出 HTTP / SOCKS5 URL，否则输出为空；每次选择一种 |
| `trust [ENDPOINT_NAME]` | 交互式 SSH 登录及主机信任设置 |
| `trust --check [ENDPOINT_NAME]` | 只检查免交互登录；需要设置时失败退出，不显示提示或接受新主机密钥 |
| `logs --follow` | 持续查看代理日志；`--lines N` 指定初始行数，默认 60 |
| `test [--url URL]` | 经 HTTP 和 SOCKS5 请求网址，默认 `https://example.com/` |
| `--help` / `<command> --help` | 总览 / 单个命令帮助，`-h` 为短选项 |
| `-v` / `--version` | 查看版本 |

`--help` 在各命令旁列出专用选项；服务命令单独分组，详情见 `ssh_proxy_cli service --help`。长选项使用两个连字符，如 `--foreground`；带值选项支持空格或 `=`，如 `--wait 30` 和 `--wait=30`。

普通 `status`、`check` 输出 JSON，没有 `--json` 选项。`status --proxy_http` 返回 `http://...`，`status --proxy_socks` 返回 `socks5h://...`；只有 `phase` 为 `running` 才输出 URL，否则无输出且退出码为 0。`check` 报告 `valid: false` 时仍可返回退出码 0，自动化请读取 JSON。`start/restart --wait SECONDS` 仅改变命令等待就绪的时间；`trust --log FILE` 用于托盘记录设置诊断，通常无需手动指定。

### 5. 高级配置与排错

完整选项见 [config.toml](config.toml)，修改后用 `restart` 应用：

| TOML 设置 | 默认值与作用 |
| --- | --- |
| `proxy.bind` | `127.0.0.1`，只允许本机回环地址 |
| `proxy.socks_port` / `proxy.http_port` | `1081` / `8181`，不可相同 |
| `ssh.config_file` | `ssh_config`，相对路径以 TOML 所在目录为基准 |
| `health.targets` | `example.com:443`、`www.microsoft.com:443`；至少一个可经隧道连接才算就绪 |
| `health.failover_on_probe_failure` | `false`；检查失败显示 `degraded`，默认保留仍存活的 SSH |
| `logging.max_log_files` | `10`，至少为 1；托盘与代理各自保留此数量的最新日志 |
| `logging.max_size_mb` | `10`，范围 1..1024；单个日志达到上限时删除较早的一半内容 |

每次托盘/代理运行各写一个连续日志，错误追加到同一文件；文件达到 `max_size_mb` 时删除较早的一半并在开头标注。启动时各自清理旧日志，包含当前文件默认最多保留 10 个托盘日志和 10 个代理日志，位于 CLI 旁的 `logs/`。

仍被运行中进程使用的日志不会删除；数量可能暂时超过上限，后续启动时再清理。

| 问题 | 处理方法 |
| --- | --- |
| 配置无效 | 运行 `check`，根据 `error` 修改文件，再启动 |
| SSH 连接失败 | 检查网络/VPN、主机端口、密钥及 agent，用 `trust 主机别名` 检查登录 |
| `trust` 成功但后台认证失败 | 确认密钥认证无需提示；仅密码登录成功不够，详见 `ssh_config` |
| 端口占用 | 停止占用程序或修改 TOML 端口，同步更新客户端 |
| 一直连接中 / `degraded` | 查看代理日志；确认服务器允许转发，至少一个健康检查目标可达 |
| 客户端未走代理 | 确认客户端支持所设代理，完全退出后重新启动 |

SSH 退出会触发重连和备用主机尝试；切换会断开现有连接，客户端需重连，并在恢复后重试中断的请求。代理只转发 TCP，不提供 VPN 或 UDP 代理。

### 6. 启动脚本与自定义

自定义无需交互的启动脚本放在 `scripts/`：Windows 用 `.ps1`，macOS 用 `.sh`。重启托盘后出现 **Run: 文件名**，仅在代理为 `running` 时可用；输出写入托盘日志，失败会提示。

除了托盘菜单，也可在解压目录手动运行脚本。先安装桌面应用、完成 SSH 设置，并**完全退出已运行的桌面应用**：

```powershell
# Windows：选择其中一个
.\scripts\Codex.ps1
.\scripts\Claude.ps1
```

```bash
# 仅 macOS：选择其中一个
bash ./scripts/Codex.sh
bash ./scripts/Claude.sh
```

先启动代理，再运行脚本。每个脚本中的 `SSH_PROXY_URL` 默认为 `http://127.0.0.1:8181`；仅当 `status --proxy_http` 成功且返回非空 URL 时替换它，否则保留默认值。脚本只查询状态，然后带着代理参数和环境变量启动桌面应用。可关闭启动终端；退出桌面应用不会停止代理。代理运行时也可点击托盘中的 **Run: Codex** / **Run: Claude**。

脚本优先自动查找已安装的桌面应用：Windows 查找 Codex Store 包及 Claude 应用包或常见用户安装目录；macOS 在 `/Applications`、`~/Applications` 查找。找不到时使用脚本顶部的 `CODEX_APP` 或 `CLAUDE_APP`，默认为空字符串；可在脚本内填写备用可执行文件路径（macOS 也支持 `.app` 路径），无需设置环境变量。仍找不到时会报错。macOS 桌面输出保存在 `logs/codex_*.txt` / `logs/claude_*.txt`。脚本需留在原来的 `scripts/` 目录。

### 7. 可选系统服务

需要系统自动运行代理时才安装服务，普通后台使用无需服务。所有系统均以当前用户注册。先完成 `trust`、确认免交互认证可用，再停止已有代理并安装：

```powershell
# Windows：以自己的账户打开管理员 PowerShell
.\ssh_proxy_cli.exe stop
.\ssh_proxy_cli.exe service install
.\ssh_proxy_cli.exe start
.\ssh_proxy_cli.exe service status
```

```bash
# macOS / Linux：当前用户，不使用 sudo
./ssh_proxy_cli stop
./ssh_proxy_cli service install
./ssh_proxy_cli start
./ssh_proxy_cli service status
```

`service install` 注册并启用以后的自动启动，不立即连接。仍用普通 `start/stop/restart` 控制当前代理；`service enable/disable` 修改自动启动，`service status` 查看服务，`service uninstall` 停止并移除服务。更多信息用 `service --help` 查看。

Windows 的 install/uninstall/enable/disable 需管理员权限；安装询问当前 Windows 账户密码，供系统服务管理器保存并用于服务登录，不写入项目文件。账户需“作为服务登录”权限和密钥访问权限。Windows 服务在开机后启动；macOS 在登录时启动；Linux 使用 systemd 用户服务，在用户管理器启动时运行。

服务环境必须能访问免交互 SSH 凭据。Linux 如需继承 agent，执行 `systemctl --user import-environment SSH_AUTH_SOCK` 并确保密钥已加载。macOS 禁用服务后，手动启动前需先 `service enable`。托盘登录启动与代理服务自动启动互相独立。

---

## English guide

### Read before use

- **Upgrade or move:** Stop the proxy and exit the tray first. Back up `config.toml`, `ssh_config` and custom scripts. Preserve your settings and logs during upgrades; do not overwrite them with ZIP defaults. Before upgrading or moving, disable **Launch App at Login** and uninstall any registered OS service; re-enable or reinstall afterward.
- **Extract the whole ZIP into a writable, permanent folder:** Keep the app, CLI, configuration and `scripts/` together; do not move the macOS `.app` alone. Open `ssh_proxy_app` on Windows/macOS; use the CLI on Linux.
- **Prepare SSH first:** Local OpenSSH and server-side TCP forwarding are required. Background connections need noninteractive key authentication; load passphrase-protected keys into `ssh-agent`. Verify the server fingerprint on first connection. See [ssh_config](ssh_config) for setup instructions.
- **Wait for readiness:** Wait for `running` in the menu before configuring clients. Defaults are `http://127.0.0.1:8181` for HTTP/HTTPS and `socks5h://127.0.0.1:1081` for SOCKS5. System proxy settings are not changed automatically. Fully quit existing Codex/Claude desktop instances before using their launchers.
- **Choose whether to keep the proxy when exiting:** **Stop proxy and exit** stops it and closes the tray; **Keep proxy running and exit** closes only the tray. With no proxy running, choose **Exit app**. **Launch App at Login** opens only the tray; to connect when the tray starts, also check **Start Proxy When App Launches**, or install an OS service to run without the tray.
- **Read the log when something fails:** The app shows the error and opens the current run log; you can also choose **Open Current Log**. Run logs are under `logs/` in the extracted folder.

SSH Proxy provides local HTTP/HTTPS and SOCKS5 proxies through your SSH server. Windows and macOS have a tray/menu-bar app; Windows, macOS and Linux all have a CLI. The server only needs SSH TCP forwarding enabled, with no HTTP proxy installation.

### 1. Extract and open the app

Extract the matching `<os>-<arch>.zip` into a writable, permanent folder; no installation is required. OS names are `windows`, `darwin` (macOS) and `linux`; `amd64` means Intel/AMD 64-bit, and `arm64` includes Apple Silicon. Windows requires Windows 10 or later; both the macOS app and CLI require macOS 13 or later.

**Windows:** Double-click `ssh_proxy_app.exe` in the extracted folder. Find its icon in the taskbar notification area, possibly under hidden icons.

**macOS:** Double-click `ssh_proxy_app.app` and find its menu-bar icon. There is no main window; right-click the icon to use the menu.

**Linux:** There is no tray app. Fill in the SSH configuration below, then follow [CLI usage](#4-cli-usage-windowsmacoslinux).

| File | Purpose |
| --- | --- |
| `ssh_proxy_app.exe` / `ssh_proxy_app.app` | Windows tray / macOS menu-bar app: the everyday entry point |
| `ssh_proxy_cli.exe` / `ssh_proxy_cli` | Proxy runtime used by the app; keep it in the folder |
| [config.toml](config.toml) | Proxy ports, SSH timeouts, health checks and logging |
| [ssh_config](ssh_config) | Your SSH connection details, with passwordless login instructions |
| `scripts/`, `logs/` | Desktop launcher examples, run logs |

Keep the folder together; do not move the macOS `.app` alone. **OpenSSH 8.7 or newer is required** and is not bundled: check with `ssh -V`. Windows 10 and older Windows 11 installations may include an outdated client; install a newer [Win32-OpenSSH](https://github.com/PowerShell/Win32-OpenSSH/releases) and set `ssh.executable` in `config.toml` to its full path. On macOS/Linux use a system client meeting the version requirement. Packages are unsigned/not notarized; if macOS blocks a trusted download, allow it through System Settings → Privacy & Security.

### 2. Configure SSH and connect from the tray

On first launch, missing or invalid SSH configuration opens automatically. You can also choose **Open SSH Config** from the menu.

Edit `ssh_config`, uncomment the sample and enter your connection details:

```sshconfig
Host my-proxy
    HostName ssh.example.com
    User my-user
    Port 22
    IdentityFile ~/.ssh/id_ed25519
```

Add concrete `Host` aliases in preferred order; the proxy tries the next one after a disconnection. Declare candidates directly in this file; `Host *` is not a candidate. No duplicate list is needed in TOML. Omit `IdentityFile` when default keys or agent credentials suffice.

**Background operation requires noninteractive key authentication.** Follow the key and `ssh-agent` instructions in `ssh_config`; load passphrase-protected keys into the agent first. OpenSSH has no `Password` directive. Verify new host fingerprints with your server administrator; trust records use OpenSSH's default files. The setup terminal does not install keys or save passwords.

Save the file, then right-click the icon and choose **Start proxy**.

Each menu start first checks silently: with a trusted host and working noninteractive login, the proxy starts without opening any window. Only when that check fails does an SSH setup terminal open (a native console on Windows, Terminal on macOS, which may require Automation permission); verify the fingerprint and log in as prompted. After the terminal closes, the silent check runs again and the proxy starts only if it passes; a password login alone is not enough, so set up keys or an agent. Failure or cancellation shows the reason and opens this app run's log; the proxy stays stopped until you correct the problem and choose **Start proxy** again.

Wait for **running** at the top of the menu before using the proxy. **Stop proxy** stops both SSH and HTTP forwarding. **Stop proxy and exit** stops the proxy before closing the tray; a stop failure shows an error and keeps the app open. **Keep proxy running and exit** closes only the tray. With no proxy running, **Exit app** closes immediately and the stop-and-exit option is disabled.

The menu shows status, SSH user/host/port and proxy addresses, with proxy controls, scripts and file actions. Configs, logs and the folder open in default applications. **Open Current Log** opens this tray run's log; use CLI `logs` for proxy logs.

**Launch App at Login starts the tray only; it does not connect the proxy.** On Windows, view it in Task Manager → Startup apps or Settings → Apps → Startup (a very long installation path may make it appear as Windows PowerShell). On macOS, check System Settings → General → Login Items & Extensions, possibly among background items. The menu checkmark means the entry is registered; it does not reflect separate disablement in OS settings.

**Start Proxy When App Launches** starts the proxy whenever the tray starts, using the same flow as **Start proxy**: it opens the config file or an SSH setup terminal when needed and reuses a proxy that is already running. Check it together with **Launch App at Login** to connect automatically after sign-in. OS services ignore this setting.

### 3. Use the proxy

| Client setting | Default address |
| --- | --- |
| HTTP / HTTPS proxy | `http://127.0.0.1:8181` |
| SOCKS5 with remote DNS | `socks5h://127.0.0.1:1081` |

Both proxies run locally and require different ports. HTTPS clients also use the `http://` address above. SSH Proxy does not change system proxy settings. Enter the appropriate address in the app you want to proxy.

#### Codex / Claude desktop apps

Install the desktop app and **fully quit any running instance**. Once the proxy is `running`, choose **Run: Codex** or **Run: Claude** from the tray menu to launch it through the HTTP proxy. Exiting the desktop app leaves the proxy running. See [launcher scripts](#6-launcher-scripts-and-customization) for more usage details.

#### Proxy environment variables for command-line clients

For clients that support environment variables, set these and launch the client from the same terminal:

```powershell
# Windows / PowerShell
$env:HTTP_PROXY = 'http://127.0.0.1:8181'
$env:HTTPS_PROXY = 'http://127.0.0.1:8181'
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
```

```bash
# macOS / Linux — bash or zsh
export HTTP_PROXY=http://127.0.0.1:8181 HTTPS_PROXY=http://127.0.0.1:8181
export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY"
export NO_PROXY=localhost,127.0.0.1,::1 no_proxy=localhost,127.0.0.1,::1
unset ALL_PROXY all_proxy
```

This also applies to Codex / Claude Code command-line clients that read these variables. Existing processes do not inherit new variables. Linux provides the CLI only: use these settings or your client's proxy options. Desktop Bash launchers are macOS-only.

### 4. CLI usage (Windows/macOS/Linux)

Open a terminal in the extracted folder and, on first use, run:

```powershell
# Windows / PowerShell
.\ssh_proxy_cli.exe check
.\ssh_proxy_cli.exe trust
.\ssh_proxy_cli.exe start
.\ssh_proxy_cli.exe status
```

```bash
# macOS / Linux
./ssh_proxy_cli check
./ssh_proxy_cli trust
./ssh_proxy_cli start
./ssh_proxy_cli status
```

Check that `check` reports `valid: true` and `status` reports `phase: running` for ready forwarding. `start` waits up to 20 seconds by default; it can return while still `connecting` / `reconnecting`, with background retries continuing. Use `trust my-proxy` for one host; without a name it tries hosts until one succeeds.

After a background start, you may close the terminal or choose **Keep proxy running and exit** in the tray; the proxy keeps running. Use **Stop proxy** or CLI `stop` to stop both SSH and HTTP forwarding. For debugging, use `start --foreground`; Ctrl+C stops it.

Prefix commands with `.\ssh_proxy_cli.exe` on Windows or `./ssh_proxy_cli` on macOS/Linux:

| Command | Purpose |
| --- | --- |
| `start` / `stop` / `restart` | Start in background, stop, or restart with current configuration |
| `start --foreground` | Debug in this terminal; Ctrl+C stops |
| `status` / `check` | JSON runtime state / configuration validation |
| `status --proxy_http` / `status --proxy_socks` | Print only the HTTP / SOCKS5 URL when running, otherwise nothing; choose one flag |
| `trust [ENDPOINT_NAME]` | Interactive SSH login and host-key setup |
| `trust --check [ENDPOINT_NAME]` | Check unattended login; fail if setup is needed, without prompts or accepting new host keys |
| `logs --follow` | Follow proxy logs; `--lines N` selects initial lines, default 60 |
| `test [--url URL]` | Fetch through HTTP and SOCKS5; default `https://example.com/` |
| `--help` / `<command> --help` | Overview / command-specific help; `-h` is the short form |
| `-v` / `--version` | Print the version |

`--help` lists each command's own flags and groups service commands separately; use `ssh_proxy_cli service --help` for service details. Long options use two dashes, such as `--foreground`. Options with values accept a space or `=`, such as `--wait 30` and `--wait=30`.

Plain `status` and `check` output JSON, with no `--json` switch. `status --proxy_http` returns `http://...`; `status --proxy_socks` returns `socks5h://...`. These print a URL only for `phase: running`, otherwise nothing with exit code 0. `check` can exit 0 with `valid: false`, so automation must read its JSON. `start/restart --wait SECONDS` changes only the command's readiness wait. `trust --log FILE` records setup diagnostics for the tray and is normally unnecessary by hand.

### 5. Advanced settings and troubleshooting

See [config.toml](config.toml) for all settings; run `restart` after saving changes:

| TOML setting | Default and purpose |
| --- | --- |
| `proxy.bind` | `127.0.0.1`; only numeric loopback addresses are allowed |
| `proxy.socks_port` / `proxy.http_port` | `1081` / `8181`; must differ |
| `ssh.config_file` | `ssh_config`; relative to the TOML directory |
| `health.targets` | `example.com:443`, `www.microsoft.com:443`; at least one must be reachable through SSH for readiness |
| `health.failover_on_probe_failure` | `false`; failed checks report `degraded` and preserve a live SSH session by default |
| `logging.max_log_files` | `10`, minimum 1; separate retention limits for tray and proxy logs |
| `logging.max_size_mb` | `10`, range 1..1024; at the limit a log's older half is removed |

Each tray/proxy run writes one continuous log; errors append to it. When a file reaches `max_size_mb`, its older half is removed and the cut is marked at the top. At startup each cleans up its oldest files, keeping at most 10 tray logs and 10 proxy logs by default, including current files, in `logs/` beside the CLI.

Logs still used by running processes are protected; retention may temporarily exceed the limit until a later startup can remove them.

| Problem | Action |
| --- | --- |
| Invalid configuration | Run `check`, fix its `error`, then start |
| SSH connection fails | Check network/VPN access, host/port, keys and agent; try `trust host-alias` |
| `trust` works but background authentication fails | Confirm key login works without prompts; password login alone is insufficient. See `ssh_config` |
| Port in use | Stop the conflicting process or change TOML ports and client settings |
| Stuck connecting / `degraded` | Read proxy logs; check server forwarding permissions and reachability of at least one health target |
| Client bypasses proxy | Confirm proxy support and fully restart the client with the new settings |

SSH exits trigger reconnection and attempts at backup hosts. Switching closes existing connections; clients must reconnect and retry interrupted requests afterward. This is TCP proxying, with no VPN or UDP proxy support.

### 6. Launcher scripts and customization

Add noninteractive launchers to `scripts/`: `.ps1` on Windows, `.sh` on macOS. Restart the tray to discover them as **Run: filename**, enabled only in the `running` state. Output joins the tray log; failures show an error.

You can also run launchers manually from the extracted folder. Install the desktop app, complete SSH setup and **fully quit any running instance** first:

```powershell
# Windows: choose one
.\scripts\Codex.ps1
.\scripts\Claude.ps1
```

```bash
# macOS only: choose one
bash ./scripts/Codex.sh
bash ./scripts/Claude.sh
```

Start the proxy before launching an app. Each script defaults `SSH_PROXY_URL` to `http://127.0.0.1:8181` and replaces it only when `status --proxy_http` succeeds with a nonempty URL; otherwise it keeps the default. Scripts only query status, then launch the desktop app with proxy arguments and environment variables. You may close the launch terminal; exiting the desktop app leaves the proxy running. The tray's **Run: Codex** / **Run: Claude** entries are also available while the proxy is running.

Scripts first find the installed desktop app automatically: Windows searches the Codex Store package and the Claude app package or common per-user installations; macOS searches `/Applications` and `~/Applications`. If none is found, they use `CODEX_APP` or `CLAUDE_APP` at the top of the script, which defaults to an empty string. Edit it inside the script to supply a fallback executable path (or macOS `.app` path); no environment setup is needed. If no app is available, the script reports an error. macOS desktop output goes to `logs/codex_*.txt` / `logs/claude_*.txt`. Keep launchers in their original `scripts/` folder.

### 7. Optional OS service

Install a service when you want the OS to run the proxy automatically; ordinary background use needs none. Registration uses the current user everywhere. Complete `trust`, confirm noninteractive authentication, then stop the existing proxy and install:

```powershell
# Windows: elevated PowerShell under your own account
.\ssh_proxy_cli.exe stop
.\ssh_proxy_cli.exe service install
.\ssh_proxy_cli.exe start
.\ssh_proxy_cli.exe service status
```

```bash
# macOS / Linux: current user, without sudo
./ssh_proxy_cli stop
./ssh_proxy_cli service install
./ssh_proxy_cli start
./ssh_proxy_cli service status
```

`service install` registers and enables future automatic startup without connecting now. Use ordinary `start/stop/restart` to control the current proxy; `service enable/disable` changes automatic startup, `service status` inspects the service, and `service uninstall` stops and removes it. See `service --help` for details.

Windows install/uninstall/enable/disable require elevation. Installation asks for your Windows account password so the Service Control Manager can store it for service logon; it is not written to project files. The account needs “Log on as a service” and key access. Windows starts the service after boot; macOS at login; Linux uses a systemd user service, started with the user manager.

The service environment needs noninteractive SSH credentials. On Linux, use `systemctl --user import-environment SSH_AUTH_SOCK` if you need the current agent, and ensure keys are loaded. On macOS, run `service enable` before manually starting a disabled service. Tray login startup and proxy-service startup are independent.

