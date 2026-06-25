"""Build glue for the ``omnis-agent`` wheels.

The wheel ships the prebuilt Go binaries (``omnis`` + ``omnis-server``) plus the
bundled config/registry/web tree as package data — it is **not** a CPython
extension. We therefore:

  * mark the distribution as non-pure / platform-specific so ``bdist_wheel``
    emits a platform tag instead of ``py3-none-any``;
  * force the ABI tag to ``none`` (we don't link against a Python ABI), giving
    ``py3-none-<platform>``;
  * take the wheel's platform tag from ``--plat-name`` on the command line
    (scripts/build_wheels.py passes the right tag per Go target);
  * compute ``package_data`` by walking the staged ``src/omnis/_dist`` tree, so
    every binary/asset is included regardless of the setuptools version's
    recursive-glob support.

Version comes from the ``OMNIS_WHEEL_VERSION`` env var (PEP 440 normalised by
the build script), defaulting to a dev version for ad-hoc local builds.
"""

import os

from setuptools import setup
from setuptools.dist import Distribution

# bdist_wheel moved into setuptools (>=70); fall back to the standalone wheel
# package for older toolchains.
try:  # pragma: no cover - import shim
    from setuptools.command.bdist_wheel import bdist_wheel as _bdist_wheel
except ImportError:  # pragma: no cover - import shim
    from wheel.bdist_wheel import bdist_wheel as _bdist_wheel


HERE = os.path.dirname(os.path.abspath(__file__))
DIST_DIR = os.path.join(HERE, "src", "omnis", "_dist")


class BinaryDistribution(Distribution):
    """A distribution that always produces a platform-specific wheel."""

    def has_ext_modules(self):  # noqa: D401 - setuptools hook
        return True

    def is_pure(self):  # noqa: D401 - setuptools hook
        return False


class bdist_wheel(_bdist_wheel):
    """Emit ``py3-none-<platform>`` rather than ``cp3x-cp3x-<platform>``."""

    def finalize_options(self):
        super().finalize_options()
        # The tree is not pure-Python (it carries native binaries), so the
        # wheel must be tagged for a specific platform.
        self.root_is_pure = False

    def get_tag(self):
        # Keep whatever platform tag --plat-name resolved to, but advertise the
        # wheel as compatible with any Python 3 and no specific ABI.
        _python, _abi, plat = super().get_tag()
        return "py3", "none", plat


def _staged_data_files():
    """Relative paths (under the ``omnis`` package) of every staged asset."""
    if not os.path.isdir(DIST_DIR):
        # No payload staged yet (e.g. a metadata-only invocation). Returning an
        # empty list keeps `pip`/`build` introspection from failing; the real
        # build always stages _dist first via scripts/build_wheels.py.
        return []
    pkg_root = os.path.join(HERE, "src", "omnis")
    out = []
    for root, _dirs, files in os.walk(DIST_DIR):
        for name in files:
            abs_path = os.path.join(root, name)
            out.append(os.path.relpath(abs_path, pkg_root))
    return out


setup(
    version=os.environ.get("OMNIS_WHEEL_VERSION", "0.0.0.dev0"),
    distclass=BinaryDistribution,
    cmdclass={"bdist_wheel": bdist_wheel},
    package_data={"omnis": _staged_data_files()},
)
