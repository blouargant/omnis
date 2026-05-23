# Packaging

This directory holds the files the goreleaser pipeline pulls into the
distributable .deb / .rpm / .zip artifacts produced by `make package`.

## Layout on a packaged Linux install

```
/usr/bin/yoke                  CLI / REPL / TUI binary
/usr/bin/yoke-server           HTTP API + Web UI binary
/etc/yoke/                     editable configuration (config(noreplace))
    agents.json                agent list + model profiles + squad layout
    mcp_config.json            MCP server definitions
    permissions.json           tool permission rules
    preferences.json           UI preferences
    remote_registries.json     remote skill/agent registry endpoints
    a2a_config.json            remote A2A agent endpoints (empty by default)
    server.yaml                server listen address, token, A2A settings
    filters/                   bash output filter patterns
    registry/agents/           built-in agent definitions (read-only)
    registry/skills/           bundled skill playbooks (read-only)
/usr/share/yoke/web/           static UI assets served by yoke-server
/var/lib/yoke/softskills/      curator-managed soft-skill library (mutable)
/etc/profile.d/yoke.sh         exports YOKE_CONFIG_PATH and YOKE_WEB_DIR
/usr/share/doc/yoke/           LICENSE + README.md
```

Windows is currently disabled in `.goreleaser.yaml` because
[core/tools/bash.go](../core/tools/bash.go) uses Unix-only syscalls
(`syscall.Setpgid`, `syscall.Kill`). Once that file gains a platform-guarded
Windows variant, flip the `goos` lists in `.goreleaser.yaml` back on and a
`yoke_<version>_Windows_x86_64.zip` artifact will join the lineup.

## What lives where in this directory

- `etc-yoke/` — the contents of `/etc/yoke/` on a packaged install. The
  `agents.json` here mirrors `config/agents.json` with every relative path
  rewritten to its FHS location.
- `profile.d/yoke.sh` — sourced by login shells; tells both binaries where
  to find the system-wide config and web assets.

The `config/filters/` directory and the `skills/`, `web/`, `LICENSE`, and
`README.md` files are pulled directly from the source tree by goreleaser —
we don't duplicate them here.

## Building the packages

```
make package          # cross-compile + .deb + .rpm + .zip into dist/
```

See the top-level `Makefile` and `.goreleaser.yaml` for the full pipeline.

## Tagging and CI releases

The `.github/workflows/release.yml` workflow fires on every tag matching
`v*` and runs the same `goreleaser release` pipeline, publishing artifacts
to a GitHub Release.

| Tag form        | Kind            | Required source branch | Pre-release flag |
| --------------- | --------------- | ---------------------- | ---------------- |
| `vA.B.C`        | stable          | `main`                 | no               |
| `vA.B.C-rcN`    | release cand.   | `validation`           | yes              |
| `vA.B.C-betaN`  | beta            | `develop`              | yes              |

The hyphen before `rc` / `beta` is required: it makes the tag valid semver
(`1.2.3-rc1`), which is what goreleaser's parser expects.

The workflow's `validate` job rejects any tag whose commit isn't reachable
from the required branch — pushing `v1.2.3` from `develop`, for example,
fails the build before goreleaser runs. To make the policy harder to
bypass, also enable GitHub branch protection on `main` / `validation` /
`develop` and restrict who can push tags.

### Cutting a release

```bash
# stable, from main
git checkout main && git pull
git tag v1.2.3 && git push origin v1.2.3

# release candidate, from validation
git checkout validation && git pull
git tag v1.2.3-rc1 && git push origin v1.2.3-rc1

# beta, from develop
git checkout develop && git pull
git tag v1.2.3-beta1 && git push origin v1.2.3-beta1
```

The workflow takes ~2 minutes; artifacts land on
`https://github.com/<owner>/<repo>/releases/tag/<tag>`.
