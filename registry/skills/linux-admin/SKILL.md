---
name: linux-admin
description: Safely make a change to a Linux workstation — install/remove packages, manage systemd services and timers, edit and validate configuration files, manage users/permissions and networking. Detect the distro and package manager, determine whether the target is package-managed or hand-maintained, preview or simulate every change, back up configs before editing, and prefer the package manager and drop-in overrides so the host stays consistent and secure. Use whenever a change to the local Linux host is required (install, remove, enable, disable, start, stop, edit a config, add a user, change permissions or networking).
metadata:
  author: blouargant@chapsvision.com
  tags: "linux, sysadmin, systemd, packages, apt, dnf, pacman, configuration, playbook"
---

# Linux Safe Administration

The *playbook* for CHANGING a Linux workstation with the smallest blast radius,
so the host stays consistent (the package manager still owns what it owns) and no
less secure than before. Read this before any package install/remove, service
change, config edit, or user/permission/network change.

Every mutating command is **permission-gated** — it prompts the user for
confirmation before it runs. Do not try to work around the prompt. If a call is
denied, report it and stop; never retry with a different flag or identity.

## Phase 1 — Establish the target and the environment

1. **Pin the target:** the *exact* intended change (package + version, unit name,
   config file + setting, user, rule) and the end state you expect.
2. **Detect the package manager and init system** (read-only):

   ```bash
   command -v apt-get dnf yum pacman zypper apk 2>/dev/null   # which package manager
   cat /etc/os-release                                        # distro + version
   ps -p 1 -o comm=                                           # init system (expect systemd)
   ```

3. **Note the privilege required.** Most changes need `sudo`. If sudo is
   unavailable or denied, stop and report — do not switch identity.

## Phase 2 — Determine who OWNS the target

Before editing a file or replacing a unit, learn whether a package owns it:

```bash
dpkg -S /etc/ssh/sshd_config     # Debian/Ubuntu: which package owns this path
rpm -qf /etc/ssh/sshd_config     # RHEL/Fedora/SUSE: which package owns this path
```

- **Package-managed file** — prefer a **drop-in override** to editing the file in
  place, so a package upgrade does not clobber your change and dpkg/rpm does not
  flag it as modified:
  - systemd units → `systemctl edit <unit>` (writes `…/<unit>.d/override.conf`),
    never hand-edit the shipped unit under `/usr/lib/systemd/system`.
  - daemons with a conf.d → drop a file in `/etc/<svc>/conf.d/` (or the
    program's documented `*.d/` directory) rather than editing the main file.
- **Hand-maintained file** — edit in place, but **back it up first** (below).

## Phase 3 — Preview before you change (always)

Prefer previews/validators that do **not** mutate:

```bash
# Package changes — simulate, don't apply
apt-get -s install <pkg>          # Debian/Ubuntu dry-run
dnf --assumeno install <pkg>      # Fedora/RHEL: answer "no", show the transaction
pacman -p -S <pkg>                # Arch: print target only

# Config validators — run the tool's own checker
visudo -c                         # sudoers syntax
sshd -t                           # sshd_config
nginx -t                          # nginx
systemd-analyze verify <unit>     # a systemd unit
```

**Back up any config file before editing** — copy it to `<file>.bak` (or rely on
the `revert` tool's snapshotting so the edit is revertible). Never edit a config
without a way back.

## Phase 4 — Apply the smallest change

- Change **only** what the goal requires. Prefer the package manager and drop-in
  overrides to editing managed files.
- Re-validate after editing a config **before** restarting the service it feeds
  (`sshd -t` / `nginx -t` / `visudo -c`), so a syntax error is caught before it
  takes a daemon down.
- For services, after a config change: `systemctl daemon-reload` (if a unit or
  drop-in changed) then `systemctl reload <unit>` where reload is supported,
  falling back to `restart` only when it is not.

## Phase 5 — Verify and report

- Re-run the validator; check the end state:
  `systemctl status <unit>`, `systemctl is-enabled <unit>`,
  `systemctl is-active <unit>`, `dpkg -l <pkg>` / `rpm -q <pkg>`.
- Report: the environment (package manager + init) and ownership determination,
  the strategy chosen and why, the exact commands run, the preview/diff, and the
  post-change health. Cite command + output.

## Hard rules

1. **Preview / back up before every real change** — no exceptions.
2. **Never run catastrophic operations** — `rm -rf /` or any system directory,
   `dd` to a block device, `mkfs`, redirecting onto `/dev/sd*`/`/dev/nvme*`,
   recursive `chmod`/`chown` on system directories. (The Bash safety floor also
   blocks the worst; never attempt to route around it.)
3. **Never disable host security** — firewall (`ufw`/`firewalld`/`iptables`),
   SELinux, or AppArmor — without an explicit user override.
4. **Never `delete` / `purge` / `userdel` / format without explicit user
   confirmation** — those are destructive and gated.
5. **Smallest change.** Prefer the package manager + drop-in overrides over
   editing package-managed files; keep existing settings you were not asked to
   change.
6. **Privilege denial → report and escalate**, do not retry as a different
   identity.

## Output rule

Always finish with a single line:
`Result: applied | previewed | blocked | needs-confirmation`.
