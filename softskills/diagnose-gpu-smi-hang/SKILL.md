---
name: diagnose-gpu-smi-hang
description: Diagnose nvidia-smi hangs in Kubernetes pods by systematically checking GPU configuration, daemonset status, and driver library paths.
---

# Diagnose GPU SMI Hangs

## Context

When `nvidia-smi` commands hang or timeout in Kubernetes pods with GPU resources, the issue often stems from misconfigured GPU management components rather than the pod itself. This skill provides a systematic approach to identify root causes including daemonset failures, library path mismatches, and driver version conflicts.

## Steps

1. **Verify pod identity and namespace** - Confirm the correct pod name and namespace; pod names may differ from expected values due to deployment naming conventions.

2. **Run baseline GPU diagnostics** - Execute `nvidia-smi -L` first to check if basic GPU listing works; if it succeeds but full `nvidia-smi` hangs, the issue is likely in extended initialization or vGPU management.

3. **Check GPU resource annotations** - Inspect pod annotations for GPU-related configuration, resource limits, and vGPU assignments.

4. **Verify node GPU status** - Check the underlying node's GPU health, driver version, and device mount points.

5. **Run nvidia-smi with timeout** - Execute `timeout <seconds> nvidia-smi` to confirm the hang and measure actual response time; distinguish between slow initialization (~5s) and true hangs.

6. **Inspect GPU management daemonsets** - Check status of GPU-related daemonsets (e.g., gpu-manager) in relevant namespaces; look for CrashLoopBackOff, numberReady: 0, or other failure states.

7. **Review daemonset logs** - Examine logs from failing GPU management pods for errors about library paths, driver versions, or mount failures.

8. **Compare expected vs actual paths** - Identify mismatches between where the daemonset expects NVIDIA libraries (e.g., `/usr/lib/x86_64-linux-gnu`) and where they actually exist on the host.

9. **Check DRIVER_VERSION configuration** - Verify the DRIVER_VERSION environment variable in daemonset specs matches the actual installed driver version.

10. **Document findings and pending fixes** - Record the root cause (e.g., daemonset crash due to path mismatch) and specific remediation steps (e.g., create symlink, update DRIVER_VERSION).

## Constraints

- Do not assume the pod itself is misconfigured; the root cause often lies in cluster-level GPU management components.
- Do not skip the `nvidia-smi -L` baseline test; it helps distinguish between basic GPU access issues and extended initialization problems.
- Do not ignore daemonset status even if the pod appears healthy; GPU management daemonsets operate at the node level.
- Avoid restarting the affected pod before checking daemonset health; this rarely resolves daemonset-level issues.

## Validation

- Step 2 succeeds when `nvidia-smi -L` returns GPU list within 2 seconds.
- Step 6 succeeds when you can confirm daemonset status (Ready or failing with specific error).
- Step 8 succeeds when you can identify any path mismatches or confirm paths are correct.
- Final validation: root cause is identified with specific evidence (log lines, path comparisons, version mismatches) and remediation steps are documented.
