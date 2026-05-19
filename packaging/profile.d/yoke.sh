# Installed by the yoke .deb / .rpm package.
# Points the yoke and yoke-server binaries at the system-wide configuration
# and static asset roots. Override any of these in your own shell rc / unit
# file to point at a per-user setup.
export YOKE_CONFIG_PATH="${YOKE_CONFIG_PATH:-/etc/yoke/agent.yaml}"
export YOKE_WEB_DIR="${YOKE_WEB_DIR:-/usr/share/yoke/web}"
