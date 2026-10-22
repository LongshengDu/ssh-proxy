#!/usr/bin/env python3
"""Build portable folders, optionally with a matching release ZIP. Requires Python 3.10+; no pip deps."""

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import plistlib
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
APP_VERSION = (ROOT / ".version").read_text(encoding="utf-8").strip()
SRC = ROOT / "src"
BUILD = ROOT / "build"
TOOLS = ROOT / ".build-tools"
DEFAULTS = SRC / "packaging" / "defaults"
SYSTEMS = ("linux", "windows", "darwin")
ARCHES = ("amd64", "arm64")
TARGETS = {f"{goos}-{arch}": (goos, arch) for goos in SYSTEMS for arch in ARCHES}
PUBLIC = tuple(sorted(path.relative_to(DEFAULTS).as_posix() for path in DEFAULTS.rglob("*") if path.is_file()))
COMMON = (*PUBLIC, ".version", "THIRD_PARTY_NOTICES.txt", "BUILDINFO.txt")
MAC_APP = "ssh_proxy_app.app/Contents/MacOS/ssh_proxy_app"
MAC_PLIST = "ssh_proxy_app.app/Contents/Info.plist"
MAC_ICON = "ssh_proxy_app.app/Contents/Resources/icon.icns"


def run(args, env=None, capture=False):
    args = [str(arg) for arg in args]
    print("+ " + subprocess.list2cmdline(args), flush=True)
    result = subprocess.run(args, cwd=ROOT, env=env, check=True, text=True,
                            encoding="utf-8", errors="replace",
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else ""


def host_target():
    system = {"Windows": "windows", "Darwin": "darwin", "Linux": "linux"}.get(platform.system())
    arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    target = f"{system}-{arch}"
    if target not in TARGETS:
        raise RuntimeError("Build hosts supported: Windows, macOS and Linux on x86-64 or ARM64. WSL is not used.")
    return target


def version(text):
    match = re.search(r"(?:go)?(\d+)\.(\d+)(?:\.(\d+))?(?![\w.])", text)
    return tuple(int(n or 0) for n in match.groups()) if match else (0, 0, 0)


def digest(path):
    value = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def unpack_go(archive, destination):
    """Extract only regular files/directories under go/, never links or traversal."""
    def output(name):
        path = PurePosixPath(name)
        if "\\" in name or ":" in name or path.is_absolute() or ".." in path.parts or not path.parts or path.parts[0] != "go":
            raise RuntimeError(f"Unsafe Go archive path: {name}")
        return destination.joinpath(*path.parts)

    if zipfile.is_zipfile(archive):
        with zipfile.ZipFile(archive) as bundle:
            for item in bundle.infolist():
                path = output(item.filename)
                mode = item.external_attr >> 16
                if stat.S_ISLNK(mode):
                    raise RuntimeError("Links are not allowed in Go downloads")
                if item.is_dir():
                    path.mkdir(parents=True, exist_ok=True)
                else:
                    path.parent.mkdir(parents=True, exist_ok=True)
                    with bundle.open(item) as source, path.open("xb") as dest:
                        shutil.copyfileobj(source, dest)
    else:
        with tarfile.open(archive) as bundle:
            for item in bundle:
                path = output(item.name)
                if item.isdir():
                    path.mkdir(parents=True, exist_ok=True)
                elif item.isfile():
                    path.parent.mkdir(parents=True, exist_ok=True)
                    with bundle.extractfile(item) as source, path.open("xb") as dest:
                        shutil.copyfileobj(source, dest)
                    path.chmod(item.mode & 0o777)
                else:
                    raise RuntimeError("Links/special files are not allowed in Go downloads")


def ensure_go(host, env, explicit=None):
    required = version(re.search(r"^go\s+(\S+)", (ROOT / "go.mod").read_text(), re.M)[1])
    binary = "go.exe" if host.startswith("windows") else "go"
    candidates = [explicit] if explicit else [TOOLS / "go" / "bin" / binary, *sorted(TOOLS.glob(f"go*/bin/{binary}"), reverse=True), shutil.which(binary)]
    for candidate in candidates:
        if not candidate:
            continue
        try:
            if version(run([candidate, "version"], env, capture=True)) >= required:
                return str(candidate)
        except (OSError, subprocess.CalledProcessError):
            pass
    if explicit:
        raise RuntimeError(f"--go must point to a working Go >= {'.'.join(map(str, required))}")
    print("Installing a project-local Go toolchain from go.dev (SHA-256 verified).", flush=True)
    with urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=30) as response:
        releases = json.load(response)
    goos, goarch = TARGETS[host]
    choices = [(version(release["version"]), file) for release in releases
               if release["stable"] and version(release["version"]) >= required
               for file in release["files"]
               if file["os"] == goos and file["arch"] == goarch and file["kind"] == "archive"]
    if not choices:
        raise RuntimeError(f"No official Go archive satisfies {required} on {host}")
    selected, file = max(choices, key=lambda item: item[0])
    TOOLS.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="download-", dir=TOOLS) as temporary:
        staging = Path(temporary)
        archive = staging / file["filename"]
        with urllib.request.urlopen("https://go.dev/dl/" + file["filename"], timeout=30) as response, archive.open("wb") as dest:
            shutil.copyfileobj(response, dest)
        if archive.stat().st_size != file["size"] or digest(archive) != file["sha256"]:
            raise RuntimeError("Go download checksum/size mismatch; archive was not installed")
        unpack_go(archive, staging)
        installed = TOOLS / ("go-" + ".".join(map(str, selected)) + "-" + host)
        if installed.exists():
            raise RuntimeError(f"Unusable toolchain already exists: {installed}; move it aside and retry")
        (staging / "go").rename(installed)
    executable = installed / "bin" / binary
    run([executable, "version"], env)
    return str(executable)


def ensure_ssh(host, env):
    if host.startswith("windows"):
        # Prefer Windows OpenSSH over other ssh.exe builds on PATH, such as Git for Windows' MSYS ssh.
        native = Path(os.environ.get("SystemRoot", r"C:\Windows")) / "System32" / "OpenSSH"
        if not (native / "ssh.exe").is_file() and not shutil.which("ssh", path=env["PATH"]):
            print("Installing Windows OpenSSH Client (Windows requires an elevated terminal).", flush=True)
            run(["powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
                 "$ErrorActionPreference = 'Stop'; Add-WindowsCapability -Online -Name OpenSSH.Client~~~~0.0.1.0"], env)
        if (native / "ssh.exe").is_file():
            env["PATH"] = str(native) + os.pathsep + env["PATH"]
    if shutil.which("ssh", path=env["PATH"]):
        return
    raise RuntimeError("Native OpenSSH is missing. Install your OS OpenSSH client (Linux: openssh-client/openssh-clients) and retry; Windows installation needs an elevated terminal.")


def environment(target):
    cache, temporary = BUILD / ".cache" / "go-build", BUILD / ".tmp"
    cache.mkdir(parents=True, exist_ok=True)
    temporary.mkdir(parents=True, exist_ok=True)
    env = os.environ.copy()
    for key in ("GOROOT", "GOFLAGS", "GOEXPERIMENT", "SSH_PROXY_LIVE", "SSH_PROXY_DISTRIBUTION"):
        env.pop(key, None)
    goos, goarch = TARGETS[target]
    env.update(CGO_ENABLED="0", GOOS=goos, GOARCH=goarch, GOTOOLCHAIN="local", GOWORK="off",
               GOCACHE=str(cache), GOTMPDIR=str(temporary), TEMP=str(temporary), TMP=str(temporary), TMPDIR=str(temporary),
               PYTHONDONTWRITEBYTECODE="1")
    return env


def payload_names(target):
    executables = ("ssh_proxy_cli",)
    if target.startswith("windows-"):
        executables = ("ssh_proxy_cli.exe", "ssh_proxy_app.exe")
    elif target.startswith("darwin-"):
        executables = ("ssh_proxy_cli", MAC_APP, MAC_PLIST, MAC_ICON)
    return (*executables, *COMMON)


def app_plist(path, name, executable, identifier):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("wb") as stream:
        plistlib.dump(dict(CFBundleName=name, CFBundleDisplayName=name, CFBundleExecutable=executable,
                          CFBundleIdentifier=identifier, CFBundleIconFile="icon.icns", CFBundlePackageType="APPL", CFBundleVersion=APP_VERSION,
                          CFBundleShortVersionString=APP_VERSION, LSUIElement=True, LSMinimumSystemVersion="12.0"), stream)


def compile_apps(go, env, target, output):
    output.mkdir(parents=True, exist_ok=True)
    windows = target.startswith("windows-")
    jobs = [("ssh_proxy_cli", output / ("ssh_proxy_cli.exe" if windows else "ssh_proxy_cli"), False)]
    if not target.startswith("linux-"):
        jobs.append(("ssh_proxy_app", output / ("ssh_proxy_app.exe" if windows else MAC_APP), True))
    if target.startswith("darwin-"):
        app_plist(output / MAC_PLIST, "SSH Proxy", "ssh_proxy_app", "com.sshproxy.tray")
        (output / MAC_ICON).parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(SRC / "ui" / "icon.icns", output / MAC_ICON)
    for command, path, gui in jobs:
        path.parent.mkdir(parents=True, exist_ok=True)
        flags = f"-s -w -X ssh_proxy/src/proxy.version={APP_VERSION}" + (" -H=windowsgui" if windows and gui else "")
        run([go, "build", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", path, "./src/cmd/" + command], env)
        if windows and gui:
            # Pure-Go resource tool runs on the build host, also for cross-builds.
            # Patch only the staged unsigned executable; no generated objects in src/.
            run([go, "tool", "go-winres", "patch", "--no-backup", "--in", SRC / "packaging" / "windows-icon.json", path], environment(host_target()))
        path.chmod(0o755)
    return [job[1] for job in jobs]


def linked_modules(build_info):
    """Module paths linked into the binaries, read from `go version -m` output."""
    modules = set()
    for text in build_info:
        for fields in (line.split() for line in text.splitlines()):
            if fields[:1] in (["dep"], ["=>"]) and len(fields) > 1:
                modules.add(fields[1])
    return modules


def notices(go, env, modules):
    """Licenses of the linked modules only, not test or tool dependencies."""
    texts = ["Third-party software bundled with SSH Proxy. Native OpenSSH is supplied by the OS, not bundled."]
    for line in run([go, "list", "-m", "-f", "{{if .Dir}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}", "all"], env, capture=True).splitlines():
        module, revision, directory = line.split("|", 2)
        if module not in modules:
            continue
        licenses = sorted(path for path in Path(directory).iterdir() if path.is_file() and re.match(r"^(LICENSE|COPYING|NOTICE)", path.name, re.I))
        if not licenses:
            raise RuntimeError(f"No license found for {module}")
        texts.append(f"\n=== {module} {revision} ===")
        texts.extend(path.read_text(encoding="utf-8") for path in licenses)
    goroot = Path(run([go, "env", "GOROOT"], env, capture=True))
    texts.extend(["\n=== Go standard library ===", (goroot / "LICENSE").read_text(encoding="utf-8")])
    return "\n".join(texts) + "\n"


def package(target, staging, target_dir):
    """ZIP only the public payload, with one os-arch directory at its root."""
    for name in PUBLIC:
        if (staging / name).read_bytes() != (DEFAULTS / name).read_bytes():
            raise RuntimeError(f"Refusing to package non-default {name}")
    archive = target_dir / f"{target}.zip"
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as bundle:
        for name in (*payload_names(target), "SHA256SUMS"):
            path = staging / name
            if path.is_symlink() or not path.is_file():
                raise RuntimeError(f"Expected regular payload file: {path}")
            info = zipfile.ZipInfo(f"{target}/{name}")
            info.create_system = 3
            executable = name.endswith((".exe", ".sh")) or "/MacOS/" in name or name == "ssh_proxy_cli"
            info.external_attr = (stat.S_IFREG | (0o755 if executable else 0o644)) << 16
            bundle.writestr(info, path.read_bytes(), compress_type=zipfile.ZIP_DEFLATED)
        info = zipfile.ZipInfo(f"{target}/logs/")
        info.create_system = 3
        info.external_attr = (stat.S_IFDIR | 0o755) << 16 | 0x10
        bundle.writestr(info, b"")
    print(f"Release ZIP: {archive}", flush=True)


def build_target(go, env, target, target_dir, release=False):
    output = target_dir / target
    with tempfile.TemporaryDirectory(prefix="package-", dir=BUILD / ".tmp") as temporary:
        staging = Path(temporary) / target
        binaries = compile_apps(go, env, target, staging)
        for name in PUBLIC:
            existing = not release and (output / name).exists()
            source = output / name if existing else DEFAULTS / name
            (staging / name).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, staging / name)
            if name.endswith(".sh") and not existing:
                (staging / name).chmod(0o755)
        (staging / "logs").mkdir()
        (staging / ".version").write_text(APP_VERSION + "\n", encoding="utf-8")
        build_info = {path: run([go, "version", "-m", path], env, capture=True) for path in binaries}
        (staging / "THIRD_PARTY_NOTICES.txt").write_text(notices(go, env, linked_modules(build_info.values())), encoding="utf-8")
        metadata = "\n".join(text.replace(str(path), path.name) for path, text in build_info.items())
        (staging / "BUILDINFO.txt").write_text(metadata + "\n", encoding="utf-8")
        (staging / "SHA256SUMS").write_text("".join(f"{digest(staging / name)}  {name}\n" for name in payload_names(target)), encoding="utf-8")
        # Defaults are installed only when absent; binaries/metadata always refresh.
        # Releases replace all packaged files. Unrelated files/logs never enter the ZIP.
        for name in (*payload_names(target), "SHA256SUMS"):
            destination = output / name
            if not release and name in PUBLIC and destination.exists():
                continue
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(staging / name, destination)
        (output / "logs").mkdir(exist_ok=True)
        if release:
            package(target, staging, target_dir)
    print(f"Build folder: {output}", flush=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release", action="store_true", help="Create the folder and matching os-arch.zip (default directory: build/release/)")
    parser.add_argument("--target-dir", type=Path, help="Output parent directory (default: build/, or build/release/ with --release)")
    parser.add_argument("--os", choices=SYSTEMS, help="Target OS (default: current)")
    parser.add_argument("--arch", choices=ARCHES, help="Target architecture (default: current)")
    parser.add_argument("--go", help="Explicit Go executable (otherwise discover or download)")
    args = parser.parse_args(argv)
    host = host_target()
    host_os, host_arch = TARGETS[host]
    target = f"{args.os or host_os}-{args.arch or host_arch}"
    target_dir = (args.target_dir or (BUILD / "release" if args.release else BUILD)).resolve()
    host_env = environment(host)
    go = ensure_go(host, host_env, args.go)
    ensure_ssh(host, host_env)
    env = environment(target)
    env["PATH"] = host_env["PATH"]
    cgo = run([go, "list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}: {{.CgoFiles}}{{end}}", "./src/cmd/..."], env, capture=True)
    if cgo:
        raise RuntimeError("Unexpected cgo dependencies: " + cgo)
    build_target(go, env, target, target_dir, args.release)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError, ValueError) as error:
        print(f"Build failed: {error}", file=sys.stderr)
        sys.exit(1)
