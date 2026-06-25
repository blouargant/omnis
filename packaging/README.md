# Packaging

This directory holds the files the goreleaser pipeline pulls into the
distributable .deb / .rpm / .zip artifacts produced by `make package`.

## Layout on a packaged Linux install

```
/usr/bin/omnis                  CLI / REPL / TUI binary
/usr/bin/omnis-server           HTTP API + Web UI binary
/etc/omnis/                     editable configuration (config(noreplace))
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
/usr/share/omnis/web/           static UI assets served by omnis-server
/var/lib/omnis/softskills/      curator-managed soft-skill library (mutable)
/etc/profile.d/omnis.sh         exports OMNIS_CONFIG_PATH and OMNIS_WEB_DIR
/usr/share/doc/omnis/           LICENSE + README.md
```

Windows is built. The Unix-only process-group syscalls (`Setpgid`, `Kill`)
that used to block it now live behind build tags in
[core/tools/bash_unix.go](../core/tools/bash_unix.go); the Windows binary
compiles its counterpart [core/tools/bash_windows.go](../core/tools/bash_windows.go)
instead (shell via `cmd.exe /C`, process-tree kill via `taskkill /T /F`).
`make package` therefore emits a `omnis_<version>_Windows_x86_64.zip` alongside
the Linux/macOS archives — that zip is the artifact the (forthcoming) WiX MSI
pipeline will unpack to source the binaries + `web/` assets.

> Caveat: the Bash tool's command strings are POSIX-shell oriented. On native
> Windows they run under `cmd.exe`, so sh builtins/pipelines may not behave
> identically — run omnis-server under WSL or Git-Bash if you need bash
> semantics. The server, web UI, A2A, and MCP wiring are unaffected.

## What lives where in this directory

- `etc-omnis/` — the contents of `/etc/omnis/` on a packaged install. The
  `agents.json` here mirrors `config/agents.json` with every relative path
  rewritten to its FHS location.
- `profile.d/omnis.sh` — sourced by login shells; tells both binaries where
  to find the system-wide config and web assets.

The `config/filters/` directory and the `skills/`, `web/`, `LICENSE`, and
`README.md` files are pulled directly from the source tree by goreleaser —
we don't duplicate them here.

## macOS — Homebrew tap

macOS is distributed as a **Homebrew formula** published to the
`blouargant/homebrew-tap` repository by the `brews:` block in
`.goreleaser.yaml`. Users install with:

```
brew install blouargant/tap/omnis
```

Layout on a `brew`-installed Mac (`$(brew --prefix)` is `/opt/homebrew` on
Apple Silicon, `/usr/local` on Intel):

```
$(brew --prefix)/bin/omnis              env-injecting wrapper → libexec/omnis
$(brew --prefix)/bin/omnis-server       env-injecting wrapper → libexec/omnis-server
$(brew --prefix)/share/omnis/web/       static Web UI assets
$(brew --prefix)/share/omnis/registry/  bundled agents + skills
$(brew --prefix)/share/omnis/*.json     bundled config defaults (agents.json, …)
$(brew --prefix)/share/omnis/filters/   bash output filter patterns
```

Because omnis embeds no defaults (it reads config/registry from disk), the
formula installs the bundled tree under `share/omnis` and the `bin/` wrappers
export `OMNIS_WEB_DIR` + `OMNIS_SYSTEM_CONFIG_DIR` so the binaries find it. The
latter relocates **only** the system layer of the config search chain — user
overrides in `~/.omnis` (and project-local `.agents/`) keep their higher
precedence, so `brew upgrade` refreshes the bundled defaults without ever
touching user config. `brew services start omnis` runs `omnis-server` via the
formula's `service` block (unauthenticated unless `OMNIS_SERVER_TOKEN` is set).

> Note: `brews` is marked deprecated by goreleaser (which now nudges toward
> `homebrew_casks`), so `make package-check` reports that deprecation. The
> formula is kept deliberately: a Homebrew **cask** cannot inject the runtime
> env vars the linked binaries need to locate the bundled (non-embedded)
> config/registry, nor run a `brew services` daemon. Publishing the tap
> requires a `HOMEBREW_TAP_GITHUB_TOKEN` secret (a PAT with write access to
> `blouargant/homebrew-tap`) in the release workflow.

## Windows — MSI installer

Windows ships a per-machine **MSI** built with the
[WiX Toolset](https://wixtoolset.org) from [windows/omnis.wxs](windows/omnis.wxs).
goreleaser (OSS) can't build MSI, so the `msi` job in
`.github/workflows/release.yml` runs on a `windows-latest` runner *after* the
goreleaser `release` job: it downloads the published
`omnis_<ver>_Windows_x86_64.zip`, stages it, and runs `wix build`. The MSI is
attached to the same GitHub Release. It builds on **stable tags only** — MSI
`ProductVersion` is strictly numeric (`x.y.z.0`) and can't carry an
`-rcN`/`-betaN` suffix.

Layout on a packaged Windows install (mirrors the Linux FHS split — binaries +
static UI read-only, config under the system data dir):

```
C:\Program Files\Omnis\omnis.exe          CLI / REPL binary
C:\Program Files\Omnis\omnis-server.exe   HTTP API + Web UI binary
C:\Program Files\Omnis\web\              static Web UI assets
C:\ProgramData\Omnis\*.json             bundled config defaults (agents.json, …)
C:\ProgramData\Omnis\server.yaml        server listen address, token, A2A settings
C:\ProgramData\Omnis\filters\           bash output filter patterns
C:\ProgramData\Omnis\registry\          bundled agents + skills
```

The installer sets three **machine** environment variables so a fresh shell
finds everything with no user setup:

| Variable | Value | Purpose |
|---|---|---|
| `PATH` (appended) | `C:\Program Files\Omnis\` | `omnis` / `omnis-server` on PATH |
| `OMNIS_WEB_DIR` | `C:\Program Files\Omnis\web` | locate the static Web UI |
| `OMNIS_SYSTEM_CONFIG_DIR` | `C:\ProgramData\Omnis` | system layer of the config chain |

`OMNIS_SYSTEM_CONFIG_DIR` relocates **only** the system layer, so per-user
overrides in `%USERPROFILE%\.omnis` (and project-local `.agents\`) keep their
higher precedence; the `ProgramData` tree is bundled defaults refreshed on
upgrade — same model as the Homebrew `share/omnis` tree above.

> Caveats / future work:
> - **No Windows Service yet.** `omnis-server.exe` isn't SCM-aware
>   (`golang.org/x/sys/windows/svc`), so the MSI installs binaries + assets +
>   PATH but doesn't register an auto-start service — run `omnis-server` from a
>   terminal (or wrap it with NSSM / a Scheduled Task). Adding `svc` support +
>   a WiX `ServiceInstall` is the natural next step.
> - **Bash tool** runs under `cmd.exe` on native Windows (see the build-tag
>   note above) — run under WSL for full POSIX-shell semantics.
> - **Code signing**: the MSI is unsigned, so SmartScreen will warn. An
>   Authenticode certificate + `signtool` removes the warning (not wired up).

## pip — PyPI wheels

A per-user, no-root install for anyone with Python 3.8+:

```
pip install omnis-agent        # or: pipx install omnis-agent
```

The PyPI distribution is **`omnis-agent`**; the console commands stay `omnis`
(CLI/TUI/REPL) and `omnis-server` (HTTP API + Web UI). Unlike the `.deb`/`.rpm`
(config in `/etc/omnis`, needs root), the pip install is per-user: the bundled
**config + registry are seeded into `~/.omnis`** on first run, so the files are
the user's to edit (and survive upgrades — existing files are never overwritten).

The packaging lives under [pip/](pip/):

```
pip/
├── pyproject.toml            # name=omnis-agent, console scripts, metadata
├── setup.py                  # forces a py3-none-<platform> binary wheel
├── README.md                 # PyPI long description
└── src/omnis/
    ├── __init__.py           # _dist/ path resolvers
    ├── launcher.py           # `omnis` / `omnis-server` entry points
    ├── seed.py               # `omnis-seed`: ~/.omnis materialisation
    └── _dist/                # staged at build time (git-ignored)
        ├── bin/{omnis,omnis-server}[.exe]
        ├── sysconf/          # config JSONs + filters/ + registry/  → seeds ~/.omnis
        └── web/              # static Web UI → OMNIS_WEB_DIR
```

How it works (no Go changes needed — it reuses the existing config search chain):

- The wheel bundles the **prebuilt Go binaries** plus the default
  config/registry/web tree as package data. Because the binaries are
  `CGO_ENABLED=0` static builds, **all six platform wheels cross-compile on a
  single Linux host** — there is no per-OS CI matrix.
- The Python launcher ([pip/src/omnis/launcher.py](pip/src/omnis/launcher.py))
  sets `OMNIS_WEB_DIR` (bundled `web/`) and `OMNIS_SYSTEM_CONFIG_DIR` (bundled
  `sysconf/`) **only when unset** (user overrides win), seeds `~/.omnis`
  ([pip/src/omnis/seed.py](pip/src/omnis/seed.py)), then `exec`s the real binary.
- `omnis-seed --force` re-copies the pristine bundled defaults into `~/.omnis`
  (or `--home <dir>`); seeding is automatic on first launch.

Platform wheels built (PEP 425 tags):

| Go target | wheel platform tag |
|---|---|
| linux/amd64 | `manylinux2014_x86_64` |
| linux/arm64 | `manylinux2014_aarch64` |
| darwin/amd64 | `macosx_10_13_x86_64` |
| darwin/arm64 | `macosx_11_0_arm64` |
| windows/amd64 | `win_amd64` |
| windows/arm64 | `win_arm64` |

Build locally:

```
make wheels                                  # all six wheels → dist/wheels/
make wheels WHEEL_PLATFORMS="linux/amd64"    # just one
```

[scripts/build_wheels.py](../scripts/build_wheels.py) does the staging +
cross-compile + `bdist_wheel` per platform and normalises the version to PEP 440
(`v1.2.3` → `1.2.3`, `v1.2.3-rc1` → `1.2.3rc1`). CI publishes them in the `pypi`
job of `.github/workflows/release.yml` via `twine upload` — requires a
**`PYPI_API_TOKEN`** repo secret. rc/beta tags publish as PEP 440 pre-releases
(pip ignores them by default), mirroring goreleaser's `prerelease: auto`.

## Building the packages

```
make package          # cross-compile + .deb + .rpm + .zip + brew formula into dist/
make wheels           # cross-compile + per-platform pip wheels into dist/wheels/
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
