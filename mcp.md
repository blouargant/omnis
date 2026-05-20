---
name: tokensave
description: >
  Code-graph MCP server — indexes a project into a SQLite graph and gives Claude 37 tools for
  symbol search, call-graph traversal, impact analysis, and code quality checks. Use for any
  structural question about a codebase: finding callers/callees, computing change blast radius,
  detecting god classes, mapping test coverage, and more.
command: tokensave
args:
  - serve
  - --path
  - /path/to/your/project
skills:
  - tokensave
---

# Tokensave MCP Server

Tokensave builds a code graph from project source and exposes it through the MCP protocol.
Claude can answer structural questions about any codebase without reading every file.

## What it provides

| Category | Tools |
|----------|-------|
| Symbol search | `tokensave_search`, `tokensave_context`, `tokensave_node`, `tokensave_files` |
| Call graph | `tokensave_callers`, `tokensave_callees`, `tokensave_callers_for` |
| Impact analysis | `tokensave_impact`, `tokensave_affected`, `tokensave_diff_context` |
| Code quality | `tokensave_complexity`, `tokensave_god_class`, `tokensave_coupling`, `tokensave_recursion`, `tokensave_dead_code` |
| Test coverage | `tokensave_test_map`, `tokensave_test_risk`, `tokensave_run_affected_tests` |
| Session memory | `tokensave_session_start`, `tokensave_session_recall`, `tokensave_record_decision` |

37 tools total — see [README](https://github.com/aovestdipaperino/tokensave).

## Setup

Install the CLI and configure Claude Code in one command:

```bash
brew install aovestdipaperino/tap/tokensave   # macOS
cargo install tokensave                        # any platform

tokensave claude-install                       # configures MCP, permissions, hook, CLAUDE.md
```

Index the target project:

```bash
cd /path/to/project
tokensave init
```

Update `args[2]` in `mcp.json` to the absolute path of the indexed project before loading this
server definition.

## Keeping the graph fresh

```bash
tokensave sync            # incremental update after code changes
tokensave sync --force    # full re-index
tokensave branch add      # per-branch graph for multi-branch workflows
```
