"""Seed omnis's per-user config home (``~/.omnis``) from the bundled defaults.

``pip install omnis-agent`` lands the default config + registry inside the wheel
(read-only). On first run the launcher copies them into the user's home so the
files are owned and editable by the user — omnis's per-user config layer
(``$OMNIS_HOME`` / ``~/.omnis``), which takes precedence over the bundled system
layer. Existing files are never overwritten, so user edits survive upgrades;
``omnis-seed --force`` re-copies the pristine defaults on demand.
"""

import argparse
import os
import shutil
import sys

# Subdirectories under the bundled sysconf/ that are copied wholesale (kept in
# sync with scripts/build_wheels.py staging and the OMNIS_SYSTEM_CONFIG_DIR
# contract in internal/paths/paths.go).
SEED_TREES = ("filters", "registry")


def home_dir(explicit=None):
    """Resolve omnis's writable state root: explicit > $OMNIS_HOME > ~/.omnis."""
    if explicit:
        return os.path.abspath(os.path.expanduser(explicit))
    env = os.environ.get("OMNIS_HOME", "").strip()
    if env:
        return os.path.abspath(os.path.expanduser(env))
    return os.path.join(os.path.expanduser("~"), ".omnis")


def _copy_file(src, dst, force):
    if os.path.exists(dst) and not force:
        return False
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copy2(src, dst)
    return True


def ensure_home_seeded(src_sysconf, home=None, force=False):
    """Copy the bundled defaults under ``src_sysconf`` into ``home``.

    Returns the number of files written. Files (and the directory trees in
    :data:`SEED_TREES`) that already exist in ``home`` are left untouched unless
    ``force`` is set. Missing or empty source trees are skipped silently so the
    function is a no-op when there is nothing to seed.
    """
    home = home or home_dir()
    if not os.path.isdir(src_sysconf):
        return 0

    written = 0
    os.makedirs(home, exist_ok=True)

    # Top-level config files (agents.json, models.json, server.yaml, …).
    for name in sorted(os.listdir(src_sysconf)):
        src = os.path.join(src_sysconf, name)
        if os.path.isfile(src):
            if _copy_file(src, os.path.join(home, name), force):
                written += 1

    # Whole subtrees (filters/, registry/agents, registry/skills).
    for tree in SEED_TREES:
        src_tree = os.path.join(src_sysconf, tree)
        if not os.path.isdir(src_tree):
            continue
        for root, _dirs, files in os.walk(src_tree):
            rel = os.path.relpath(root, src_sysconf)
            for fname in files:
                src = os.path.join(root, fname)
                dst = os.path.join(home, rel, fname)
                if _copy_file(src, dst, force):
                    written += 1
    return written


def main(argv=None):
    """Entry point for the ``omnis-seed`` console command."""
    from . import sysconf_dir

    parser = argparse.ArgumentParser(
        prog="omnis-seed",
        description="Materialise omnis's bundled default config + registry into "
        "the per-user config home (~/.omnis). Run automatically on first use; "
        "use --force to refresh the pristine defaults.",
    )
    parser.add_argument(
        "--home",
        help="Target config home (default: $OMNIS_HOME or ~/.omnis).",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Overwrite existing files with the bundled defaults.",
    )
    args = parser.parse_args(argv)

    target = home_dir(args.home)
    count = ensure_home_seeded(sysconf_dir(), home=target, force=args.force)
    verb = "Refreshed" if args.force else "Seeded"
    print("{} {} file(s) into {}".format(verb, count, target))
    return 0


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
