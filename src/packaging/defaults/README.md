# SSH Proxy

[中文使用指南](#中文使用指南) · [English guide](#english-guide)

## 中文使用指南

### 使用前必读

- **升级：** 停止代理并退出托盘，把新版 ZIP 解压到原目录、覆盖已有文件即可。你的 `config.toml`、`ssh_config` 和日志不会被覆盖；新版本新增的设置会在下次启动时自动加入 `config.toml`（文件中的注释可能会被移除）。
- **移动目录：** 托盘应用先取消已启用的 **Launch App at Login**，然后停止代理并退出托盘；CLI 用户如果已启用自启动，需要先运行 `autostart disable`，再停止代理。移动后按需重新启动并恢复自启动。
- **完整解压到可写的固定目录：** 应用、命令行、配置和 `scripts/` 保持在一起；macOS 不要单独移动 `.app`，首次使用前先运行 `run_on_mac.sh`（见第 1 节）。
- **先准备 SSH：** 需要 OpenSSH 8.7 或更新版本，服务器需允许 TCP 转发。代理在后台运行，必须能免密码自动登录，`login`（或托盘的 **Start proxy**）会自动设置。首次连接向管理员核对服务器指纹。说明见 [ssh_config](support/ssh_config.in)。
- **显示 running 后再使用：** HTTP/HTTPS 代理为 `http://127.0.0.1:8181`，SOCKS5 为 `socks5h://127.0.0.1:1081`。程序不会修改系统代理，需在应用中自行填写。用脚本启动 Codex/Claude 前，先完全退出已运行的桌面应用。
- **退出时选择是否保留代理：** **Exit app: Stop proxy** 停止代理并退出；**Exit app: Keep proxy running** 只退出托盘，代理继续运行。
- **出错时看日志：** 应用会显示原因；日志在安装目录的 `logs/`。

SSH Proxy 通过你的 SSH 服务器，在本机提供 HTTP/HTTPS 代理和 SOCKS5 代理。Windows 和 macOS 有托盘（菜单栏）应用；Windows、macOS、Linux 都可用命令行。服务器需允许 SSH TCP 转发；登录设置和登录检查会在服务器上执行命令，所以账户需要能登录 shell。

### 1. 准备与安装

- **系统：** Windows 10 或更新；macOS 13 或更新；Linux 仅命令行。
- **OpenSSH 8.7 或更新：** 未打包在内，用 `ssh -V` 检查。Windows 10 和较早 Windows 11 自带的版本可能过旧，可安装新版 [Win32-OpenSSH](https://github.com/PowerShell/Win32-OpenSSH/releases)，并在 `config.toml` 的 `ssh.executable` 填写其完整路径。
- **安装：** 把对应的 `ssh-proxy-<系统>-<架构>.zip` 完整解压到可写的固定目录（`windows`/`darwin`/`linux`；`amd64` 为 Intel/AMD，`arm64` 为 Apple Silicon 等 ARM）。所有文件保持在同一目录；macOS 不要单独移动 `.app`。
- **macOS 首次使用前运行 `run_on_mac.sh`：** 程序未签名且未经公证，macOS 会阻止从网络下载的文件。打开“终端”，输入 `bash `（末尾有一个空格），把解压目录中的 `run_on_mac.sh` 拖入终端窗口，按回车。每次下载新版本后都要再运行一次。

| 文件 | 用途 |
| --- | --- |
| `ssh-proxy-app.exe` / `ssh-proxy-app.app` | 托盘 / 菜单栏应用，日常使用入口 |
| `ssh-proxy-cli.exe` / `ssh-proxy-cli` | 命令行和代理程序，应用依赖它，请保留 |
| `ssh_config` | 你的 SSH 服务器信息，含免密码登录步骤；首次启动时从 `support/` 创建 |
| `config.toml` | 端口、超时、健康检查和日志设置；首次启动时从 `support/` 创建 |
| `support/` | 默认配置模板（`*.in`）、版本信息和许可证（Apache 2.0），升级时会被替换；请修改上面两个文件，不要改这里 |
| `run_on_mac.sh` | 仅 macOS：首次使用前解除下载限制 |
| `scripts/`、`logs/` | Codex/Claude 启动脚本、运行日志 |

解压即可使用，无需安装器、Go 或 Python。Linux 用户先按下例填写 `ssh_config`，再看第 4 节命令行操作。

### 2. 打开应用并连接（Windows / macOS）

打开 `ssh-proxy-app`：Windows 在任务栏通知区域（可能在隐藏图标中），macOS 在菜单栏。右键图标打开菜单。还没有填写 SSH 服务器时（例如首次打开），会自动打开 `ssh_config` 供你填写，不会弹出错误；文件开头说明了为什么打开它。SSH 配置有错误时，会显示原因并打开该文件。

编辑安装目录中的 `ssh_config`（首次打开应用或运行 `check` 时自动创建），取消示例前的 `#` 并填写自己的信息：

```sshconfig
Host my-proxy
    HostName ssh.example.com
    User my-user
    Port 22
```

- 可写多个 `Host`，按文件顺序连接，断线后尝试下一个。`Host *` 不算连接目标。
- **代理在后台运行，需要免密码登录。** 保存后在托盘点 **Start proxy** 或运行 `login`：没有 SSH 密钥时自动创建，输入一次服务器密码即把公钥加到服务器，以后不再需要密码。服务器不允许密码登录时，把显示的公钥发给管理员。
- 首次连接需向管理员核对服务器指纹。

1. 选择 **Start proxy**。若登录已可免交互，代理直接启动；SSH 报告需要设置登录（密钥未被接受，或服务器指纹尚未确认）时，会打开 SSH 设置终端（macOS 使用“终端”，可能需允许“自动化”权限），它会在需要时创建密钥，请按提示核对指纹并输入一次服务器密码。设置成功后终端自动关闭并再检查一次，通过才启动；设置失败时终端保持打开，便于查看原因（Windows 按 Enter 关闭）。网络或 VPN 尚未连接时，代理直接启动并在后台重试，连通后自动连接。
2. 菜单顶部显示 **running** 后即可使用。菜单还显示 SSH 主机和两个代理地址。
3. **Stop proxy** 停止代理。

| 菜单项 | 作用 |
| --- | --- |
| **Run: Codex** / **Run: Claude** | 通过代理启动对应桌面应用（代理运行时可用） |
| **Launch App at Login** | 登录时启动托盘（不自动连接） |
| **Start Proxy When App Launches** | 托盘启动时自动启动代理；与上一项同时勾选即可登录后自动连接 |
| **Open SSH Config** / **Open App Config** | 编辑 `ssh_config` / `config.toml` |
| **Open Current Log** / **Open Installed Location** | 打开本次托盘日志 / 安装目录 |
| **Exit app: Stop proxy** | 停止代理并退出托盘 |
| **Exit app: Keep proxy running** | 只退出托盘，代理继续运行 |
| **Exit app** | 代理未运行时，直接退出托盘 |

配置和日志均以文本打开。操作失败或配置有误时会显示原因，关闭提示后打开本次托盘日志；设置失败需修正后再次选择 **Start proxy**。代理自动重连期间的错误不弹窗，显示在托盘图标的提示文字中。托盘观察到代理意外停止时，会提示并打开代理日志。

登录启动可在 Windows“任务管理器 → 启动应用”或 macOS“系统设置 → 通用 → 登录项与扩展”查看。菜单勾选表示已注册，不反映这些系统设置中的额外禁用。

### 3. 在应用中使用代理

| 客户端设置 | 默认地址 |
| --- | --- |
| HTTP / HTTPS 代理 | `http://127.0.0.1:8181` |
| SOCKS5（远端解析域名） | `socks5h://127.0.0.1:1081` |

SSH Proxy 不修改系统代理，请在需要代理的应用中填写上面的地址；HTTPS 也使用 `http://` 地址。

**Codex / Claude 桌面应用：** 安装后先**完全退出**正在运行的桌面应用，代理显示 running 后在托盘选择 **Run: Codex** 或 **Run: Claude**。也可在安装目录运行 `.\scripts\Codex.ps1` / `.\scripts\Claude.ps1`（Windows）或 `bash ./scripts/Codex.sh` / `bash ./scripts/Claude.sh`（macOS）。这些桌面脚本不支持 Linux，Linux 客户端使用下方环境变量。脚本使用正在运行的 HTTP 代理地址，默认 `http://127.0.0.1:8181`。脚本会自动查找已安装的应用；找不到时，先把脚本复制为新文件名（如 `scripts/MyCodex.ps1`），再在副本顶部的 `CODEX_APP` / `CLAUDE_APP` 填写可执行文件路径（macOS 也可填 `.app` 路径）；升级会替换自带脚本，你的副本会保留。退出桌面应用不会停止代理。

**命令行客户端：** 设置环境变量后，从同一终端启动客户端：

```powershell
# Windows PowerShell
$env:HTTP_PROXY = 'http://127.0.0.1:8181'
$env:HTTPS_PROXY = 'http://127.0.0.1:8181'
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
```

```bash
# macOS / Linux（bash 或 zsh）
export HTTP_PROXY=http://127.0.0.1:8181 HTTPS_PROXY=http://127.0.0.1:8181
export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY"
export NO_PROXY=localhost,127.0.0.1,::1 no_proxy=localhost,127.0.0.1,::1
unset ALL_PROXY all_proxy
```

### 4. 命令行（Windows / macOS / Linux）

在安装目录打开终端。Windows 命令前用 `.\ssh-proxy-cli.exe`，macOS/Linux 用 `./ssh-proxy-cli`。首次使用：

```powershell
# Windows PowerShell
.\ssh-proxy-cli.exe check
.\ssh-proxy-cli.exe login
.\ssh-proxy-cli.exe start
.\ssh-proxy-cli.exe status
```

```bash
# macOS / Linux
./ssh-proxy-cli check     # valid 应为 true
./ssh-proxy-cli login     # 设置免密码登录：核对指纹、输入一次服务器密码
./ssh-proxy-cli start     # 后台启动；可关闭终端
./ssh-proxy-cli status    # phase 为 running 即就绪
```

| 命令 | 用途 |
| --- | --- |
| `start` / `stop` / `restart` | 后台启动 / 停止 / 按当前配置重启 |
| `start --foreground` | 在当前终端运行，Ctrl+C 停止 |
| `status` | 运行状态（JSON），含错误原因和 `autostart` |
| `status --proxy_http` / `--proxy_socks` | 运行时只输出代理 URL，否则无输出 |
| `check` | 检查配置（JSON） |
| `login [别名]` | 设置免密码登录（如 `login my-proxy`）：需要时创建密钥，把公钥加到服务器并检查；已完成的步骤会跳过。`--check` 只检查 |
| `logs [--follow] [--lines N]` | 查看代理日志 |
| `test [--url URL]` | 经两个代理请求网址测试 |
| `autostart enable` / `disable` / `status` | 登录时自动启动代理（见下节） |
| `--help`、`<命令> --help`、`--version` | 帮助与版本 |

`start` 默认等待 20 秒就绪（`--wait` 可调整）；启动准备较慢时最多额外等待 30 秒。返回时可能仍在连接，后台会继续重试，以 `status` 的 `phase` 为准。

### 5. 不用托盘，登录时自动启动

Windows 将下方 `./ssh-proxy-cli` 换成 `.\ssh-proxy-cli.exe`。

```bash
./ssh-proxy-cli autostart enable    # 以后登录时自动启动代理
./ssh-proxy-cli autostart status
./ssh-proxy-cli autostart disable   # 取消，不影响当前运行的代理
```

- 无需管理员权限或 sudo；`enable` 不会立即启动代理，现在要用就运行 `start`。
- 需要免交互的 SSH 登录；`enable` 检查失败只会警告，仍会保存设置。
- 可与托盘同时使用，不会产生两个代理。
- Windows 登录时会短暂出现命令行窗口。Linux 需要 systemd 用户会话。代理在桌面会话之前启动，用不到之后才启动的 ssh-agent（`systemctl --user import-environment SSH_AUTH_SOCK` 只对本次登录有效）：请使用 `login` 创建的无口令密钥，或在 `~/.config/environment.d/` 中把 `SSH_AUTH_SOCK` 设为登录时已存在的 agent 套接字。
- 在用户登录后运行，不在登录前启动；代理崩溃不会自动重启。用 `status` 查看原因，再运行 `start`。

### 6. 设置与日志

编辑 `config.toml` 后运行 `restart`（或在托盘中停止再启动）：

| 设置 | 默认值与作用 |
| --- | --- |
| `proxy.bind` | `127.0.0.1`，仅本机 |
| `proxy.http_port` / `proxy.socks_port` | `8181` / `1081`，不可相同 |
| `ssh.executable` | `ssh`，可填 OpenSSH 完整路径 |
| `ssh.config_file` | `ssh_config` |
| `health.targets` | 至少一个可经隧道访问才算就绪 |
| `health.failover_on_probe_failure` | `false`：检查失败只显示 `degraded`，不切换主机 |
| `logging.max_log_files` / `logging.max_size_mb` | 托盘和代理各保留 `10` 个日志 / 每个最大 `10` MB |

每次托盘或代理运行各写一个连续日志；达到大小限制时删除较旧的一半内容。日志在安装目录的 `logs/`。托盘的 **Open Current Log** 打开托盘日志；代理日志用 `logs` 命令或 `status` 中的日志路径查看。

### 7. 常见问题

| 问题 | 处理 |
| --- | --- |
| 配置无效 | 运行 `check`，按 `error` 修改 |
| SSH 连接失败 | 检查网络/VPN、主机和端口、密钥及 agent；运行 `login my-proxy` |
| 服务器不允许密码登录 | 把 `login` 显示的公钥（`ssh-ed25519` 开头）发给管理员，加好后再运行 `login` |
| 端口被占用 | 关闭占用程序，或修改端口并同步更新客户端 |
| 一直连接中 / `degraded` | 查看代理日志；确认服务器允许转发、健康检查目标可达 |
| 提示 OpenSSH 版本过旧 | 安装 OpenSSH 8.7 或更新版本（见第 1 节） |
| 客户端没有走代理 | 确认已填写代理地址，并完全重启该客户端 |
| macOS 无法打开 SSH 设置终端 | 在系统设置 → 隐私与安全性 → 自动化中允许 SSH Proxy 控制“终端” |

切换主机时现有连接会断开，客户端重连即可。仅转发 TCP，不提供 VPN 或 UDP。

### 8. 自定义脚本

把无需交互的脚本放入 `scripts/`（Windows 用 `.ps1`，macOS 用 `.sh`），重启托盘后会出现 **Run: 文件名**，代理运行时可用，输出写入托盘日志。脚本需保留在 `scripts/` 目录中。

---

## English guide

### Read before use

- **Upgrade:** stop the proxy and quit the tray, then unzip the new version into the same folder and replace the existing files. Your `config.toml`, `ssh_config` and logs are not overwritten; settings added by the new version are added to `config.toml` the next time it starts (comments in that file may be removed).
- **Move the folder:** in the tray app, turn off **Launch App at Login** if enabled, then stop the proxy and quit the tray. CLI users with autostart enabled should run `autostart disable` before stopping the proxy. After moving, restart and re-enable login startup as needed.
- **Extract the whole ZIP into a writable, permanent folder:** keep the app, command line, configuration and `scripts/` together; don't move the macOS `.app` on its own. On macOS, run `run_on_mac.sh` before first use (see section 1).
- **Prepare SSH first:** OpenSSH 8.7 or later is required, and the server must allow TCP forwarding. The proxy runs in the background, so it must log in without a password; `login` (or **Start proxy** in the tray) sets that up for you. Verify the server fingerprint with your administrator on first connection. See [ssh_config](support/ssh_config.in).
- **Use the proxy once it shows running:** HTTP/HTTPS is `http://127.0.0.1:8181` and SOCKS5 is `socks5h://127.0.0.1:1081`. System proxy settings aren't changed; set the address in each app. Fully quit Codex/Claude before starting them with the launchers.
- **Choose what happens when you exit:** **Exit app: Stop proxy** stops the proxy and quits; **Exit app: Keep proxy running** closes only the tray and leaves the proxy running.
- **When something fails, read the log:** the app shows the reason; logs are in `logs/` in the install folder.

SSH Proxy provides local HTTP/HTTPS and SOCKS5 proxies through your SSH server. Windows and macOS have a tray (menu-bar) app; Windows, macOS and Linux all have a command line. The server must allow SSH TCP forwarding; login setup and its check run a command on the server, so your account needs a normal shell.

### 1. Requirements and installation

- **OS:** Windows 10 or later; macOS 13 or later; Linux (command line only).
- **OpenSSH 8.7 or later:** not bundled; check with `ssh -V`. Windows 10 and older Windows 11 may include an outdated client. Install a newer [Win32-OpenSSH](https://github.com/PowerShell/Win32-OpenSSH/releases) and set its full path as `ssh.executable` in `config.toml`.
- **Install:** extract the whole `ssh-proxy-<os>-<arch>.zip` (`windows`/`darwin`/`linux`; `amd64` for Intel/AMD, `arm64` for Apple Silicon and other ARM) into a writable, permanent folder. Keep all files together; don't move the macOS `.app` on its own.
- **On macOS, run `run_on_mac.sh` before first use:** the programs are unsigned and not notarized, so macOS blocks them when downloaded. Open Terminal, type `bash ` (with a space at the end), drag `run_on_mac.sh` from the extracted folder into the Terminal window, and press Return. Run it again after each new download.

| File | Purpose |
| --- | --- |
| `ssh-proxy-app.exe` / `ssh-proxy-app.app` | Tray / menu-bar app, the everyday entry point |
| `ssh-proxy-cli.exe` / `ssh-proxy-cli` | Command line and proxy runtime; the app needs it |
| `ssh_config` | Your SSH servers, with passwordless-login steps; created from `support/` on first start |
| `config.toml` | Ports, timeouts, health checks and logging; created from `support/` on first start |
| `support/` | Default configuration templates (`*.in`), build information and the license (Apache 2.0), replaced on upgrade; edit the two files above, not these |
| `run_on_mac.sh` | macOS only: removes the download block before first use |
| `scripts/`, `logs/` | Codex/Claude launchers, run logs |

No installer, Go or Python is needed. On Linux, fill in `ssh_config` using the example below, then follow section 4 for CLI usage.

### 2. Open the app and connect (Windows / macOS)

Open `ssh-proxy-app`: its icon is in the Windows notification area (maybe under hidden icons) or the macOS menu bar. Right-click it for the menu. While no SSH server is set up yet (as on first launch), `ssh_config` opens for you to fill in, with no error message; its first lines explain why it opened. If the SSH config has a mistake, the app shows the reason and opens the file.

Edit `ssh_config` in the install folder (created the first time you open the app or run `check`), uncomment the example and enter your details:

```sshconfig
Host my-proxy
    HostName ssh.example.com
    User my-user
    Port 22
```

- Add several `Host` entries to have backups: they're tried in file order, and the next one is used after a disconnect. `Host *` isn't a connection target.
- **The proxy runs in the background, so it needs passwordless login.** After saving, click **Start proxy** in the tray or run `login`: it creates an SSH key if you have none, and after you enter your server password once it adds your public key to the server, so no password is needed after that. If the server doesn't allow password login, send the public key it prints to your administrator.
- Verify the server fingerprint with your administrator on first connection.

1. Choose **Start proxy**. If login already works without prompts, the proxy starts. If SSH reports that login needs setting up (your key isn't accepted, or the server fingerprint isn't confirmed yet), an SSH setup terminal opens (Terminal on macOS, which may ask for Automation permission); it creates a key if needed; verify the fingerprint and enter your server password once. After a successful setup the terminal closes, login is checked again and the proxy starts only if it passes. A failed setup keeps the terminal open so you can read why (on Windows, press Enter to close it). If the network or VPN isn't up yet, the proxy starts anyway and keeps retrying until it connects.
2. Use the proxy once the menu shows **running**. The menu also shows the SSH host and both proxy addresses.
3. **Stop proxy** stops it.

| Menu item | What it does |
| --- | --- |
| **Run: Codex** / **Run: Claude** | Start that desktop app through the proxy (while running) |
| **Launch App at Login** | Open the tray at login (doesn't connect) |
| **Start Proxy When App Launches** | Start the proxy when the tray opens; with the item above, connects at login |
| **Open SSH Config** / **Open App Config** | Edit `ssh_config` / `config.toml` |
| **Open Current Log** / **Open Installed Location** | This tray run's log / the install folder |
| **Exit app: Stop proxy** | Stop the proxy and close the tray |
| **Exit app: Keep proxy running** | Close only the tray |
| **Exit app** | Close the tray when the proxy is stopped |

Configs and logs open as text. When an action fails or the configuration is wrong, the app shows the reason and opens the tray run log after you dismiss the message. After a setup failure, fix the problem and choose **Start proxy** again. While the proxy reconnects on its own, its errors appear in the tray icon's tooltip instead of a message. If the tray observes the proxy stopping unexpectedly, it alerts you and opens the proxy log.

View login startup in Windows Task Manager → Startup apps or macOS System Settings → General → Login Items & Extensions. The menu checkmark means registered; it does not reflect additional disabling in those system settings.

### 3. Use the proxy in your apps

| Client setting | Default address |
| --- | --- |
| HTTP / HTTPS proxy | `http://127.0.0.1:8181` |
| SOCKS5 (remote DNS) | `socks5h://127.0.0.1:1081` |

SSH Proxy doesn't change system proxy settings; enter these addresses in the apps you want to proxy. HTTPS also uses the `http://` address.

**Codex / Claude desktop apps:** install the app and **fully quit** any running instance. Once the proxy is running, choose **Run: Codex** or **Run: Claude** in the tray. From the install folder, you can also run `.\scripts\Codex.ps1` / `.\scripts\Claude.ps1` on Windows or `bash ./scripts/Codex.sh` / `bash ./scripts/Claude.sh` on macOS. These desktop scripts do not support Linux; use the environment variables below for Linux clients. They use the running HTTP proxy address, defaulting to `http://127.0.0.1:8181`, and find the installed app automatically; if discovery fails, copy the script to a new name (for example `scripts/MyCodex.ps1`) and set `CODEX_APP` / `CLAUDE_APP` at the top of the copy to the executable path (or a `.app` path on macOS). Upgrades replace the bundled scripts but keep your copy. Quitting the desktop app leaves the proxy running.

**Command-line clients:** set these variables, then start the client from the same terminal:

```powershell
# Windows PowerShell
$env:HTTP_PROXY = 'http://127.0.0.1:8181'
$env:HTTPS_PROXY = 'http://127.0.0.1:8181'
$env:NO_PROXY = 'localhost,127.0.0.1,::1'
Remove-Item Env:ALL_PROXY -ErrorAction SilentlyContinue
```

```bash
# macOS / Linux (bash or zsh)
export HTTP_PROXY=http://127.0.0.1:8181 HTTPS_PROXY=http://127.0.0.1:8181
export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY"
export NO_PROXY=localhost,127.0.0.1,::1 no_proxy=localhost,127.0.0.1,::1
unset ALL_PROXY all_proxy
```

### 4. Command line (Windows / macOS / Linux)

Open a terminal in the install folder. Use `.\ssh-proxy-cli.exe` on Windows and `./ssh-proxy-cli` on macOS/Linux. First run:

```powershell
# Windows PowerShell
.\ssh-proxy-cli.exe check
.\ssh-proxy-cli.exe login
.\ssh-proxy-cli.exe start
.\ssh-proxy-cli.exe status
```

```bash
# macOS / Linux
./ssh-proxy-cli check     # valid should be true
./ssh-proxy-cli login     # set up passwordless login: verify the fingerprint, enter the server password once
./ssh-proxy-cli start     # starts in the background; you can close the terminal
./ssh-proxy-cli status    # phase "running" means ready
```

| Command | Purpose |
| --- | --- |
| `start` / `stop` / `restart` | Start in the background / stop / restart with the current config |
| `start --foreground` | Run in this terminal; Ctrl+C stops |
| `status` | Status as JSON, including any error and `autostart` |
| `status --proxy_http` / `--proxy_socks` | Print only the proxy URL while running, otherwise nothing |
| `check` | Check the configuration (JSON) |
| `login [alias]` | Set up passwordless login (e.g. `login my-proxy`): creates a key if needed, adds it to the server and checks; skips steps already done. `--check` only checks |
| `logs [--follow] [--lines N]` | Show the proxy log |
| `test [--url URL]` | Fetch a URL through both proxies |
| `autostart enable` / `disable` / `status` | Start the proxy at login (next section) |
| `--help`, `<command> --help`, `--version` | Help and version |

`start` waits 20 seconds for readiness by default (`--wait` changes this); slow initialization may take up to 30 extra seconds. It can return while still connecting, with retries continuing in the background; check `phase` in `status`.

### 5. Start at login without the tray

On Windows, replace `./ssh-proxy-cli` below with `.\ssh-proxy-cli.exe`.

```bash
./ssh-proxy-cli autostart enable    # start the proxy at future logins
./ssh-proxy-cli autostart status
./ssh-proxy-cli autostart disable   # stop doing that; the running proxy keeps running
```

- No administrator rights or sudo. `enable` doesn't start the proxy now; run `start` for that.
- Login must work without prompts. If `enable`'s login check fails, it only warns and still saves the setting.
- Works together with the tray without creating a second proxy.
- Windows briefly shows a console window at login. Linux needs a systemd user session. The proxy starts before your desktop session, so it can't use an ssh-agent started later (`systemctl --user import-environment SSH_AUTH_SOCK` lasts only for the current login): use the passphrase-free key `login` creates, or set `SSH_AUTH_SOCK` in `~/.config/environment.d/` to an agent socket that exists at login.
- Starts after user login, not before it. A crashed proxy is not restarted automatically; check `status` for the reason, then run `start`.

### 6. Settings and logs

After editing `config.toml`, run `restart` (or Stop and Start in the tray):

| Setting | Default and purpose |
| --- | --- |
| `proxy.bind` | `127.0.0.1`, this computer only |
| `proxy.http_port` / `proxy.socks_port` | `8181` / `1081`; must differ |
| `ssh.executable` | `ssh`; may be a full path to OpenSSH |
| `ssh.config_file` | `ssh_config` |
| `health.targets` | At least one must be reachable through the tunnel for "running" |
| `health.failover_on_probe_failure` | `false`: failed checks show `degraded` without switching hosts |
| `logging.max_log_files` / `logging.max_size_mb` | Keep `10` tray and `10` proxy logs / up to `10` MB each |

Each app or proxy run writes one continuous log; reaching the size limit removes its older half. Logs are in `logs/` in the install folder. **Open Current Log** in the tray opens the tray log; see the proxy log with `logs` or the log path in `status`.

### 7. Troubleshooting

| Problem | What to do |
| --- | --- |
| Invalid configuration | Run `check` and fix what `error` says |
| SSH connection fails | Check network/VPN, host and port, keys and agent; run `login my-proxy` |
| The server doesn't allow password login | Send the public key `login` prints (starting with `ssh-ed25519`) to your administrator, then run `login` again |
| Port in use | Close the other program, or change the port and update your clients |
| Stuck connecting / `degraded` | Read the proxy log; check the server allows forwarding and a health target is reachable |
| "OpenSSH 8.7 or newer is required" | Install a newer OpenSSH (see section 1) |
| A client bypasses the proxy | Check its proxy setting and fully restart it |
| macOS cannot open SSH setup | Allow SSH Proxy to control Terminal in System Settings → Privacy & Security → Automation |

Switching hosts closes existing connections; clients just reconnect. Only TCP is forwarded; there's no VPN or UDP support.

### 8. Custom scripts

Put non-interactive scripts in `scripts/` (`.ps1` on Windows, `.sh` on macOS). After restarting the tray they appear as **Run: filename**, usable while the proxy is running, with output in the tray log. Keep scripts in the `scripts/` folder.
