# `linux_admin` change-executor agent (System squad)

**Date:** 2026-07-21
**Surface:** config + registry only (no Go changes; hot-reloadable)
**Status:** design approved, implemented

## Problem

A user wants an agent that "knows how to administer a Linux computer and can help
a user administer their workstation," added as a **non-builtin member of the
default System squad**.

The System squad's **leader** already coordinates OS/host administration (its
description covers shell, package management, services/processes, filesystem/log
inspection, config files, cron/systemd, networking), and the squad already has a
read-only **investigator**. So the new agent must fill a *distinct* niche rather
than duplicate the leader.

## Decisions (from brainstorming)

1. **Role: careful change executor ("the hands").** The leader diagnoses and
   briefs; this agent determines *how* to make a change safely and carries it
   out. Read-only evidence gathering stays with the investigator. This is the
   local-host analog of `k8s_editor`.
2. **Safety encoded as a dedicated skill** (`linux-admin`), loaded first each
   turn — the codebase's established pattern (mirrors `k8s-modification` /
   `k8s-triage`). Keeps the instruction short and the playbook reusable +
   hot-reloadable.
3. **Model tier: `balanced`** (~$0.26/$1.58 per M tok) — more headroom than
   `hosted` for the wide surface (distros, package managers, systemd,
   networking), still far cheaper than `high`/`premium`.
4. **One skill, not two.** k8s splits *triage* (decide) from *modification*
   (do); here the deciding stays with the System leader, so only the *change
   playbook* is needed. No separate "linux-triage" decision skill.

## Prior art (what we mirror)

- **Agent** `registry/agents/k8s_editor/{agent.json,instruction.md}` — a
  non-builtin change specialist: `model_ref` + `skills` + the `fs`-family tool
  list; short instruction that says "load the playbook skill first, confirm the
  target, preview every change, mutations are confirmation-gated, verify &
  report."
- **Skill** `registry/skills/k8s-modification/SKILL.md` — a phased,
  preview-first change playbook with a **Hard rules** section and a final
  single-line `Result:` output rule.
- **Skill permissions sidecar** `registry/skills/k8s-triage/permissions.json` —
  `permissions.allow` of read-only command prefixes + `permissions.ask` regexes
  for mutating verbs (`{regex, reason, tools:["Bash"]}`).
- **Wiring** `config/agents.json` — the `agents` name list + each squad's
  `members` list. Auto-discovered by `agent.NewAgent()`; no Go change.

## Design

### A. Agent — `registry/agents/linux_admin/`

`agent.json`:
- `model_ref`: **balanced**
- `builtin`: false, `enabled`: true, `leader`: false, `allow_file_attachments`: true
- `skills`: `["linux-admin"]`
- `tools`: `Bash, Read, Write, Edit, Grep, Glob, Skill, revert, mime, mcp, softskills, calc, bg`
  - Tool set = `k8s_editor`'s set **plus `bg`** (Linux admin routinely runs long
    ops — `apt upgrade`, service restarts to watch, log tails — and `bg` lets it
    background/monitor them without blocking the turn).
- `max_instances`: 1 (a workstation change agent should not fan out; parallel
  mutations to one host are a footgun). `resumable_sessions` absent ⇒ default on.

**Description** (what the leader sees; scopes the delegation): careful Linux
workstation change specialist — installs/removes packages, manages systemd
services/timers, edits and validates config files, manages users/permissions and
networking; detects the distro/package manager, previews or simulates every
change, backs up configs before editing, and every mutating command is
permission-gated. Does not diagnose from scratch — the leader briefs the target
and intended change.

`instruction.md` (short, mirrors `k8s_editor`): you are a Linux change
specialist; the leader briefs you with the target + intended change; **load the
`linux-admin` skill first and follow it**; confirm the target and required
privilege (sudo) before acting; base every change on evidence, not assumptions;
mutations are confirmation-gated (don't try to bypass the prompt; a denied call
→ report and stop, don't retry as another identity); list missing info under
"open questions" for the leader (no `teammate_ask`); verify after applying and
report exact commands + output in fenced blocks; professional/direct tone.

### B. Skill — `registry/skills/linux-admin/`

`SKILL.md` (YAML frontmatter `name: linux-admin`, `description:` covering
install/remove/service/config/user/network changes so `load_skill` triggers,
`metadata.tags`). Phased, distro-agnostic (detect & adapt) playbook:

- **Phase 1 — Establish target & environment.** Detect the package manager
  (`apt`/`dnf`/`yum`/`pacman`/`zypper`/`apk`) and init system (systemd primary).
  Pin the exact intended change. Note whether it needs `sudo`.
- **Phase 2 — Determine ownership.** Is the target file/unit **package-managed**
  (`dpkg -S`, `rpm -qf`) or hand-maintained? Prefer the package manager and
  **drop-in overrides** (`systemctl edit`, `/etc/*/conf.d/`) over clobbering a
  package-managed file.
- **Phase 3 — Preview before every change (always).** Simulate where possible
  (`apt-get -s`, `dnf --assumeno`, `pacman -p`); validate configs with the
  tool's own checker (`visudo -c`, `sshd -t`, `nginx -t`, `systemd-analyze
  verify`); **back up any config file before editing** (`.bak`, or the `revert`
  tool).
- **Phase 4 — Apply the smallest change.** Touch the fewest things; prefer
  overrides to edits of managed files; re-validate before restarting a service.
- **Phase 5 — Verify & report.** Re-run the validator; `systemctl status` /
  `is-enabled` / `is-active`; confirm the intended end state. Report the
  environment/ownership determination, the strategy chosen and why, the exact
  commands run, the preview/diff, and the post-change health — cite command +
  output.
- **Hard rules** (non-negotiable): preview/backup before every change; never run
  catastrophic ops (`rm -rf /`, `dd` to a disk, `mkfs`, `>` onto a block device,
  recursive `chmod`/`chown` on system dirs — the Bash safety floor also blocks
  the worst); never disable host security (firewall/SELinux/AppArmor) without an
  explicit override; never `delete`/`purge`/`userdel`/format without explicit
  confirmation; smallest change; privilege denial → report and escalate, don't
  retry as another identity.
- **Output rule:** finish with a single line
  `Result: applied | previewed | blocked | needs-confirmation`.

`permissions.json` sidecar (same shape as `k8s-triage/permissions.json`):
- `permissions.allow` — safe read-only inspection prefixes (`systemctl status|
  is-*|list-*|show|cat`, `journalctl`, `dpkg -l|-L|-S`, `dpkg-query`,
  `apt list`, `apt-cache`, `apt-get -s`, `rpm -q*`, `pacman -Q*`, `ss`,
  `ip route|addr|link show`, `sysctl -a`, `df`, `free`, `lsblk`, `lscpu`,
  `lsmod`, `uname`, `id`, `hostnamectl status`, `timedatectl status`).
- `permissions.ask` — mutating verbs via `{regex, reason, tools:["Bash"]}`:
  package install/remove/upgrade across apt/dnf/yum/pacman/zypper/apk;
  `systemctl start|stop|restart|reload|enable|disable|mask|unmask|edit`;
  user/group/credential (`useradd|usermod|userdel|groupadd|…|passwd`);
  `ip … add|del|change|replace|set|flush`; firewall
  (`ufw|firewall-cmd|iptables|nft`); `mount|umount`; `sysctl -w`; disk ops
  (`mkfs|mkswap|parted|fdisk|…`).
- Catastrophic patterns are left to the shipped Bash safety-floor deny.

No `requires.json` — apt/dnf/systemctl/etc. are OS-native and assumed present.

### C. Wiring & housekeeping

- `config/agents.json`: `"linux_admin"` added to the `agents` list and the
  **System** squad's `members`.
- **CLAUDE.md**: `linux_admin` added under the System squad in the agent-topology
  diagram (self-maintenance rule).
- **internal/features/FEATURES.md**: one bullet under the current
  in-development minor (1.9).
- No Go code, no build/test infra changes.

## Verification (done)

- `make build` clean; `make test` exit 0 / 0 failures; JSON valid.
- In-process `ResolveRuntimeSettings` + `BuildInstance` resolve the System squad
  as `[investigator, summariser, helper, linux_admin]`.
- Running-server smoke: `/api/squads` lists `linux_admin` in the System squad
  with its description; no boot errors. (Smoke must run with `env -u
  OMNIS_CONFIG_PATH` — that env var, exported in the dev shell, is an explicit
  single-file config bypass that otherwise shadows `OMNIS_SYSTEM_CONFIG_DIR`.)

## Out of scope / non-goals

- No separate "linux-triage" decision skill (deciding stays with the leader).
- No `requires.json` (native OS tooling assumed present).
- Not a leader; not fan-out (`max_instances: 1`).
- No new tool groups or Go wiring — purely config + registry + docs.

## Deployment note

The dev machine's running omnis reads `/etc/omnis` (its shell exports
`OMNIS_CONFIG_PATH=/etc/omnis/agents.json`), so these repo changes only take
effect there once deployed into `/etc/omnis` (`config/agents.json` →
`/etc/omnis/agents.json`; `registry/{agents/linux_admin,skills/linux-admin}` →
`/etc/omnis/registry/...`). There is no `make install` target.
