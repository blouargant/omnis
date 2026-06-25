#!/usr/bin/env python3
"""Build per-platform ``omnis-agent`` wheels by repackaging cross-compiled binaries.

Because the Go binaries are ``CGO_ENABLED=0`` static builds, every platform wheel
can be produced on a single host: for each target we cross-compile ``omnis`` and
``omnis-server``, stage them next to the bundled config/registry/web tree under
``packaging/pip/src/omnis/_dist``, and run ``setup.py bdist_wheel --plat-name``
with the matching PEP 425 platform tag. Wheels land in ``dist/wheels``.

Usage:
    python3 scripts/build_wheels.py                  # all platforms
    python3 scripts/build_wheels.py linux/amd64 ...  # a subset
    VERSION=v1.2.3 python3 scripts/build_wheels.py   # pin the version

Environment:
    VERSION    Source version (default: ``git describe`` / ``dev``). Normalised
               to PEP 440 for the wheel; passed verbatim to the Go ldflags.
"""

import os
import re
import shutil
import subprocess
import sys

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PIP_DIR = os.path.join(REPO_ROOT, "packaging", "pip")
PKG_DIR = os.path.join(PIP_DIR, "src", "omnis")
DIST_STAGE = os.path.join(PKG_DIR, "_dist")
WHEELS_OUT = os.path.join(REPO_ROOT, "dist", "wheels")

# Go target -> PEP 425 platform tag. Static binaries make manylinux2014 safe.
PLATFORMS = {
    "linux/amd64": "manylinux2014_x86_64",
    "linux/arm64": "manylinux2014_aarch64",
    "darwin/amd64": "macosx_10_13_x86_64",
    "darwin/arm64": "macosx_11_0_arm64",
    "windows/amd64": "win_amd64",
    "windows/arm64": "win_arm64",
}

# Config JSONs + server.yaml seeded into ~/.omnis and exposed as the system layer.
CONFIG_FILES = [
    "agents.json",
    "models.json",
    "mcp_config.json",
    "permissions.json",
    "preferences.json",
    "remote_registries.json",
    "a2a_config.json",
    "server.yaml",
]


def run(cmd, **kw):
    print("  $ " + " ".join(cmd))
    subprocess.run(cmd, check=True, **kw)


def git_version():
    if os.environ.get("VERSION"):
        return os.environ["VERSION"].strip()
    try:
        out = subprocess.run(
            ["git", "describe", "--tags", "--always", "--dirty"],
            cwd=REPO_ROOT, capture_output=True, text=True,
        )
        if out.returncode == 0 and out.stdout.strip():
            return out.stdout.strip()
    except OSError:
        pass
    return "dev"


def git_commit():
    if os.environ.get("COMMIT"):
        return os.environ["COMMIT"].strip()
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            cwd=REPO_ROOT, capture_output=True, text=True,
        )
        if out.returncode == 0:
            return out.stdout.strip() or "none"
    except OSError:
        pass
    return "none"


def build_date():
    return subprocess.run(
        ["date", "-u", "+%Y-%m-%dT%H:%M:%SZ"], capture_output=True, text=True
    ).stdout.strip()


def pep440(version):
    """Normalise a git/semver version string to a PEP 440 wheel version.

    ``v1.2.3`` -> ``1.2.3``; ``v1.2.3-rc1`` -> ``1.2.3rc1``;
    ``v1.2.3-beta2`` -> ``1.2.3b2``; anything non-conforming (snapshots, dirty
    describes) -> ``0.0.0.dev0`` so the wheel always carries a valid version.
    """
    v = version.strip().lstrip("v")
    m = re.match(r"^(\d+\.\d+\.\d+)$", v)
    if m:
        return m.group(1)
    m = re.match(r"^(\d+\.\d+\.\d+)-rc(\d+)$", v)
    if m:
        return "{}rc{}".format(m.group(1), m.group(2))
    m = re.match(r"^(\d+\.\d+\.\d+)-beta(\d+)$", v)
    if m:
        return "{}b{}".format(m.group(1), m.group(2))
    return "0.0.0.dev0"


def stage_assets():
    """Copy the bundled config/registry/web tree into ``_dist`` (binaries added later)."""
    sysconf = os.path.join(DIST_STAGE, "sysconf")
    os.makedirs(sysconf, exist_ok=True)
    os.makedirs(os.path.join(DIST_STAGE, "bin"), exist_ok=True)

    for name in CONFIG_FILES:
        shutil.copy2(os.path.join(REPO_ROOT, "config", name), os.path.join(sysconf, name))

    shutil.copytree(
        os.path.join(REPO_ROOT, "config", "filters"),
        os.path.join(sysconf, "filters"),
    )
    for kind in ("agents", "skills"):
        shutil.copytree(
            os.path.join(REPO_ROOT, "registry", kind),
            os.path.join(sysconf, "registry", kind),
        )
    shutil.copytree(os.path.join(REPO_ROOT, "web"), os.path.join(DIST_STAGE, "web"))


def build_binaries(goos, goarch, version, commit, date):
    ldflags = " ".join([
        "-s", "-w",
        "-X", "main.version=" + version,
        "-X", "main.commit=" + commit,
        "-X", "main.date=" + date,
    ])
    env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
    ext = ".exe" if goos == "windows" else ""
    targets = [("omnis", "."), ("omnis-server", "./server")]
    for binname, pkg in targets:
        out = os.path.join(DIST_STAGE, "bin", binname + ext)
        run(
            ["go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, pkg],
            cwd=REPO_ROOT, env=env,
        )
        if goos != "windows":
            os.chmod(out, 0o755)  # preserve +x through the wheel


def build_wheel(plat_tag, wheel_version):
    env = dict(os.environ, OMNIS_WHEEL_VERSION=wheel_version)
    # Clean stale build state so a prior target's files can't leak in.
    for junk in ("build", "omnis_agent.egg-info"):
        shutil.rmtree(os.path.join(PIP_DIR, junk), ignore_errors=True)
    run(
        [
            sys.executable, "setup.py", "bdist_wheel",
            "--plat-name", plat_tag,
            "--dist-dir", WHEELS_OUT,
        ],
        cwd=PIP_DIR, env=env,
    )


def main(argv):
    selected = argv or list(PLATFORMS)
    unknown = [p for p in selected if p not in PLATFORMS]
    if unknown:
        sys.exit("unknown platform(s): {} (known: {})".format(
            ", ".join(unknown), ", ".join(PLATFORMS)))

    version = git_version()
    commit = git_commit()
    date = build_date()
    wheel_version = pep440(version)
    print(">> source version: {}  ->  wheel version (PEP 440): {}".format(version, wheel_version))

    os.makedirs(WHEELS_OUT, exist_ok=True)
    built = []
    for target in selected:
        goos, goarch = target.split("/")
        plat_tag = PLATFORMS[target]
        print(">> {}  ->  {}".format(target, plat_tag))
        shutil.rmtree(DIST_STAGE, ignore_errors=True)
        stage_assets()
        build_binaries(goos, goarch, version, commit, date)
        build_wheel(plat_tag, wheel_version)
        built.append(plat_tag)

    # Leave a clean tree behind.
    shutil.rmtree(DIST_STAGE, ignore_errors=True)
    for junk in ("build", "omnis_agent.egg-info"):
        shutil.rmtree(os.path.join(PIP_DIR, junk), ignore_errors=True)

    print(">> built {} wheel(s) into {}:".format(len(built), WHEELS_OUT))
    for name in sorted(os.listdir(WHEELS_OUT)):
        if name.endswith(".whl"):
            print("   " + name)


if __name__ == "__main__":
    main(sys.argv[1:])
