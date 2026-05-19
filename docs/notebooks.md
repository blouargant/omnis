# Notebook walkthroughs

Start here: **[examples/index.ipynb](../examples/index.ipynb)** is the
curriculum entry point. It explains how the notebooks fit together,
groups them into seven complexity tiers, and offers curated reading
paths ("I want to specialise the agent", "I'm doing SRE work", …).

Every `examples/sNN_<name>/` directory then ships with its own Jupyter
notebook (`sNN_<name>.ipynb`) that teaches that one example, in the
same complexity order as [docs/examples-catalog.md](examples-catalog.md):

| Tier | Path                                                                  |
|------|------------------------------------------------------------------------|
| 1    | [s01_loop](../examples/s01_loop/s01_loop.ipynb) … [s07_web_search](../examples/s07_web_search/s07_web_search.ipynb) |
| 2    | [s08_ask_user](../examples/s08_ask_user/s08_ask_user.ipynb) … [s10_mcp](../examples/s10_mcp/s10_mcp.ipynb) |
| 3    | [s11_todo](../examples/s11_todo/s11_todo.ipynb) … [s17_interrupt](../examples/s17_interrupt/s17_interrupt.ipynb) |
| 4    | [s18_events](../examples/s18_events/s18_events.ipynb) … [s20_compress](../examples/s20_compress/s20_compress.ipynb) |
| 5    | [s21_skills](../examples/s21_skills/s21_skills.ipynb) … [s23_softskills](../examples/s23_softskills/s23_softskills.ipynb) |
| 6    | [s24_worktree](../examples/s24_worktree/s24_worktree.ipynb) … [s29_redis](../examples/s29_redis/s29_redis.ipynb) |
| 7    | [s30_k8s_context_e2e](../examples/s30_k8s_context_e2e/s30_k8s_context_e2e.ipynb) |

## Setup

The notebooks use the [GoNB](https://github.com/janpfeifer/gonb) Go
kernel for Jupyter.

```bash
# 1. Install Go (already required for Yoke itself).
# 2. Install GoNB into a Jupyter kernelspec:
go install github.com/janpfeifer/gonb@latest
gonb --install

# 3. Make sure jupyter is available (one option: pipx).
pipx install jupyterlab

# 4. Provider env vars. Default points at a local Ollama so the notebooks
#    work against self-hosted models (vLLM, Ollama, LM Studio, …) with
#    zero API spend. Swap in anthropic / openai / gemini if you have keys;
#    full provider catalogue in docs/providers.md.
export YOKE_PROVIDER=openai_compat
export OPENAI_BASE_URL=http://localhost:11434/v1
export YOKE_MODEL=qwen2.5-coder

# 5. Launch jupyter from the repo root so module-relative imports resolve:
cd /path/to/yoke
jupyter lab
```

In JupyterLab, open any `examples/sNN_*/sNN_*.ipynb` and select the
**Go (gonb)** kernel.

## How each notebook is laid out

All 30 notebooks share the same skeleton:

1. **Title + concept anchor** — what this example teaches and where it
   fits in the harness.
2. **Prerequisites** — env vars, optional extras (Redis for
   [s29_redis](../examples/s29_redis/s29_redis.ipynb), a cluster for
   [s30_k8s_context_e2e](../examples/s30_k8s_context_e2e/s30_k8s_context_e2e.ipynb), etc.).
3. **Imports** + **helper** cells.
4. **Numbered walkthrough** — each section breaks one chunk of the
   underlying `main.go` into a markdown intro + code cell pair, so you
   can poke at intermediate state instead of running the whole thing.
5. **What to look for** — observable behaviour to confirm.
6. **Try it yourself** — 1-2 short variations.

## Caveat — every notebook calls the LLM

These walkthroughs always invoke the agent loop (your
[chosen behaviour](../examples/s01_loop/s01_loop.ipynb)) so they need a
configured provider and consume API credits.
[s17_interrupt](../examples/s17_interrupt/s17_interrupt.ipynb) and
[s30_k8s_context_e2e](../examples/s30_k8s_context_e2e/s30_k8s_context_e2e.ipynb)
have additional limitations called out in their own prerequisites
section.
