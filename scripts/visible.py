#!/usr/bin/env python3
"""Run a command in a visible terminal window and record its output and result under build/test-results/.

For any agent or person on Windows, macOS and Linux: the command runs in a new window titled NAME (PowerShell on
Windows, Terminal on macOS, the desktop's terminal emulator on Linux) that closes when the command finishes, while
the caller waits for its exit code. Without a desktop, or on macOS without permission to control Terminal, it runs
in the current terminal instead. The command keeps the caller's PATH. On failure the caller also gets the last
output lines. With --no-wait the caller returns at once and reads completion.json later.
Example: python scripts/visible.py --name unit -- python scripts/run_unit_tests.py
"""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
RESULTS = ROOT / "build" / "test-results"
TAIL_LINES = 40

# Linux terminal emulators in order of preference, with the arguments that run a
# command. $TERMINAL, a common desktop setting, comes first and must accept -e.
LINUX_TERMINALS = (("x-terminal-emulator", "-e"), ("gnome-terminal", "--"), ("konsole", "-e"), ("xfce4-terminal", "-x"),
                   ("kitty",), ("alacritty", "-e"), ("wezterm", "start", "--"), ("foot",), ("xterm", "-e"))

# macOS Terminal scripts; arguments are passed as data, never interpolated.
OPEN_TERMINAL = """on run argv
    tell application "Terminal"
        activate
        do script (item 1 of argv)
        return id of front window
    end tell
end run"""
CLOSE_TERMINAL = """on run argv
    if application "Terminal" is not running then return "gone"
    set windowId to (item 1 of argv) as integer
    tell application "Terminal"
        if not (exists window id windowId) then return "gone"
        if busy of tab 1 of window id windowId then return "busy"
        close window id windowId
        return "closed"
    end tell
end run"""


def now():
    return datetime.now().astimezone().isoformat(timespec="seconds")


def child(report):
    """Run the recorded command, tee its output and write completion.json."""
    request = json.loads((report / "command.json").read_text(encoding="utf-8"))
    (report / "child.pid").write_text(str(os.getpid()), encoding="utf-8")
    if os.name != "nt" and sys.stdout.isatty():
        print(f"\033]0;{request['name']}\007", end="")  # Window title.
    print(f"> {subprocess.list2cmdline(request['command'])}\nReport: {report}\n", flush=True)
    # A new terminal window may start a login shell; keep the caller's PATH, such as an active venv.
    env = dict(os.environ, PATH=request.get("path") or os.environ.get("PATH", ""), PYTHONUNBUFFERED="1", PYTHONIOENCODING="utf-8")
    completion = dict(name=request["name"], command=request["command"], started=now())
    try:
        with (report / "output.txt").open("w", encoding="utf-8") as output, \
                subprocess.Popen(request["command"], cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                 text=True, encoding="utf-8", errors="replace") as process:
            for line in process.stdout:
                output.write(line)
                output.flush()
                print(line, end="", flush=True)
            code = process.wait()
        completion.update(result="pass" if code == 0 else "fail", exit_code=code)
    except KeyboardInterrupt:
        completion.update(result="cancelled", exit_code=130)
    except OSError as error:
        completion.update(result="fail", exit_code=127, error=str(error))
    completion["finished"] = now()
    (report / "completion.json").write_text(json.dumps(completion, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"\n{request['name']}: {completion['result'].upper()} (exit {completion['exit_code']})\nReport: {report}", flush=True)
    return completion["exit_code"]


def windows_terminal(name, args):
    """New PowerShell console. It exits 0, so terminals that keep failed sessions
    open still close it; the result is in completion.json. Returns running()."""
    quote = lambda value: "'" + str(value).replace("'", "''") + "'"
    script = f"$Host.UI.RawUI.WindowTitle = {quote(name)}; & " + " ".join(map(quote, args)) + "; exit 0"
    terminal = subprocess.Popen(["powershell.exe", "-NoLogo", "-NoProfile", "-Command", script],
                                cwd=ROOT, creationflags=subprocess.CREATE_NEW_CONSOLE)
    return lambda: terminal.poll() is None


def macos_terminal(args):
    """New Terminal window running the child; returns close() for once it exits."""
    osascript = lambda *script: subprocess.run(["/usr/bin/osascript", "-e", *map(str, script)],
                                               check=True, capture_output=True, text=True).stdout.strip()
    try:
        window = osascript(OPEN_TERMINAL, f"cd {shlex.quote(str(ROOT))} && exec {shlex.join(map(str, args))}")
    except (OSError, subprocess.CalledProcessError) as error:
        raise RuntimeError(f"cannot control Terminal: {getattr(error, 'stderr', '') or error}") from error

    def close():
        # Terminal may keep a window whose shell exited; close it once it is idle.
        for _ in range(20):
            if osascript(CLOSE_TERMINAL, window) != "busy":
                return
            time.sleep(0.25)
    return close


def linux_terminal(args):
    """New window of the desktop's terminal emulator, which closes when the child exits."""
    if not (os.environ.get("DISPLAY") or os.environ.get("WAYLAND_DISPLAY")):
        raise RuntimeError("no desktop display")
    preferred = ((os.environ["TERMINAL"], "-e"),) if os.environ.get("TERMINAL") else ()
    for program, *run in preferred + LINUX_TERMINALS:
        if shutil.which(program):
            subprocess.Popen([program, *run, *map(str, args)], cwd=ROOT, stdin=subprocess.DEVNULL,
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
            return
    raise RuntimeError("no terminal emulator found")


def child_running(report, deadline):
    """Whether the windowed child still runs, from the PID it records. A terminal
    that never starts it counts as closed after the deadline."""
    pid = report / "child.pid"
    if not pid.exists():
        return time.monotonic() < deadline
    try:
        os.kill(int(pid.read_text(encoding="utf-8")), 0)
    except (OSError, ValueError):
        return False
    return True


def launch(name, command, wait):
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    report = RESULTS / f"{name}-{stamp}"
    report.mkdir(parents=True)
    request = dict(name=name, command=command, path=os.environ.get("PATH", ""))
    (report / "command.json").write_text(json.dumps(request, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    args = [sys.executable, Path(__file__).resolve(), "--child", report]
    close = None
    try:
        if os.name == "nt":
            running = windows_terminal(name, args)
        else:
            close = macos_terminal(args) if sys.platform == "darwin" else linux_terminal(args)
            deadline = time.monotonic() + 30
            running = lambda: child_running(report, deadline)
    except RuntimeError as error:
        print(f"No visible window ({error}); running in this terminal.", flush=True)
        return child(report)
    print(f"Started in a new window: {name}\nReport: {report}", flush=True)
    if not wait:
        return 0
    completion = report / "completion.json"
    while not completion.exists():
        if not running() and not completion.exists():
            result = dict(name=name, result="cancelled", exit_code=130, finished=now(),
                          error="Terminal closed before the command reported completion")
            completion.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
            break
        time.sleep(1)
    # The child writes completion.json just before it exits; then its window closes.
    for _ in range(20):
        if not running():
            break
        time.sleep(0.25)
    if close:
        close()
    result = json.loads(completion.read_text(encoding="utf-8"))
    output = report / "output.txt"
    if result["exit_code"] != 0 and output.exists():
        tail = output.read_text(encoding="utf-8", errors="replace").splitlines()[-TAIL_LINES:]
        print(f"Last {len(tail)} output lines:\n" + "\n".join(tail))
    print(f"{name}: {result['result'].upper()} (exit {result['exit_code']})\nOutput: {output}")
    return result["exit_code"]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--name", help="Result folder prefix and window title, for example unit or build")
    parser.add_argument("--no-wait", action="store_true", help="Return after opening the window; read completion.json later")
    parser.add_argument("--child", type=Path, help=argparse.SUPPRESS)
    parser.add_argument("command", nargs=argparse.REMAINDER, help="Command to run after --")
    args = parser.parse_args(argv)
    if args.child:
        # The window's own exit status is not the result: the caller reads completion.json.
        child(args.child)
        return 0
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or not args.name or not re.fullmatch(r"[A-Za-z0-9_.-]+", args.name):
        parser.error("use --name NAME -- COMMAND [ARGS...]; NAME uses letters, digits, dot, dash or underscore")
    return launch(args.name, command, not args.no_wait)


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.exit(main())
