# Installed by the omnis .deb / .rpm package.
# Points the omnis and omnis-server binaries at the system-wide configuration
# and static asset roots. Override any of these in your own shell rc / unit
# file to point at a per-user setup.
export OMNIS_CONFIG_PATH="${OMNIS_CONFIG_PATH:-/etc/omnis/agents.json}"
export OMNIS_WEB_DIR="${OMNIS_WEB_DIR:-/usr/share/omnis/web}"
