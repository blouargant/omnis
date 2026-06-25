"""Console-script entry points that run the bundled Go binaries.

Each entry point:

  1. resolves the bundled binary (``_dist/bin/omnis`` / ``omnis-server``);
  2. ensures it is executable (POSIX wheels can lose the bit);
  3. points omnis at the bundled assets — ``OMNIS_WEB_DIR`` (static UI) and
     ``OMNIS_SYSTEM_CONFIG_DIR`` (read-only system config layer) — *only* when
     the user hasn't already set them;
  4. seeds ``~/.omnis`` with the bundled config + registry on first run;
  5. replaces this process with the binary (``execv`` on POSIX; a child
     process whose exit code we propagate on Windows, which has no real exec).
"""

import os
import stat
import sys

from . import bin_dir, sysconf_dir, web_dir
from .seed import ensure_home_seeded


def _binary_path(name):
    exe = name + (".exe" if os.name == "nt" else "")
    path = os.path.join(bin_dir(), exe)
    if not os.path.isfile(path):
        sys.stderr.write(
            "omnis: bundled binary not found: {}\n"
            "This wheel may be corrupt or built for another platform.\n".format(path)
        )
        sys.exit(1)
    return path


def _ensure_executable(path):
    if os.name == "nt":
        return
    try:
        mode = os.stat(path).st_mode
        want = mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH
        if want != mode:
            os.chmod(path, want)
    except OSError:
        # Best-effort: if we can't chmod we still try to exec and let the OS
        # report a clear error.
        pass


def _prepare_env():
    """Inject the bundled-asset env defaults without clobbering user overrides."""
    if not os.environ.get("OMNIS_WEB_DIR", "").strip():
        os.environ["OMNIS_WEB_DIR"] = web_dir()
    if not os.environ.get("OMNIS_SYSTEM_CONFIG_DIR", "").strip():
        os.environ["OMNIS_SYSTEM_CONFIG_DIR"] = sysconf_dir()


def _seed_home():
    # Best-effort: a failure to seed (e.g. read-only home) must not stop the
    # binary from launching — it can still run off the bundled system layer.
    try:
        ensure_home_seeded(sysconf_dir())
    except Exception as exc:  # pragma: no cover - defensive
        sys.stderr.write("omnis: warning: could not seed ~/.omnis: {}\n".format(exc))


def _run(name):
    binary = _binary_path(name)
    _ensure_executable(binary)
    _prepare_env()
    _seed_home()

    argv = [binary] + sys.argv[1:]
    if os.name == "nt":
        import subprocess

        completed = subprocess.run(argv)
        sys.exit(completed.returncode)
    else:
        # Replace this Python process so signals / TTY / exit status pass
        # straight through to omnis (important for the interactive TUI/REPL).
        os.execv(binary, argv)


def main_omnis():
    """``omnis`` console command → the CLI / TUI / REPL binary."""
    _run("omnis")


def main_server():
    """``omnis-server`` console command → the HTTP API + Web UI binary."""
    _run("omnis-server")
