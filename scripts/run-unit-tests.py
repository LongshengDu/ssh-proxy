#!/usr/bin/env python3
"""Run all source/component tests; no installed app, desktop session or real SSH account needed."""

import argparse
import os
from pathlib import Path
import subprocess
import sys

import build as b

# Static checks cover every supported OS, so Windows development also type-checks macOS/Linux code.
CHECK_SYSTEMS = ("windows", "darwin", "linux")
PACKAGES = ["./src/...", "./tests/unit", "./tests/helpers"]
SYSTEM_PACKAGES = ["-tags", "systemtest", "./tests/system", "./tests/helpers"]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", help="Go executable (otherwise discover/bootstrap)")
    args = parser.parse_args(argv)
    target = b.host_target()
    env = b.environment(target)
    go = b.ensure_go(target, env, args.go)
    b.ensure_ssh(target, env)
    # staticcheck runs the go command itself.
    env["PATH"] = str(Path(go).parent) + os.pathsep + env["PATH"]
    b.run([go, "mod", "download"], env)
    b.run([go, "mod", "verify"], env)
    unformatted = b.run([Path(go).with_name("gofmt" + Path(go).suffix), "-l", "src", "tests"], env, capture=True)
    if unformatted:
        raise RuntimeError("gofmt needed:\n" + unformatted)
    staticcheck = b.BUILD / ".cache" / "bin" / ("staticcheck" + Path(go).suffix)
    b.run([go, "build", "-o", staticcheck, "honnef.co/go/tools/cmd/staticcheck"], env)
    for goos in CHECK_SYSTEMS:
        checked = dict(env, GOOS=goos)
        b.run([go, "vet", *PACKAGES], checked)
        b.run([go, "vet", *SYSTEM_PACKAGES], checked)
        b.run([staticcheck, *PACKAGES], checked)
        b.run([staticcheck, *SYSTEM_PACKAGES], checked)
    b.run([go, "test", "-vet=off", "-count=1", "-timeout", "120s", "./tests/unit"], env)
    return 0


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    try:
        sys.exit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"Unit tests failed: {error}", file=sys.stderr)
        sys.exit(1)
