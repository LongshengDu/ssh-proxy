#!/usr/bin/env python3
"""Run all source/component tests; no installed app, desktop session or real SSH account needed."""

import argparse
import subprocess
import sys

import build as b


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", help="Go executable (otherwise discover/bootstrap)")
    args = parser.parse_args(argv)
    target = b.host_target()
    env = b.environment(target)
    go = b.ensure_go(target, env, args.go)
    b.ensure_ssh(target, env)
    b.run([go, "mod", "download"], env)
    b.run([go, "mod", "verify"], env)
    b.run([go, "vet", "./src/...", "./tests/unit", "./tests/helpers"], env)
    b.run([go, "test", "-vet=off", "-count=1", "-timeout", "120s", "./tests/unit"], env)
    return 0


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    try:
        sys.exit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"Unit tests failed: {error}", file=sys.stderr)
        sys.exit(1)
