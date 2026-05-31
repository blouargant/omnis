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

Windows is built. The Unix-only process-group syscalls (`Setpgid`, `Kill`)
that used to block it now live behind build tags in
[core/tools/bash_unix.go](../core/tools/bash_unix.go); the Windows binary
compiles its counterpart [core/tools/bash_windows.go](../core/tools/bash_windows.go)
instead (shell via `cmd.exe /C`, process-tree kill via `taskkill /T /F`).
`make package` therefore emits a `yoke_<version>_Windows_x86_64.zip` alongside
the Linux/macOS archives — that zip is the artifact the (forthcoming) WiX MSI
pipeline will unpack to source the binaries + `web/` assets.

> Caveat: the Bash tool's command strings are POSIX-shell oriented. On native
> Windows they run under `cmd.exe`, so sh builtins/pipelines may not behave
> identically — run yoke-server under WSL or Git-Bash if you need bash
> semantics. The server, web UI, A2A, and MCP wiring are unaffected.

## What lives where in this directory

- `etc-yoke/` — the contents of `/etc/yoke/` on a packaged install. The
  `agents.json` here mirrors `config/agents.json` with every relative path
  rewritten to its FHS location.
- `profile.d/yoke.sh` — sourced by login shells; tells both binaries where
  to find the system-wide config and web assets.

The `config/filters/` directory and the `skills/`, `web/`, `LICENSE`, and
`README.md` files are pulled directly from the source tree by goreleaser —
we don't duplicate them here.

## macOS — Homebrew tap

macOS is distributed as a **Homebrew formula** published to the
`blouargant/homebrew-tap` repository by the `brews:` block in
`.goreleaser.yaml`. Users install with:

```
brew install blouargant/tap/yoke
```

Layout on a `brew`-installed Mac (`$(brew --prefix)` is `/opt/homebrew` on
Apple Silicon, `/usr/local` on Intel):

```
$(brew --prefix)/bin/yoke              env-injecting wrapper → libexec/yoke
$(brew --prefix)/bin/yoke-server       env-injecting wrapper → libexec/yoke-server
$(brew --prefix)/share/yoke/web/       static Web UI assets
$(brew --prefix)/share/yoke/registry/  bundled agents + skills
$(brew --prefix)/share/yoke/*.json     bundled config defaults (agents.json, …)
$(brew --prefix)/share/yoke/filters/   bash output filter patterns
```

Because yoke embeds no defaults (it reads config/registry from disk), the
formula installs the bundled tree under `share/yoke` and the `bin/` wrappers
export `YOKE_WEB_DIR` + `YOKE_SYSTEM_CONFIG_DIR` so the binaries find it. The
latter relocates **only** the system layer of the config search chain — user
overrides in `~/.yoke` (and project-local `.agents/`) keep their higher
precedence, so `brew upgrade` refreshes the bundled defaults without ever
touching user config. `brew services start yoke` runs `yoke-server` via the
formula's `service` block (unauthenticated unless `YOKE_SERVER_TOKEN` is set).

> Note: `brews` is marked deprecated by goreleaser (which now nudges toward
> `homebrew_casks`), so `make package-check` reports that deprecation. The
> formula is kept deliberately: a Homebrew **cask** cannot inject the runtime
> env vars the linked binaries need to locate the bundled (non-embedded)
> config/registry, nor run a `brew services` daemon. Publishing the tap
> requires a `HOMEBREW_TAP_GITHUB_TOKEN` secret (a PAT with write access to
> `blouargant/homebrew-tap`) in the release workflow.

## Windows — MSI installer

Windows ships a per-machine **MSI** built with the
[WiX Toolset](https://wixtoolset.org) from [windows/yoke.wxs](windows/yoke.wxs).
goreleaser (OSS) can't build MSI, so the `msi` job in
`.github/workflows/release.yml` runs on a `windows-latest` runner *after* the
goreleaser `release` job: it downloads the published
`yoke_<ver>_Windows_x86_64.zip`, stages it, and runs `wix build`. The MSI is
attached to the same GitHub Release. It builds on **stable tags only** — MSI
`ProductVersion` is strictly numeric (`x.y.z.0`) and can't carry an
`-rcN`/`-betaN` suffix.

Layout on a packaged Windows install (mirrors the Linux FHS split — binaries +
static UI read-only, config under the system data dir):

```
C:\Program Files\Yoke\yoke.exe          CLI / REPL binary
C:\Program Files\Yoke\yoke-server.exe   HTTP API + Web UI binary
C:\Program Files\Yoke\web\              static Web UI assets
C:\ProgramData\Yoke\*.json             bundled config defaults (agents.json, …)
C:\ProgramData\Yoke\server.yaml        server listen address, token, A2A settings
C:\ProgramData\Yoke\filters\           bash output filter patterns
C:\ProgramData\Yoke\registry\          bundled agents + skills
```

The installer sets three **machine** environment variables so a fresh shell
finds everything with no user setup:

| Variable | Value | Purpose |
|---|---|---|
| `PATH` (appended) | `C:\Program Files\Yoke\` | `yoke` / `yoke-server` on PATH |
| `YOKE_WEB_DIR` | `C:\Program Files\Yoke\web` | locate the static Web UI |
| `YOKE_SYSTEM_CONFIG_DIR` | `C:\ProgramData\Yoke` | system layer of the config chain |

`YOKE_SYSTEM_CONFIG_DIR` relocates **only** the system layer, so per-user
overrides in `%USERPROFILE%\.yoke` (and project-local `.agents\`) keep their
higher precedence; the `ProgramData` tree is bundled defaults refreshed on
upgrade — same model as the Homebrew `share/yoke` tree above.

> Caveats / future work:
> - **No Windows Service yet.** `yoke-server.exe` isn't SCM-aware
>   (`golang.org/x/sys/windows/svc`), so the MSI installs binaries + assets +
>   PATH but doesn't register an auto-start service — run `yoke-server` from a
>   terminal (or wrap it with NSSM / a Scheduled Task). Adding `svc` support +
>   a WiX `ServiceInstall` is the natural next step.
> - **Bash tool** runs under `cmd.exe` on native Windows (see the build-tag
>   note above) — run under WSL for full POSIX-shell semantics.
> - **Code signing**: the MSI is unsigned, so SmartScreen will warn. An
>   Authenticode certificate + `signtool` removes the warning (not wired up).

## Building the packages

```
make package          # cross-compile + .deb + .rpm + .zip + brew formula into dist/
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
