---
name: review
description: Review a body of work (source code, configuration, documents, infra manifests, …) for correctness, clarity, and obvious risks. Use this skill whenever the user asks for a review, audit or critique.
---

# Review

This skill is **language- and domain-agnostic**. The procedure is the same
whether you are reviewing Go code, a Kubernetes manifest, a Terraform
module, a SQL migration, a markdown spec, or a Dockerfile.

## Procedure

1. **Discover scope.** Use `Glob` (or the equivalent listing tool from a
   mounted MCP server) to enumerate the candidate artefacts. If the user
   gave a path, use it; otherwise ask.
2. **Read each artefact in full** before commenting on it. Do not review
   from filename alone.
3. **Score per finding** with:
   - location (`file:line` or resource id)
   - severity: `info` | `warn` | `error`
   - rationale (≤ 2 sentences, evidence-based)
   - suggested fix (concrete: a diff, a command, or a config change)
4. **Group findings** by severity at the end and propose the smallest
   reversible next step.

## Universal red flags to look for

- Missing error / failure handling
- Resource lifecycle (leaks, orphans, missing teardown)
- Untrusted input flowing into a sink (command, query, path, URL)
- Secrets in plaintext
- Permission scope wider than necessary
- Race conditions / unbounded fan-out
- Implicit dependencies on environment that is not declared

## Output rule

Always finish with a single line: `Overall: ok | needs-changes | blocked`.
