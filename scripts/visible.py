#!/usr/bin/env python3
"""Run a command in a visible terminal and record its output and completion under build/test-results/.

Windows opens a new PowerShell console that stays open for review. Other hosts run in the current terminal.
Example: python scripts/visible.py --name unit -- python scripts/run_unit_tests.py
"""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
RESULTS = ROOT / "build" / "test-results"


def now():
    return datetime.now().astimezone().isoformat(timespec="seconds")


def child(report):
    """Run the recorded command, tee its output and write completion.json."""
    request = json.loads((report / "command.json").read_text(encoding="utf-8"))
    print(f"> {subprocess.list2cmdline(request['command'])}\nReport: {report}\n", flush=True)
    env = dict(os.environ, PYTHONUNBUFFERED="1", PYTHONIOENCODING="utf-8")
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


def launch(name, command, wait):
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    report = RESULTS / f"{name}-{stamp}"
    report.mkdir(parents=True)
    (report / "command.json").write_text(json.dumps(dict(name=name, command=command), indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    if os.name != "nt":
        return child(report)
    quote = lambda value: "'" + str(value).replace("'", "''") + "'"
    script = f"& {quote(sys.executable)} {quote(Path(__file__).resolve())} --child {quote(report)}"
    terminal = subprocess.Popen(["powershell.exe", "-NoLogo", "-NoProfile", "-NoExit", "-Command", script],
                                cwd=ROOT, creationflags=subprocess.CREATE_NEW_CONSOLE)
    print(f"Started in a new window: {name}\nReport: {report}", flush=True)
    if not wait:
        return 0
    completion = report / "completion.json"
    while not completion.exists():
        if terminal.poll() is not None and not completion.exists():
            result = dict(name=name, result="cancelled", exit_code=130, finished=now(),
                          error="Terminal closed before the command reported completion")
            completion.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
            break
        time.sleep(1)
    result = json.loads(completion.read_text(encoding="utf-8"))
    print(f"{name}: {result['result'].upper()} (exit {result['exit_code']})\nOutput: {report / 'output.txt'}")
    return result["exit_code"]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--name", help="Result folder prefix, for example unit or build")
    parser.add_argument("--no-wait", action="store_true", help="Return after opening the window (Windows)")
    parser.add_argument("--child", type=Path, help=argparse.SUPPRESS)
    parser.add_argument("command", nargs=argparse.REMAINDER, help="Command to run after --")
    args = parser.parse_args(argv)
    if args.child:
        return child(args.child)
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or not args.name or not re.fullmatch(r"[A-Za-z0-9_.-]+", args.name):
        parser.error("use --name NAME -- COMMAND [ARGS...]; NAME uses letters, digits, dot, dash or underscore")
    return launch(args.name, command, not args.no_wait)


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.exit(main())
