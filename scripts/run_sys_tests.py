#!/usr/bin/env python3
"""Test an existing native build using isolated configs and a local SSH server.

Windows/macOS app checks require an unlocked desktop. macOS requests permissions
before testing. This runner never builds releases or uses private dev settings.
"""

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import shutil
import subprocess
import sys

import build as b


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, help="Built os-arch folder (default: build/<current-os>-<arch>)")
    parser.add_argument("--go", help="Go executable (otherwise discover/bootstrap)")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--cli-only", dest="scope", action="store_const", const="cli", help="Run CLI/backend tests only")
    selection.add_argument("--app-only", dest="scope", action="store_const", const="app", help="Run Windows/macOS app tests only (also needs the built CLI)")
    parser.set_defaults(scope="all")
    parser.add_argument("--live-config", type=Path, help="Also test real SSH endpoints from this config (keys/agent required)")
    args = parser.parse_args(argv)
    if args.scope == "app" and args.live_config:
        parser.error("--live-config belongs to CLI tests; omit --app-only")
    return args


def prepare_environment(args, target, directory):
    if target.startswith("linux") and args.scope == "app":
        raise RuntimeError("Linux supports CLI tests only; use --cli-only or omit the selection.")
    cli = directory / ("ssh_proxy_cli.exe" if target.startswith("windows") else "ssh_proxy_cli")
    if not cli.is_file():
        raise RuntimeError(f"Missing {cli}. Run python scripts/build.py first.")
    env = b.environment(target)
    env["GOFLAGS"] = "-tags=systemtest"
    env["SSH_PROXY_SYS_SCOPE"] = args.scope
    if args.live_config:
        env["SSH_PROXY_LIVE_CONFIG"] = str(args.live_config.resolve())
    else:
        env.pop("SSH_PROXY_LIVE_CONFIG", None)
    go = b.ensure_go(target, env, args.go)
    b.ensure_ssh(target, env)
    return go, env


def stage_programs(directory, target, report, scope):
    staging = report / "programs"
    for name in b.payload_names(target):
        if scope == "cli" and name.startswith("ssh_proxy_app"):
            continue
        source = b.DEFAULTS / name if name in b.PUBLIC else directory / name
        destination = staging / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, destination)
    (staging / "logs").mkdir()
    return staging


def request_macos_permissions():
    """Ask through supported OS APIs; only the user can grant access."""
    try:
        from ApplicationServices import AXIsProcessTrustedWithOptions, kAXTrustedCheckOptionPrompt
    except ImportError as error:
        raise RuntimeError("Install macOS test dependencies: python3 -m pip install -r requirements.txt") from error
    print("macOS will request Accessibility and Automation access. Allow this terminal/Python to control System Events and Terminal.", flush=True)
    while True:
        accessible = AXIsProcessTrustedWithOptions({kAXTrustedCheckOptionPrompt: True})
        # Sending real Apple events triggers Automation consent for each target.
        probes = [subprocess.run(["/usr/bin/osascript", "-e", script], capture_output=True,
                                 text=True) for script in (
            'tell application "System Events" to get UI elements enabled',
            'tell application "Terminal" to get name',
        )]
        accessible = AXIsProcessTrustedWithOptions({kAXTrustedCheckOptionPrompt: False})
        if accessible and all(p.returncode == 0 for p in probes) and probes[0].stdout.strip() == "true":
            return
        pane = "Privacy_Accessibility" if not accessible or probes[0].stdout.strip() == "false" else "Privacy_Automation"
        subprocess.run(["/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?" + pane], check=True)
        for probe in probes:
            if probe.returncode:
                print(probe.stderr.strip(), file=sys.stderr)
        if not sys.stdin.isatty():
            raise RuntimeError("Desktop permissions are missing. Grant access in System Settings and rerun from an interactive terminal.")
        input("Grant the requested access in System Settings, then press Enter to recheck (Ctrl+C cancels): ")


def in_scope(name, scope):
    return scope == "all" or (name.startswith("TestTray") == (scope == "app"))


def list_tests(go, env, scope):
    """Every top-level test compiled for this host and scope is required."""
    output = b.run([go, "test", "-list", ".", "./tests/system"], env, capture=True)
    names = [line.strip() for line in output.splitlines() if line.startswith("Test")]
    return sorted(name for name in names if in_scope(name, scope))


def run_tests(go, env, report, scope):
    command = [go, "test", "-json", "-count=1", "-timeout", "12m"]
    if scope == "cli":
        command += ["-skip", "^TestTray"]
    elif scope == "app":
        command += ["-run", "^TestTray"]
    command.append("./tests/system")
    results = {}
    with (report / "events.jsonl").open("w", encoding="utf-8") as events, (report / "output.txt").open("w", encoding="utf-8") as output:
        with subprocess.Popen(command, cwd=b.ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True, encoding="utf-8", errors="replace") as process:
            for line in process.stdout:
                events.write(line)
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    event = {"Output": line}
                message = event.get("Output", "")
                output.write(message)
                output.flush()
                print(message, end="", flush=True)
                if event.get("Test") and event.get("Action") in ("pass", "fail", "skip"):
                    results[event["Test"]] = event["Action"]
            return process.wait(), results


# Only these tests may skip, and only under the stated condition.
ALLOWED_SKIPS = {"TestLiveEndpoints": lambda args: not args.live_config}


def assess_results(code, results, required, args):
    """Missing tests and unexpected skips cannot produce a passing report."""
    allowed = {name for name, condition in ALLOWED_SKIPS.items() if condition(args)}
    unexpected = [name for name, state in results.items() if state == "skip" and name not in allowed]
    missing = sorted(name for name in required if name not in allowed and results.get(name) != "pass")
    if not required:
        missing = ["(no system tests found for this scope)"]
    passed = code == 0 and not unexpected and not missing
    return dict(result="pass" if passed else "fail", required=required, tests=results,
                unexpected_skips=unexpected, required_not_passed=missing)


def main(argv=None):
    args = parse_args(argv)
    target = b.host_target()
    directory = (args.build_dir or b.BUILD / target).resolve()
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S-%f")
    report = b.BUILD / "test-results" / ("system-" + stamp)
    report.mkdir(parents=True)
    summary = dict(target=target, scope=args.scope, full_platform_coverage=args.scope == "all" or (target.startswith("linux") and args.scope == "cli"),
                   build_dir=str(directory), live_endpoints=bool(args.live_config))
    print(f"Testing {directory} ({args.scope})\nReport: {report}", flush=True)
    try:
        go, env = prepare_environment(args, target, directory)
        env["SSH_PROXY_SYS_DIR"] = str(stage_programs(directory, target, report, args.scope))
        if target.startswith("darwin") and args.scope != "cli":
            request_macos_permissions()
        required = list_tests(go, env, args.scope)
        code, results = run_tests(go, env, report, args.scope)
        summary.update(assess_results(code, results, required, args))
    except (OSError, RuntimeError, subprocess.SubprocessError, EOFError, KeyboardInterrupt) as error:
        summary.update(result="fail", error=str(error) or "Testing cancelled")
    (report / "summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    if summary.get("error"):
        print(summary["error"], file=sys.stderr)
    print(f"System tests ({args.scope}): {summary['result'].upper()}\nReport: {report / 'summary.json'}")
    return 1 if summary["result"] == "fail" else 0


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.exit(main())
