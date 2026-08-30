# Installed by the omnis .deb / .rpm package.
# Points the omnis and omnis-server binaries at the static Web UI asset root.
# Override this in your own shell rc / unit file if you relocate the assets.
#
# Deliberately does NOT set OMNIS_CONFIG_PATH: that variable is the explicit-
# file bypass (agent/runtime_config.go loadRuntimeConfig) — it reads
# agents.json verbatim from a single path instead of merging omnis's
# documented 3-layer config search chain (.agents > $HOME/.omnis >
# /etc/omnis). This package already installs the system config at
# /etc/omnis, which is the chain's built-in lowest-precedence layer
# (paths.SystemConfigDir's default), so no override is needed to find it —
# and setting OMNIS_CONFIG_PATH here would silently disable every per-user
# override in $HOME/.omnis/agents.json (agents list, squads, router_squad,
# turn_budget, embed_model_ref, eval_model_ref, serper_key, …) for the whole
# machine, with no error anywhere. See CLAUDE.md's Configuration files /
# Distribution sections. If you deliberately need the old single-file
# behavior, export OMNIS_CONFIG_PATH yourself in your own shell rc — do not
# reintroduce it here.
export OMNIS_WEB_DIR="${OMNIS_WEB_DIR:-/usr/share/omnis/web}"
