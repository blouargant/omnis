"""Python launcher package for the ``omnis-agent`` distribution.

This package contains no application logic — omnis is a Go program. The wheel
bundles the prebuilt ``omnis`` / ``omnis-server`` binaries plus the default
config/registry/web tree under :data:`_dist`, and the console-script entry
points (:mod:`omnis.launcher`) exec the right binary after pointing it at the
bundled assets and seeding ``~/.omnis`` (:mod:`omnis.seed`).
"""

import os

__all__ = ["dist_dir", "bin_dir", "sysconf_dir", "web_dir"]

# Populated from the wheel version at build time (setup.py). The string here is
# only the fallback for source checkouts / editable installs.
__version__ = "0.0.0.dev0"


def dist_dir():
    """Absolute path to the bundled payload directory (``_dist``)."""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "_dist")


def bin_dir():
    """Directory holding the bundled ``omnis`` / ``omnis-server`` binaries."""
    return os.path.join(dist_dir(), "bin")


def sysconf_dir():
    """Bundled system-config layer (matches the OMNIS_SYSTEM_CONFIG_DIR contract).

    Holds the default config JSONs (``filters/`` and ``registry/`` get seeded
    into ``~/.omnis`` on first run — see :data:`omnis.seed.SEED_TREES`) plus
    ``hooks/``, which does NOT get seeded: ``hooks.json``'s command always
    names the hook script via ``${OMNIS_SYSTEM_CONFIG_DIR}``, never a layered
    path, so a copy under ``~/.omnis/hooks/`` would just be dead weight that
    silently has no effect if a user edited it.
    """
    return os.path.join(dist_dir(), "sysconf")


def web_dir():
    """Bundled static Web UI assets (pointed at via OMNIS_WEB_DIR)."""
    return os.path.join(dist_dir(), "web")
