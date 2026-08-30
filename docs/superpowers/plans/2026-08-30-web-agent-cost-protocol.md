# web_agent Cost Protocol — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the measuring instrument and run the Phase 0/1 campaign that decides — from data, not analysis — which `web_agent` cost variant is adopted.

**Architecture:** All code lands in the **sibling repo `../omnis-benches`** (project policy: no bench code in omnis). `squad-bench/bench.py` gains multi-turn tasks, a deterministic fact-checklist gate (new pure module `scoring.py`), and URL-coverage metrics. Two new drivers sit beside it: `variants.py` (apply/revert a config variant over the omnis HTTP API) and `campaign.py` (time-interleaved runs + drift witness). `judge.py` is the blind pairwise LLM judge, run only on variants that survive the deterministic gate.

**Tech Stack:** Python 3 **standard library only** (`urllib.request`, `argparse`, `json`, `re`, `unittest`). No pip, no third-party packages. The omnis server is driven over HTTP exactly as the web UI does.

**Spec:** [docs/superpowers/specs/2026-08-30-web-agent-cost-protocol-design.md](../specs/2026-08-30-web-agent-cost-protocol-design.md)

## Global Constraints

- **All bench code goes in `../omnis-benches`, never in the omnis repo.** Hard policy in both CLAUDE.md files. Every commit in Tasks 1–7 is made from `../omnis-benches`.
- **Python standard library only.** No pip installs, no third-party packages. Match the existing style in `bench.py` (`urllib.request`, `argparse`, `subprocess`).
- **Nothing imports omnis.** These tools drive omnis over HTTP or as a subprocess.
- **Credentials come from the root `.env`** (`omnis-benches/.env`, gitignored): `OPENAI_BASE_URL`, `OPENAI_API_KEY`. Never hardcode endpoints or keys, never invent per-tool credential loading. Load with `set -a; . ./.env; set +a`.
- **Self-maintenance:** after any change to a tool's interface, metrics, or flags, update `omnis-benches/CLAUDE.md` **and** the affected tool's README in the same commit.
- **Backward compatibility:** existing `tasks.json` / `tasks-kubernetes.json` runs must keep producing identical records. Every new field is additive; a task declaring no `facts`/`forbidden` scores `quality_gate: null`.
- **Scope:** this plan covers the instrument + config-only variants (V0–V4) + the Phase 0/1 campaign. **Phase 2 (V5/V6, Go changes in omnis) is deliberately NOT planned here** — the spec only authorises writing Go if config-only variants prove insufficient, and which one to write depends on Phase 1 data. Phase 2 gets its own plan.

---

## File Structure

| File | Responsibility |
|---|---|
| `../omnis-benches/squad-bench/scoring.py` | **New.** Pure answer-scoring functions (fact checklist, forbidden-with-`unless`, gate verdict). No I/O. This module decides which variant ships, so it is the one part that is unit-tested. |
| `../omnis-benches/squad-bench/test_bench.py` | **New.** `unittest` suite for every pure function added by this plan. |
| `../omnis-benches/squad-bench/bench.py` | **Modify.** Multi-turn prompts, URL coverage, wire `scoring` into the record and `summarize`. |
| `../omnis-benches/squad-bench/tasks-web.json` | **New.** The three task shapes: `web-lookup`, `web-deep-ds7`, `web-canary`. |
| `../omnis-benches/squad-bench/variants.json` | **New.** Declarative V0–V4 config patches. |
| `../omnis-benches/squad-bench/variants.py` | **New.** Snapshot / apply / verify / revert a variant over the omnis config API. |
| `../omnis-benches/squad-bench/campaign.py` | **New.** Time-interleaved campaign driver + drift witness. |
| `../omnis-benches/squad-bench/judge.py` | **New.** Blind pairwise LLM judge (layer 2). |
| `../omnis-benches/squad-bench/README.md` | **Modify.** New metrics, new tasks, campaign + judge usage. |
| `../omnis-benches/CLAUDE.md` | **Modify.** squad-bench section: new metrics/flags/entry points. |

---

### Task 1: `scoring.py` — the deterministic fact checklist

This is the instrument that decides which variant is adopted. A bug here silently ratifies a degraded variant, so it is pure (no I/O) and fully unit-tested. `omnis-benches` has no test suite today; this task introduces one using the stdlib `unittest` module (no new dependency).

**Files:**
- Create: `../omnis-benches/squad-bench/scoring.py`
- Create: `../omnis-benches/squad-bench/test_bench.py`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `match_rule(pattern: str, text: str) -> bool`
  - `score_facts(facts: list[dict], answers: list[str]) -> dict` with keys `found: list[str]`, `missing: list[str]`, `required: int`, `optional_found: list[str]`
  - `check_forbidden(forbidden: list[dict], answers: list[str]) -> list[str]`
  - `quality_gate(task: dict, answers: list[str]) -> dict` with keys `facts: dict`, `forbidden_hits: list[str]`, `quality_gate: bool | None`

- [ ] **Step 1: Write the failing tests**

Create `../omnis-benches/squad-bench/test_bench.py`:

```python
#!/usr/bin/env python3
"""Unit tests for squad-bench pure logic. Run: python3 -m unittest discover -s squad-bench"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import scoring


class TestMatchRule(unittest.TestCase):
    def test_regex_form_is_case_insensitive(self):
        self.assertTrue(scoring.match_rule("/actuateur|actionneur/", "un ACTUATEUR"))

    def test_substring_form_is_case_insensitive(self):
        self.assertTrue(scoring.match_rule("9820772380", "ref 9820772380 ok"))
        self.assertFalse(scoring.match_rule("9820772380", "ref 123"))

    def test_empty_pattern_never_matches(self):
        self.assertFalse(scoring.match_rule("", "anything"))


class TestScoreFacts(unittest.TestCase):
    def test_on_index_selects_the_right_turn(self):
        facts = [{"id": "oem", "on": 2, "required": True, "match": "/9820772380/"}]
        answers = ["nope", "nope", "ref 9820772380"]
        r = scoring.score_facts(facts, answers)
        self.assertEqual(r["found"], ["oem"])
        self.assertEqual(r["missing"], [])

    def test_fact_present_in_wrong_turn_is_missing(self):
        facts = [{"id": "oem", "on": 2, "required": True, "match": "/9820772380/"}]
        answers = ["ref 9820772380", "nope", "nope"]
        self.assertEqual(scoring.score_facts(facts, answers)["missing"], ["oem"])

    def test_on_absent_searches_all_turns(self):
        facts = [{"id": "any", "required": True, "match": "/needle/"}]
        self.assertEqual(scoring.score_facts(facts, ["a", "needle", "c"])["found"], ["any"])

    def test_optional_facts_are_not_required(self):
        facts = [{"id": "used", "on": 0, "required": False, "match": "/39/"}]
        r = scoring.score_facts(facts, ["no price"])
        self.assertEqual(r["missing"], [])
        self.assertEqual(r["required"], 0)


class TestCheckForbidden(unittest.TestCase):
    def test_bare_assertion_is_a_violation(self):
        rules = [{"id": "ref-as-fact", "on": 0, "match": "/9831776780/",
                  "unless": "/non confirm|à vérifier/"}]
        self.assertEqual(scoring.check_forbidden(rules, ["la ref est 9831776780"]),
                         ["ref-as-fact"])

    def test_hedged_mention_is_not_a_violation(self):
        """The DS7 baseline names 9831776780 while flagging it unconfirmed."""
        rules = [{"id": "ref-as-fact", "on": 0, "match": "/9831776780/",
                  "unless": "/non confirm|à vérifier/"}]
        answers = ["La référence 9831776780 n'a pas pu être confirmée — à vérifier."]
        self.assertEqual(scoring.check_forbidden(rules, answers), [])


class TestQualityGate(unittest.TestCase):
    def test_passes_when_all_required_found_and_nothing_forbidden(self):
        task = {"facts": [{"id": "a", "on": 0, "required": True, "match": "/ok/"}]}
        self.assertTrue(scoring.quality_gate(task, ["ok"])["quality_gate"])

    def test_fails_on_one_missing_required_fact(self):
        task = {"facts": [{"id": "a", "on": 0, "required": True, "match": "/ok/"}]}
        self.assertFalse(scoring.quality_gate(task, ["nope"])["quality_gate"])

    def test_fails_on_a_forbidden_hit_even_with_all_facts(self):
        task = {"facts": [{"id": "a", "on": 0, "required": True, "match": "/ok/"}],
                "forbidden": [{"id": "bad", "on": 0, "match": "/invented/"}]}
        r = scoring.quality_gate(task, ["ok and invented"])
        self.assertFalse(r["quality_gate"])
        self.assertEqual(r["forbidden_hits"], ["bad"])

    def test_returns_none_when_the_task_declares_nothing(self):
        self.assertIsNone(scoring.quality_gate({}, ["whatever"])["quality_gate"])


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: FAIL — `ModuleNotFoundError: No module named 'scoring'`.

- [ ] **Step 3: Write the minimal implementation**

Create `../omnis-benches/squad-bench/scoring.py`:

```python
#!/usr/bin/env python3
"""Deterministic answer scoring for squad-bench — layer 1 of the quality gate.

Pure functions, no I/O. This module decides which cost variant is allowed to
ship, so a bug here would silently ratify a degraded variant: it is the one
part of the harness that is unit-tested.

A task declares:
    facts:     [{id, on?, required, match}]   `on` = turn index; absent = any turn
    forbidden: [{id, on?, match, unless?}]    `unless` = an acceptable hedge

`match` and `unless` use bench.py's existing convention: `/regex/` (case
insensitive) or a plain case-insensitive substring.
"""
import re


def match_rule(pattern, text):
    """True if `pattern` matches `text`. `/regex/` or case-insensitive substring."""
    if not pattern:
        return False
    text = text or ""
    if pattern.startswith("/") and pattern.endswith("/") and len(pattern) > 1:
        return bool(re.search(pattern[1:-1], text, re.I))
    return pattern.lower() in text.lower()


def _answer_for(answers, idx):
    """The answer a rule applies to: one turn when `on` is set, else all of them."""
    answers = answers or []
    if idx is None:
        return "\n".join(answers)
    return answers[idx] if 0 <= idx < len(answers) else ""


def score_facts(facts, answers):
    found, missing, optional_found = [], [], []
    required = 0
    for f in facts or []:
        hit = match_rule(f.get("match"), _answer_for(answers, f.get("on")))
        if f.get("required"):
            required += 1
            (found if hit else missing).append(f.get("id"))
        elif hit:
            optional_found.append(f.get("id"))
    return {"found": found, "missing": missing,
            "required": required, "optional_found": optional_found}


def check_forbidden(forbidden, answers):
    """Ids of violated rules. A match that carries its `unless` hedge is NOT a
    violation — the baseline names an unconfirmed part reference while saying so,
    and that is correct behaviour, not a defect."""
    hits = []
    for f in forbidden or []:
        text = _answer_for(answers, f.get("on"))
        if not match_rule(f.get("match"), text):
            continue
        if f.get("unless") and match_rule(f["unless"], text):
            continue
        hits.append(f.get("id"))
    return hits


def quality_gate(task, answers):
    """Layer-1 verdict. `quality_gate` is None when the task declares no rules."""
    facts = score_facts(task.get("facts"), answers)
    hits = check_forbidden(task.get("forbidden"), answers)
    declared = bool(task.get("facts")) or bool(task.get("forbidden"))
    passed = (not facts["missing"] and not hits) if declared else None
    return {"facts": facts, "forbidden_hits": hits, "quality_gate": passed}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: PASS — 13 tests, `OK`.

- [ ] **Step 5: Commit**

```bash
cd ../omnis-benches && git add squad-bench/scoring.py squad-bench/test_bench.py && git commit -m "feat(squad-bench): deterministic fact-checklist scoring

Layer 1 of the web_agent cost-protocol quality gate. Pure, no I/O, and the
first unit tests in this repo — this module decides which cost variant is
adopted, so a bug in it would silently ratify a degraded variant.

The forbidden/unless rule is the load-bearing part: the DS7 baseline names an
unconfirmed part reference *while flagging it unconfirmed*. A variant that
asserts it flatly must fail; the baseline must pass. No facts-found score
catches that distinction."
```

---

### Task 2: Multi-turn tasks in `bench.py`

The expensive turn in the trace was a **follow-up**, with the leader already holding context. `bench.py` is single-turn today, so it cannot reproduce that shape.

**Files:**
- Modify: `../omnis-benches/squad-bench/bench.py` (`fresh`, `run_task`; add `task_prompts`, `run_one_turn`)
- Modify: `../omnis-benches/squad-bench/test_bench.py`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces:
  - `task_prompts(task: dict) -> list[str]`
  - `run_one_turn(base, token, sid, prompt, m, agents, deadline) -> bool` (True when the turn reached `done`)
  - record fields `answers: list[str]` (one entry per turn) and `prompts: list[str]` (what was asked — `judge.py` needs the question, and nothing else in the record carries it); `answer` stays the joined text.

- [ ] **Step 1: Write the failing test**

Append to `../omnis-benches/squad-bench/test_bench.py` (before the `if __name__` block):

```python
import bench


class TestTaskPrompts(unittest.TestCase):
    def test_single_prompt_becomes_a_one_element_list(self):
        self.assertEqual(bench.task_prompts({"prompt": "hello"}), ["hello"])

    def test_prompts_list_is_used_verbatim(self):
        self.assertEqual(bench.task_prompts({"prompts": ["a", "b"]}), ["a", "b"])

    def test_prompts_wins_over_prompt(self):
        self.assertEqual(bench.task_prompts({"prompt": "x", "prompts": ["a"]}), ["a"])

    def test_missing_both_raises(self):
        with self.assertRaises(KeyError):
            bench.task_prompts({})
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v -k TaskPrompts
```

Expected: FAIL — `AttributeError: module 'bench' has no attribute 'task_prompts'`.

- [ ] **Step 3: Write the implementation**

In `../omnis-benches/squad-bench/bench.py`, add `task_prompts` and `run_one_turn` immediately above `run_task`:

```python
def task_prompts(task):
    """A task declares `prompts: [...]` (multi-turn) or `prompt` (single turn).

    The costly turn in the DS7 trace was a follow-up — the leader already held
    context — so reproducing that shape needs more than one POST on one session.
    """
    if task.get("prompts"):
        return list(task["prompts"])
    return [task["prompt"]]


def run_one_turn(base, token, sid, prompt, m, agents, deadline):
    """POST one prompt and fold its SSE stream into `m`. True once `done` seen.

    `deadline` is per turn: a 3-turn task gets 3× the budget of a 1-turn task,
    which keeps single-turn behaviour byte-identical to before.
    """
    t0 = time.time()
    done = False
    try:
        resp = _req("POST", base, f"/api/sessions/{sid}/messages", token,
                    {"prompt": prompt}, timeout=deadline)
        done = consume(resp, m, agents, t0, deadline)
    except (urllib.error.URLError, ConnectionError, TimeoutError):
        pass
    while not done and time.time() - t0 < deadline:
        try:
            resp = _req("GET", base,
                        f"/api/sessions/{sid}/messages/stream?from={m['_seq']}",
                        token, timeout=deadline)
            if getattr(resp, "status", 200) == 204:
                m["status"] = "done"
                return True
            done = consume(resp, m, agents, t0, deadline)
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            time.sleep(1)
    return done
```

Then replace the body of `run_task` between `t0 = time.time()` and `m["wall_ms"] = ...` with:

```python
    t0 = time.time()
    m["answers"] = []
    done = True
    m["prompts"] = task_prompts(task)
    for prompt in m["prompts"]:
        mark = len(m["_answer_parts"])
        done = run_one_turn(base, token, sid, prompt, m, agents, deadline)
        m["answers"].append("".join(m["_answer_parts"][mark:]).strip())
        if not done:
            break
    if not done:
        m["status"] = "timeout"
        try:
            api("POST", base, f"/api/sessions/{sid}/cancel", token, {})
            m["status"] = "cancelled"
        except Exception:
            pass
```

And change the answer assembly line from `m["answer"] = "".join(m["_answer_parts"]).strip()` to:

```python
    m["answer"] = "\n\n".join(a for a in m["answers"] if a).strip()
```

(For a single-turn task this is byte-identical to the previous expression.)

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: PASS — 17 tests, `OK`.

- [ ] **Step 5: Verify backward compatibility against the live server**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
python3 squad-bench/bench.py --task search-single --json | python3 -m json.tool | head -30
```

Expected: a record with `status: "done"`, and a new `answers` array holding exactly one string equal to `answer`.

- [ ] **Step 6: Commit**

```bash
cd ../omnis-benches && git add squad-bench/bench.py squad-bench/test_bench.py && git commit -m "feat(squad-bench): multi-turn tasks via prompts: [...]

The costly turn in the DS7 trace was a follow-up, with the leader already
holding context; a single-turn harness cannot reproduce that shape. Adds
prompts: [...] alongside prompt, loops the POSTs on one session, and keeps
one answer per turn in a new answers[] field for per-turn scoring.

deadline becomes per-turn, so single-turn runs are byte-identical."
```

---

### Task 3: Wire scoring + URL coverage into `bench.py`

**Files:**
- Modify: `../omnis-benches/squad-bench/bench.py` (`fresh`, `consume`, `run_task`, `summarize`)
- Modify: `../omnis-benches/squad-bench/test_bench.py`
- Modify: `../omnis-benches/squad-bench/README.md`
- Modify: `../omnis-benches/CLAUDE.md`

**Interfaces:**
- Consumes: `scoring.quality_gate` (Task 1); `run_task`'s `m["answers"]` (Task 2).
- Produces: record fields `facts`, `forbidden_hits`, `quality_gate`, `distinct_urls: int`, `fetches: int`, `facts_per_fetch: float | None`; helper `fetch_count(m) -> int`.

- [ ] **Step 1: Write the failing tests**

Append to `../omnis-benches/squad-bench/test_bench.py`:

```python
class TestFetchCount(unittest.TestCase):
    def test_sums_leader_and_subagent_webfetch(self):
        m = {"leader_tools": {"WebFetch": 2, "Read": 9},
             "subagent_tools": {"web_agent": {"WebFetch": 5, "WebSearch": 3},
                                "summariser": {"WebFetch": 1}}}
        self.assertEqual(bench.fetch_count(m), 8)

    def test_zero_when_nothing_fetched(self):
        self.assertEqual(bench.fetch_count({"leader_tools": {}, "subagent_tools": {}}), 0)


class TestNoteUrl(unittest.TestCase):
    def test_records_http_urls_only(self):
        m = {"_urls": set()}
        bench._note_url(m, {"url": "https://example.com/a"})
        bench._note_url(m, {"url": "file:///etc/passwd"})
        bench._note_url(m, {"pattern": "not a url"})
        bench._note_url(m, None)
        self.assertEqual(m["_urls"], {"https://example.com/a"})

    def test_deduplicates(self):
        m = {"_urls": set()}
        bench._note_url(m, {"url": "https://example.com/a"})
        bench._note_url(m, {"url": "https://example.com/a"})
        self.assertEqual(len(m["_urls"]), 1)
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v -k "FetchCount or NoteUrl"
```

Expected: FAIL — `AttributeError: module 'bench' has no attribute 'fetch_count'`.

- [ ] **Step 3: Write the implementation**

In `bench.py`, add the import near the top (after `import urllib.request`):

```python
import scoring
```

Add `"_urls": set(),` to the dict returned by `fresh()`.

Add these two helpers immediately below `note_model`:

```python
def _note_url(m, args):
    """Record a URL a tool was asked to fetch, so source coverage can be
    compared across variants. Answers barely cite URLs (0/0/1 across the DS7
    trace), so the answer text is useless for this — the tool args are not."""
    if not isinstance(args, dict):
        return
    u = args.get("url") or args.get("URL")
    if isinstance(u, str) and u.startswith(("http://", "https://")):
        m["_urls"].add(u)


def fetch_count(m):
    """WebFetch calls across the leader and every sub-agent."""
    n = m["leader_tools"].get("WebFetch", 0)
    for tools in m["subagent_tools"].values():
        n += tools.get("WebFetch", 0)
    return n
```

In `consume`, add `_note_url(m, d.get("args"))` as the last line of **both** the `tool_call` branch and the `agent_tool_call` branch.

In `run_task`, replace the single `m["correct"] = check_expect(...)` line with:

```python
    m["correct"] = check_expect(task.get("expect"), m["answer"])
    m.update(scoring.quality_gate(task, m["answers"]))
    m["distinct_urls"] = len(m["_urls"])
    m["fetches"] = fetch_count(m)
    m["facts_per_fetch"] = (round(len(m["facts"]["found"]) / m["fetches"], 3)
                            if m["fetches"] else None)
```

and add `"_urls"` to the tuple of keys popped just below:

```python
    for k in ("_answer_parts", "_seq", "_urls"):
        m.pop(k, None)
```

In `summarize`, insert before the `answer:` line:

```python
    gate = {True: "PASS", False: "FAIL", None: "n/a"}[m.get("quality_gate")]
    f = m.get("facts") or {}
    print(f"  quality_gate={gate}  facts={len(f.get('found', []))}/{f.get('required', 0)}"
          f"  missing={f.get('missing') or '-'}  forbidden={m.get('forbidden_hits') or '-'}")
    print(f"  fetches={m.get('fetches')}  distinct_urls={m.get('distinct_urls')}"
          f"  facts_per_fetch={m.get('facts_per_fetch')}")
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: PASS — 21 tests, `OK`.

- [ ] **Step 5: Verify against the live server that legacy tasks still score `n/a`**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
python3 squad-bench/bench.py --task search-single 2>&1 | grep -E "quality_gate|fetches"
```

Expected: `quality_gate=n/a  facts=0/0 ...` — `tasks.json` declares no `facts`, so the gate must not fire.

- [ ] **Step 6: Update the docs**

In `squad-bench/README.md`, add these rows to the "Metrics (per run)" table, after the `correct` row:

```markdown
| `quality_gate` | layer-1 verdict: every `required` fact found **and** no `forbidden` rule hit. `null` when the task declares neither. |
| `facts` | `{found[], missing[], required, optional_found[]}` from the task's `facts` list |
| `forbidden_hits` | ids of `forbidden` rules violated (a match carrying its `unless` hedge is not a violation) |
| `fetches` / `distinct_urls` | `WebFetch` calls, and how many distinct URLs were fetched — an **efficiency** signal (same facts with fewer fetches is strictly better), not a quality one |
| `facts_per_fetch` | `len(facts.found) / fetches` |
```

In `omnis-benches/CLAUDE.md`, in the `## squad-bench` bullet list, extend the "Metrics per run" line by appending: `, quality_gate / facts / forbidden_hits (deterministic layer-1 scoring, see scoring.py), fetches / distinct_urls / facts_per_fetch`. Add a bullet: `- Multi-turn tasks: a task may declare `prompts: [...]` instead of `prompt`; answers land in `answers[]` and `facts` rules select a turn with `on: <index>`.` Add a bullet: `- Unit tests: `python3 -m unittest discover -s squad-bench` (stdlib only).`

- [ ] **Step 7: Commit**

```bash
cd ../omnis-benches && git add squad-bench/bench.py squad-bench/test_bench.py squad-bench/README.md CLAUDE.md && git commit -m "feat(squad-bench): quality gate + URL coverage metrics

Wires scoring.quality_gate into the record and adds fetches/distinct_urls/
facts_per_fetch, taken from agent_tool_call args.

Source coverage deliberately does NOT come from the answer text: measured on
the DS7 trace the three answers contain 0, 0 and 1 URLs, so that metric would
read ~0 on the baseline itself. Coverage is an efficiency signal, not a
quality one.

Additive: a task declaring no facts/forbidden scores quality_gate=null, so
tasks.json and tasks-kubernetes.json runs are unchanged."
```

---

### Task 4: `tasks-web.json` — the three task shapes, canary calibrated

One DS7 task cannot validate a general change — it would tune to a single trace. Three shapes are the validity floor: a **lookup** (where D must pay off), a **deep** multi-turn replay (where A and B must pay off), and a **canary** whose answer sits deep in a long page, designed to **fail** if a variant truncates.

The canary needs **calibration**, not a guess: its fact must sit **beyond ~9 000 characters** into the fetched page (so a tighter shaper cap would lose it) but **before ~30 000** (so the current 32 000-char cap still reaches it). A fact outside that window makes the canary either always-pass or always-fail, and therefore worthless.

**Files:**
- Create: `../omnis-benches/squad-bench/tasks-web.json`

**Interfaces:**
- Consumes: the `facts`/`forbidden`/`prompts` schema from Tasks 1–2.
- Produces: task ids `web-lookup`, `web-deep-ds7`, `web-canary` used by `campaign.py` (Task 6).

- [ ] **Step 1: Calibrate the canary — measure where candidate facts sit**

Run this against candidate pages, in order, and keep the first whose fact lands in the window:

```bash
cd ../omnis-benches && for u in \
  "https://fr.wikipedia.org/wiki/DS_7_Crossback" \
  "https://en.wikipedia.org/wiki/Peugeot_EMP2_platform" \
  "https://www.rfc-editor.org/rfc/rfc7231.txt" ; do
  python3 - "$u" <<'EOF'
import re, sys, urllib.request
u = sys.argv[1]
r = urllib.request.Request(u, headers={"User-Agent": "Mozilla/5.0"})
html = urllib.request.urlopen(r, timeout=30).read().decode("utf-8", "replace")
text = re.sub(r"<[^>]+>", " ", html)
text = re.sub(r"\s+", " ", text)
print(f"\n== {u}  ({len(text)} chars of text)")
for label, pat in [("empattement/wheelbase", r"empattement|wheelbase"),
                   ("réservoir/fuel tank",   r"réservoir|fuel tank"),
                   ("417 Expectation",       r"417 Expectation")]:
    hits = [mm.start() for mm in re.finditer(pat, text, re.I)]
    print(f"   {label:24} offsets={hits[:5]}  in-window={[h for h in hits if 9000 <= h <= 30000][:3]}")
EOF
done
```

Expected: at least one `in-window` offset is non-empty. Record the winning URL, the fact label, and the offset — they go into the task's `notes` so a later disappearance is detectable.

- [ ] **Step 2: Write `tasks-web.json` using the calibrated canary**

Create `../omnis-benches/squad-bench/tasks-web.json`, substituting `<CANARY_URL>`, `<CANARY_QUESTION>` and `<CANARY_REGEX>` with the values Step 1 produced:

```json
{
  "_comment": "Web-research cost protocol — see omnis docs/superpowers/specs/2026-08-30-web-agent-cost-protocol-design.md. Three shapes are the validity floor: lookup (D must pay off), deep multi-turn (A/B must pay off), canary (must FAIL if a variant truncates). `facts`/`forbidden` are scored by scoring.py; `on` is a turn index into answers[].",
  "tasks": [
    {
      "id": "web-lookup",
      "squad": "knowledge",
      "kind": "web",
      "prompt": "En France, quelle est la durée de validité d'un contrôle technique automobile pour une voiture particulière ?",
      "facts": [
        {"id": "duration", "on": 0, "required": true, "match": "/2\\s*ans|deux\\s*ans/"}
      ],
      "notes": "Lookup tier: one search away, stable regulation, and French-bound so it also exercises the search-language rule. This is the task V4 (depth-ladder gating) must make cheaper WITHOUT losing the fact. A high fetches count here is itself the defect."
    },
    {
      "id": "web-deep-ds7",
      "squad": "knowledge",
      "kind": "web",
      "prompts": [
        "Sur une DS7 de 2018 la trappe a essence n'est jamais verrouillée, est-ce que c'est normal ?",
        "combien coûte le remplacement de l'actuateur ?",
        "donne moi les références de la pièce neuve et d'occasion."
      ],
      "facts": [
        {"id": "actuator",   "on": 0, "required": true,  "match": "/actuateur|actionneur/"},
        {"id": "price-new",  "on": 1, "required": true,  "match": "/[5-9][0-9]\\s*(€|euros)/"},
        {"id": "oem",        "on": 2, "required": true,  "match": "/9820772380/"},
        {"id": "caveat",     "on": 2, "required": true,  "match": "/à vérifier|non confirm|indicatif|concessionnaire/"},
        {"id": "price-used", "on": 1, "required": false, "match": "/39\\s*(€|euros)/"}
      ],
      "forbidden": [
        {"id": "ref-as-fact", "on": 2, "match": "/9831776780/", "unless": "/non confirm|à vérifier|pourrait|obsolète/"}
      ],
      "notes": "Verbatim replay of conversation_fitting-eagle.json — the trace that cost $2.87, of which web_agent was $2.5157 (9.4M prompt / 45k output tokens). GROUND TRUTH IS NOT INDEPENDENT: these references come from omnis itself and must be confirmed once by hand. Until then this task measures consistency against the baseline, not correctness."
    },
    {
      "id": "web-canary",
      "squad": "knowledge",
      "kind": "web",
      "prompt": "<CANARY_QUESTION>",
      "facts": [
        {"id": "buried-fact", "on": 0, "required": true, "match": "<CANARY_REGEX>"}
      ],
      "notes": "TRUNCATION CANARY. Source pinned: <CANARY_URL>; the fact sits at character offset <OFFSET> of the extracted text — past ~9k (so a tighter shaper cap loses it) and before ~30k (so the current 32k cap reaches it). If this task starts failing on the BASELINE, the page changed: re-run the calibration in the plan's Task 4 Step 1 before trusting any campaign result."
    }
  ]
}
```

- [ ] **Step 3: Verify the baseline passes all three**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
python3 squad-bench/bench.py --tasks squad-bench/tasks-web.json --suite --out /tmp/calib.jsonl
```

Expected: `quality_gate=PASS` on all three. **A baseline failure invalidates the task, not the agent** — fix the task's regex or re-calibrate the canary before going further.

- [ ] **Step 4: Commit**

```bash
cd ../omnis-benches && git add squad-bench/tasks-web.json && git commit -m "feat(squad-bench): web cost-protocol task set

Three shapes, the validity floor for not overfitting to one trace: a lookup
(what depth-ladder gating must make cheaper), the verbatim 3-turn DS7 replay
(the \$2.52 trace), and a calibrated truncation canary.

The canary's fact is measured to sit between ~9k and ~30k characters into its
source page: past that lower bound a tighter shaper cap loses it, below the
upper bound the current 32k cap still reaches it. A fact outside that window
would make the canary always-pass or always-fail, i.e. worthless.

Ground truth for the DS7 references is NOT independent — recorded in notes."
```

---

### Task 5: `variants.py` + `variants.json` — declarative config switching

Hand-editing config between runs is fragile and, here, actively dangerous: `OMNIS_CONFIG_PATH` is exported in the developer's shell profile and bypasses `OMNIS_SYSTEM_CONFIG_DIR`. Variants are applied over the HTTP API instead — which is also what makes time-interleaving (Task 6) possible at all.

omnis's own CLAUDE.md documents that a config PUT can silently **drop** keys it does not round-trip, so apply is always followed by a verify, and revert is checked against a pre-campaign snapshot.

**Files:**
- Create: `../omnis-benches/squad-bench/variants.py`
- Create: `../omnis-benches/squad-bench/variants.json`
- Modify: `../omnis-benches/squad-bench/test_bench.py`

**Interfaces:**
- Consumes: `bench.api` (existing HTTP helper).
- Produces:
  - `find_agent(cfg: dict, name: str) -> dict | None`
  - `apply_patch(cfg: dict, patch: dict) -> dict` (returns a **new** dict; patch is `{"agent": <name>, "key": <str>, "value": <any>}` or `{"remove_from": <list-key>, "agent": <name>, "value": <any>}`)
  - `load_variants(path) -> dict[str, dict]`
  - `Switcher(base, token)` with `.snapshot()`, `.apply(variant)`, `.verify(variant)`, `.revert()`

- [ ] **Step 1: Probe the live config shape (do not guess it)**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
curl -s -H "Authorization: Bearer $OMNIS_SERVER_TOKEN" \
  http://127.0.0.1:8080/api/config/parsed/agent | python3 -m json.tool | head -40
```

Expected: the JSON shape of the `agents` container. `find_agent` below handles **both** a list-of-objects and a name-keyed dict, so either shape works — but read the output and confirm `web_agent` is reachable and carries `max_instances` / `tools`.

- [ ] **Step 2: Write the failing tests**

Append to `../omnis-benches/squad-bench/test_bench.py`:

```python
import variants


class TestFindAgent(unittest.TestCase):
    def test_list_of_objects_shape(self):
        cfg = {"agents": [{"name": "leader"}, {"name": "web_agent", "max_instances": 10}]}
        self.assertEqual(variants.find_agent(cfg, "web_agent")["max_instances"], 10)

    def test_name_keyed_dict_shape(self):
        cfg = {"agents": {"web_agent": {"max_instances": 10}}}
        self.assertEqual(variants.find_agent(cfg, "web_agent")["max_instances"], 10)

    def test_unknown_agent_is_none(self):
        self.assertIsNone(variants.find_agent({"agents": []}, "nope"))


class TestApplyPatch(unittest.TestCase):
    def test_set_key_does_not_mutate_the_input(self):
        cfg = {"agents": [{"name": "web_agent", "max_instances": 10}]}
        out = variants.apply_patch(cfg, {"agent": "web_agent", "key": "max_instances", "value": 4})
        self.assertEqual(variants.find_agent(out, "web_agent")["max_instances"], 4)
        self.assertEqual(variants.find_agent(cfg, "web_agent")["max_instances"], 10)

    def test_remove_from_drops_a_list_entry(self):
        cfg = {"agents": [{"name": "web_agent", "tools": ["serper", "web", "ddg"]}]}
        out = variants.apply_patch(cfg, {"agent": "web_agent", "remove_from": "tools", "value": "web"})
        self.assertEqual(variants.find_agent(out, "web_agent")["tools"], ["serper", "ddg"])

    def test_remove_absent_entry_is_a_noop(self):
        cfg = {"agents": [{"name": "web_agent", "tools": ["serper"]}]}
        out = variants.apply_patch(cfg, {"agent": "web_agent", "remove_from": "tools", "value": "web"})
        self.assertEqual(variants.find_agent(out, "web_agent")["tools"], ["serper"])

    def test_unknown_agent_raises(self):
        with self.assertRaises(KeyError):
            variants.apply_patch({"agents": []}, {"agent": "ghost", "key": "x", "value": 1})
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v -k "FindAgent or ApplyPatch"
```

Expected: FAIL — `ModuleNotFoundError: No module named 'variants'`.

- [ ] **Step 4: Write `variants.py`**

```python
#!/usr/bin/env python3
"""Apply / verify / revert an omnis config variant over the HTTP API.

Why over the API and not by editing files: OMNIS_CONFIG_PATH is exported in the
developer shell profile and bypasses OMNIS_SYSTEM_CONFIG_DIR, so file edits are
easy to apply to the wrong layer. It is also what makes time-interleaved
campaigns possible — switching variants must be one call, not a manual step.

omnis's CLAUDE.md documents that a config PUT can silently drop keys it does not
round-trip, so every apply is verified and every revert is checked against the
pre-campaign snapshot.
"""
import copy
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from bench import api  # noqa: E402  (stdlib-only HTTP helper, already in this dir)

SECTIONS = ("agent",)


def find_agent(cfg, name):
    """Locate an agent entry regardless of the container shape (list or dict)."""
    ags = (cfg or {}).get("agents")
    if isinstance(ags, dict):
        return ags.get(name)
    if isinstance(ags, list):
        for a in ags:
            if isinstance(a, dict) and a.get("name") == name:
                return a
    return None


def apply_patch(cfg, patch):
    """Return a NEW config with `patch` applied. Never mutates `cfg`.

    patch = {"agent": n, "key": k, "value": v}          -> set
          | {"agent": n, "remove_from": k, "value": v}  -> drop from a list
    """
    out = copy.deepcopy(cfg)
    a = find_agent(out, patch["agent"])
    if a is None:
        raise KeyError(f"agent {patch['agent']!r} not found in config")
    if "remove_from" in patch:
        cur = a.get(patch["remove_from"]) or []
        a[patch["remove_from"]] = [x for x in cur if x != patch["value"]]
    else:
        a[patch["key"]] = patch["value"]
    return out


def load_variants(path):
    with open(path) as f:
        return {v["id"]: v for v in json.load(f)["variants"]}


class Switcher:
    """Applies variants to a running server, and can always get back to V0."""

    def __init__(self, base, token):
        self.base, self.token = base, token
        self.baseline = None

    def snapshot(self):
        self.baseline = {s: api("GET", self.base, f"/api/config/parsed/{s}", self.token)
                         for s in SECTIONS}
        return self.baseline

    def _put(self, section, cfg):
        api("PUT", self.base, f"/api/config/parsed/{section}", self.token, cfg)

    def _reload(self):
        api("POST", self.base, "/api/config/reload", self.token, {})

    def apply(self, variant):
        """Apply a variant from the SNAPSHOT, never from current state, so
        variants never stack."""
        if self.baseline is None:
            self.snapshot()
        for section in SECTIONS:
            cfg = copy.deepcopy(self.baseline[section])
            touched = False
            for p in variant.get("patches", []):
                if p.get("section", "agent") != section:
                    continue
                cfg = apply_patch(cfg, p)
                touched = True
            if touched:
                self._put(section, cfg)
        self._reload()

    def verify(self, variant):
        """Read the live config back and confirm each patch actually landed.
        Returns a list of human-readable mismatches (empty == verified)."""
        bad = []
        for section in SECTIONS:
            live = api("GET", self.base, f"/api/config/parsed/{section}", self.token)
            for p in variant.get("patches", []):
                if p.get("section", "agent") != section:
                    continue
                a = find_agent(live, p["agent"]) or {}
                if "remove_from" in p:
                    if p["value"] in (a.get(p["remove_from"]) or []):
                        bad.append(f"{p['agent']}.{p['remove_from']} still has {p['value']!r}")
                elif a.get(p["key"]) != p["value"]:
                    bad.append(f"{p['agent']}.{p['key']} is {a.get(p['key'])!r},"
                               f" expected {p['value']!r}")
        return bad

    def revert(self):
        """Restore the snapshot and confirm it round-tripped."""
        if self.baseline is None:
            return []
        for section in SECTIONS:
            self._put(section, self.baseline[section])
        self._reload()
        bad = []
        for section in SECTIONS:
            live = api("GET", self.base, f"/api/config/parsed/{section}", self.token)
            if live != self.baseline[section]:
                bad.append(f"section {section} did not round-trip on revert")
        return bad
```

- [ ] **Step 5: Write `variants.json`**

```json
{
  "_comment": "Config-only variants for the web_agent cost protocol (Phase 1). V5/V6 need Go and are deliberately absent — the spec only authorises writing Go if these prove insufficient. Every variant is applied from the V0 snapshot, never stacked.",
  "variants": [
    {
      "id": "V0",
      "label": "baseline",
      "patches": []
    },
    {
      "id": "V1",
      "label": "max_instances 10 -> 4",
      "hypothesis": "C — W is linear in cost; fewer parallel dispatches also reduce gateway contention",
      "patches": [
        {"section": "agent", "agent": "web_agent", "key": "max_instances", "value": 4}
      ]
    },
    {
      "id": "V3",
      "label": "B — delegate fetching to web_fetcher",
      "hypothesis": "B — splitting one F-step session into F one-step sessions kills the F^2 term; token count collapses even though price per token is unchanged",
      "patches": [
        {"section": "agent", "agent": "web_agent", "key": "subagents", "value": ["web_fetcher"]},
        {"section": "agent", "agent": "web_agent", "remove_from": "tools", "value": "web"},
        {"section": "agent", "agent": "web_fetcher", "key": "model_ref", "value": "balanced"}
      ]
    },
    {
      "id": "V3b",
      "label": "B — web_fetcher does the searching too",
      "hypothesis": "B — as V3, but web_agent keeps no web tools at all",
      "patches": [
        {"section": "agent", "agent": "web_agent", "key": "subagents", "value": ["web_fetcher"]},
        {"section": "agent", "agent": "web_agent", "remove_from": "tools", "value": "web"},
        {"section": "agent", "agent": "web_agent", "remove_from": "tools", "value": "serper"},
        {"section": "agent", "agent": "web_agent", "remove_from": "tools", "value": "ddg"},
        {"section": "agent", "agent": "web_fetcher", "key": "model_ref", "value": "balanced"}
      ]
    }
  ]
}
```

**V2 and V4 are instruction edits, not config-key edits, so they are not in this file.** They are applied by editing `registry/agents/web_agent/instruction.md` (V2) and the `deep-research` skill trigger (V4) in the running install, then `POST /api/config/reload`. Task 7 Step 3 records exactly how each was applied, because an instruction variant is not reproducible from `variants.json` alone.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: PASS — 28 tests, `OK`.

- [ ] **Step 7: Verify apply → verify → revert against the live server**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && python3 - <<'EOF'
import os, sys
sys.path.insert(0, "squad-bench")
import variants
sw = variants.Switcher(os.environ.get("OMNIS_SERVER", "http://127.0.0.1:8080"),
                       os.environ.get("OMNIS_SERVER_TOKEN", ""))
sw.snapshot()
vs = variants.load_variants("squad-bench/variants.json")
sw.apply(vs["V1"]); print("V1 mismatches:", sw.verify(vs["V1"]) or "none")
print("revert mismatches:", sw.revert() or "none")
EOF
```

Expected: `V1 mismatches: none` **and** `revert mismatches: none`. A non-empty revert list means the config PUT is dropping keys — stop and fix that before running any campaign, because every later measurement would be against an unknown config.

- [ ] **Step 8: Commit**

```bash
cd ../omnis-benches && git add squad-bench/variants.py squad-bench/variants.json squad-bench/test_bench.py && git commit -m "feat(squad-bench): declarative config variants over the HTTP API

Applying variants by hand is fragile here: OMNIS_CONFIG_PATH is exported in
the dev shell profile and bypasses OMNIS_SYSTEM_CONFIG_DIR, so a file edit
easily lands in the wrong layer. Switching over the API also makes the
time-interleaved campaign possible — it must be one call.

Every apply is read back and verified, and revert is checked against the
pre-campaign snapshot, because omnis's own CLAUDE.md documents that a config
PUT can silently drop keys it does not round-trip. Variants are always applied
from the snapshot so they never stack.

V5/V6 are absent by design: the spec only authorises Go if config-only fails."
```

---

### Task 6: `campaign.py` — interleaved runs + drift witness

The confound specific to a *web* bench: a page can change or vanish between runs. Running all of V0 then all of V3 would confuse web drift with variant effect.

**Files:**
- Create: `../omnis-benches/squad-bench/campaign.py`
- Modify: `../omnis-benches/squad-bench/test_bench.py`
- Modify: `../omnis-benches/squad-bench/README.md`
- Modify: `../omnis-benches/CLAUDE.md`

**Interfaces:**
- Consumes: `bench.run_task`, `variants.Switcher`, `variants.load_variants`.
- Produces:
  - `interleaved_order(variant_ids: list[str], repeats: int) -> list[str]`
  - `drift_ok(first: list[dict], last: list[dict]) -> tuple[bool, str]`
  - `median(xs: list[float]) -> float | None`
  - CLI: `python3 squad-bench/campaign.py --variants V0,V1,V3,V3b --repeat 2 --out campaign.jsonl`

- [ ] **Step 1: Write the failing tests**

Append to `../omnis-benches/squad-bench/test_bench.py`:

```python
import campaign


class TestInterleavedOrder(unittest.TestCase):
    def test_variants_alternate_within_each_repeat(self):
        self.assertEqual(campaign.interleaved_order(["V0", "V1", "V3"], 2),
                         ["V0", "V1", "V3", "V0", "V1", "V3"])

    def test_single_repeat_is_one_pass(self):
        self.assertEqual(campaign.interleaved_order(["V0", "V1"], 1), ["V0", "V1"])

    def test_zero_repeats_is_empty(self):
        self.assertEqual(campaign.interleaved_order(["V0"], 0), [])


class TestMedian(unittest.TestCase):
    def test_odd_length(self):
        self.assertEqual(campaign.median([3, 1, 2]), 2)

    def test_even_length_averages_the_middle(self):
        self.assertEqual(campaign.median([1, 2, 3, 4]), 2.5)

    def test_empty_is_none(self):
        self.assertIsNone(campaign.median([]))


class TestDriftOk(unittest.TestCase):
    def test_stable_witness_passes(self):
        first = [{"task": "t", "quality_gate": True, "total_cost_usd": 1.0}]
        last = [{"task": "t", "quality_gate": True, "total_cost_usd": 1.2}]
        ok, _ = campaign.drift_ok(first, last)
        self.assertTrue(ok)

    def test_one_task_regressing_voids_the_campaign(self):
        """A single canary regression must void it; the others still passing
        must not mask it."""
        first = [{"task": "web-canary", "quality_gate": True, "total_cost_usd": 1.0},
                 {"task": "web-lookup", "quality_gate": True, "total_cost_usd": 1.0}]
        last = [{"task": "web-canary", "quality_gate": False, "total_cost_usd": 1.0},
                {"task": "web-lookup", "quality_gate": True, "total_cost_usd": 1.0}]
        ok, why = campaign.drift_ok(first, last)
        self.assertFalse(ok)
        self.assertIn("web-canary", why)

    def test_cost_doubling_in_the_witness_voids_the_campaign(self):
        first = [{"task": "t", "quality_gate": True, "total_cost_usd": 1.0}]
        last = [{"task": "t", "quality_gate": True, "total_cost_usd": 2.5}]
        ok, why = campaign.drift_ok(first, last)
        self.assertFalse(ok)
        self.assertIn("cost", why.lower())
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v -k "Interleaved or Median or DriftOk"
```

Expected: FAIL — `ModuleNotFoundError: No module named 'campaign'`.

- [ ] **Step 3: Write `campaign.py`**

```python
#!/usr/bin/env python3
"""Run a time-interleaved variant campaign and record every run to JSONL.

The confound specific to a web bench is that the web moves: a page can change or
vanish mid-campaign. Running all of V0 then all of V3 would confuse web drift
with variant effect, so variants alternate within each repeat, and V0 is run
once more at the very end as a drift witness. If the closing witness diverges
from the opening one, the campaign is void — not "interesting".

Usage:
  set -a; . ./.env; set +a
  python3 squad-bench/campaign.py --variants V0,V1,V3,V3b --repeat 2 \
      --tasks squad-bench/tasks-web.json --out campaign.jsonl
"""
import argparse
import copy
import json
import os
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import bench       # noqa: E402
import variants    # noqa: E402

COST_DRIFT_FACTOR = 2.0   # witness cost may not more than double


def interleaved_order(variant_ids, repeats):
    """[V0,V1,V3, V0,V1,V3, ...] — variants alternate, so web drift and gateway
    weather hit every variant equally instead of only the ones run last."""
    order = []
    for _ in range(max(0, repeats)):
        order.extend(variant_ids)
    return order


def median(xs):
    xs = sorted(x for x in xs if x is not None)
    if not xs:
        return None
    mid = len(xs) // 2
    return xs[mid] if len(xs) % 2 else (xs[mid - 1] + xs[mid]) / 2


def drift_ok(first, last):
    """Compare the opening and closing V0 witness runs."""
    if not first or not last:
        return True, "no witness pair"
    was = {r.get("task"): r.get("quality_gate") for r in first}
    now = {r.get("task"): r.get("quality_gate") for r in last}
    regressed = [t for t, v in was.items() if v is True and now.get(t) is False]
    if regressed:
        return False, ("baseline quality regressed between the opening and closing "
                       f"witness on: {', '.join(regressed)}")
    c0, c1 = median([r.get("total_cost_usd") for r in first]), \
             median([r.get("total_cost_usd") for r in last])
    if c0 and c1 and c1 > c0 * COST_DRIFT_FACTOR:
        return False, f"baseline cost drifted {c0} -> {c1} (> {COST_DRIFT_FACTOR}x)"
    return True, "stable"


def _run(base, token, task, agents, deadline, out, vid, phase):
    m = bench.run_task(base, token, copy.deepcopy(task), agents, deadline, False, None)
    m["variant"] = vid
    m["phase"] = phase
    m["at"] = time.strftime("%Y-%m-%dT%H:%M:%S")
    bench.summarize(m)
    if out:
        with open(out, "a") as f:
            f.write(json.dumps(m) + "\n")
    return m


def main():
    ap = argparse.ArgumentParser(description="Interleaved variant campaign.")
    ap.add_argument("--server", default=os.environ.get("OMNIS_SERVER", "http://127.0.0.1:8080"))
    ap.add_argument("--token", default=os.environ.get("OMNIS_SERVER_TOKEN", ""))
    ap.add_argument("--tasks", default=os.path.join(HERE, "tasks-web.json"))
    ap.add_argument("--variants-file", default=os.path.join(HERE, "variants.json"))
    ap.add_argument("--variants", default="V0", help="comma-separated variant ids")
    ap.add_argument("--repeat", type=int, default=2)
    ap.add_argument("--deadline", type=int, default=420, help="per-turn cap (s)")
    ap.add_argument("--out", help="append one JSON record per run")
    args = ap.parse_args()

    tasks = json.load(open(args.tasks))["tasks"]
    catalog = variants.load_variants(args.variants_file)
    ids = [v.strip() for v in args.variants.split(",") if v.strip()]
    unknown = [v for v in ids if v not in catalog]
    if unknown:
        sys.exit(f"unknown variant(s): {', '.join(unknown)}")

    squads = bench.api("GET", args.server, "/api/squads", args.token)
    agents = set()
    for s in squads.get("squads", []):
        agents.update(s.get("members", []))
        if s.get("leader"):
            agents.add(s["leader"])

    sw = variants.Switcher(args.server, args.token)
    sw.snapshot()
    opening, closing = [], []
    try:
        print("=== opening witness (V0) ===")
        sw.apply(catalog["V0"])
        for t in tasks:
            opening.append(_run(args.server, args.token, t, agents,
                                args.deadline, args.out, "V0", "witness-open"))

        for vid in interleaved_order(ids, args.repeat):
            v = catalog[vid]
            sw.apply(v)
            bad = sw.verify(v)
            if bad:
                sys.exit(f"variant {vid} did not apply cleanly: {bad}")
            print(f"=== {vid} — {v['label']} ===")
            for t in tasks:
                _run(args.server, args.token, t, agents, args.deadline, args.out, vid, "campaign")

        print("=== closing witness (V0) ===")
        sw.apply(catalog["V0"])
        for t in tasks:
            closing.append(_run(args.server, args.token, t, agents,
                                args.deadline, args.out, "V0", "witness-close"))
    finally:
        bad = sw.revert()
        print("revert:", "clean" if not bad else f"MISMATCH {bad}")

    ok, why = drift_ok(opening, closing)
    print(f"\n===== drift witness: {'OK' if ok else 'CAMPAIGN VOID'} — {why} =====")
    sys.exit(0 if ok else 2)


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: PASS — 37 tests, `OK`.

- [ ] **Step 5: Smoke-test the campaign wiring on one cheap variant**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
python3 squad-bench/campaign.py --variants V0 --repeat 1 \
  --tasks squad-bench/tasks-web.json --out /tmp/smoke.jsonl 2>&1 | tail -20
```

Expected: three phases run (`witness-open`, `campaign`, `witness-close`), `revert: clean`, and `drift witness: OK`.

- [ ] **Step 6: Update the docs**

In `squad-bench/README.md`, add a `## Variant campaigns` section:

```markdown
## Variant campaigns

`campaign.py` runs several config variants against the same task suite and writes
one JSON record per run, tagged with `variant` and `phase`.

```bash
set -a; . ./.env; set +a
python3 squad-bench/campaign.py --variants V0,V1,V3,V3b --repeat 2 \
    --tasks squad-bench/tasks-web.json --out campaign.jsonl
```

Variants live in `variants.json` and are applied over the omnis config API
(`PUT /api/config/parsed/<section>` + `POST /api/config/reload`), always from the
V0 snapshot so they never stack. Each apply is read back and **verified**; the
campaign always **reverts** in a `finally` block and reports whether the revert
round-tripped.

**Variants are interleaved in time** (V0,V1,V3, V0,V1,V3 …) and V0 is run once
before and once after the campaign as a **drift witness**. The web moves: running
all of V0 then all of V3 would confuse page drift with variant effect. If the
closing witness regressed in quality, or its median cost more than doubled, the
campaign exits non-zero and its numbers must be discarded.
```

In `omnis-benches/CLAUDE.md`, add to the Layout table a row for the campaign entry point, and to the `## squad-bench` bullets: `- `campaign.py` — interleaved multi-variant campaigns with a V0 drift witness; `variants.py`/`variants.json` apply config variants over the HTTP API (verified apply, checked revert).`

- [ ] **Step 7: Commit**

```bash
cd ../omnis-benches && git add squad-bench/campaign.py squad-bench/test_bench.py squad-bench/README.md CLAUDE.md && git commit -m "feat(squad-bench): interleaved variant campaigns with a drift witness

The confound specific to a web bench is that the web moves. Running all of V0
then all of V3 would confuse page drift with variant effect, so variants
alternate within each repeat and V0 runs once before and once after as a
witness. A witness that regressed in quality, or whose median cost more than
doubled, voids the campaign (exit 2) rather than being reported as a result.

Reverts in a finally block and reports whether the revert round-tripped."
```

---

### Task 7: `judge.py` — blind pairwise LLM judge, and run the campaign

**Files:**
- Create: `../omnis-benches/squad-bench/judge.py`
- Modify: `../omnis-benches/squad-bench/test_bench.py`
- Modify: `../omnis-benches/squad-bench/README.md`
- Create: `../omnis-benches/reports/web-agent-cost-phase1-2026-08-30.md`

**Interfaces:**
- Consumes: campaign JSONL records (`variant`, `task`, `answers`, `quality_gate`, `total_cost_usd`, `wall_ms`) from Task 6.
- Produces:
  - `build_pairs(baseline_answer: str, variant_answer: str, passes: int) -> list[tuple[str, str, bool]]` — `(A, B, swapped)`
  - `parse_verdict(text: str) -> dict[str, str]` — criterion → `better|equivalent|worse`
  - `aggregate(verdicts: list[dict], swapped: list[bool]) -> dict[str, str]` — majority per criterion, un-swapping positions
  - CLI: `python3 squad-bench/judge.py --records campaign.jsonl --variant V3`

- [ ] **Step 1: Write the failing tests**

Append to `../omnis-benches/squad-bench/test_bench.py`:

```python
import judge


class TestBuildPairs(unittest.TestCase):
    def test_positions_alternate_to_cancel_position_bias(self):
        pairs = judge.build_pairs("BASE", "VAR", 4)
        self.assertEqual([p[2] for p in pairs], [False, True, False, True])
        self.assertEqual(pairs[0][:2], ("BASE", "VAR"))
        self.assertEqual(pairs[1][:2], ("VAR", "BASE"))


class TestParseVerdict(unittest.TestCase):
    def test_reads_one_line_per_criterion(self):
        txt = ("completeness: better\nsourcing: equivalent\n"
               "caveats: worse\ninvention: equivalent\n")
        self.assertEqual(judge.parse_verdict(txt),
                         {"completeness": "better", "sourcing": "equivalent",
                          "caveats": "worse", "invention": "equivalent"})

    def test_unparseable_criterion_is_omitted(self):
        self.assertEqual(judge.parse_verdict("completeness: maybe\nsourcing: worse"),
                         {"sourcing": "worse"})


class TestAggregate(unittest.TestCase):
    """Every verdict must end up describing THE VARIANT. When a pass was not
    swapped, A was the baseline, so its raw verdict describes the baseline and
    has to be flipped. Getting this backwards inverts every judge result while
    still looking plausible, so each direction is pinned separately."""

    def test_unswapped_verdict_describes_the_baseline_and_is_flipped(self):
        # not swapped => A was the BASELINE; "better" means the baseline won,
        # i.e. the variant is worse.
        self.assertEqual(judge.aggregate([{"sourcing": "better"}], [False])["sourcing"],
                         "worse")

    def test_swapped_verdict_describes_the_variant_and_is_kept(self):
        # swapped => A was the VARIANT; "better" means the variant won.
        self.assertEqual(judge.aggregate([{"sourcing": "better"}], [True])["sourcing"],
                         "better")

    def test_identical_raw_verdict_in_both_positions_cancels_out(self):
        # This is the entire point of permuting: the same raw verdict regardless
        # of which text came first is position bias, not a preference.
        self.assertEqual(
            judge.aggregate([{"sourcing": "better"}, {"sourcing": "better"}],
                            [False, True])["sourcing"], "equivalent")

    def test_majority_wins(self):
        # all unswapped: the baseline was judged worse twice => the variant is better
        verdicts = [{"caveats": "worse"}, {"caveats": "equivalent"}, {"caveats": "worse"}]
        self.assertEqual(judge.aggregate(verdicts, [False, False, False])["caveats"],
                         "better")

    def test_no_majority_is_equivalent(self):
        verdicts = [{"caveats": "better"}, {"caveats": "worse"}]
        self.assertEqual(judge.aggregate(verdicts, [False, False])["caveats"],
                         "equivalent")
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v -k "BuildPairs or ParseVerdict or Aggregate"
```

Expected: FAIL — `ModuleNotFoundError: No module named 'judge'`.

- [ ] **Step 3: Write `judge.py`**

```python
#!/usr/bin/env python3
"""Layer 2 of the quality gate: a blind pairwise LLM judge.

Only variants that already passed the deterministic layer-1 gate reach this
script. It compares each variant answer against the baseline answer for the same
task, BLIND (no variant identity in the prompt) and with the two answers'
POSITIONS PERMUTED across passes, because a judge that always sees the baseline
first will favour a position rather than a text. Verdicts are un-swapped before
being counted.

Credentials come from the root .env (OPENAI_BASE_URL / OPENAI_API_KEY), per repo
policy. Stdlib only.

Usage:
  set -a; . ./.env; set +a
  python3 squad-bench/judge.py --records campaign.jsonl --variant V3
"""
import argparse
import collections
import json
import os
import sys
import urllib.request

CRITERIA = ("completeness", "sourcing", "caveats", "invention")
VALID = ("better", "equivalent", "worse")

PROMPT = """You are comparing two answers to the same question. You do not know
how either was produced. Judge ONLY the texts.

QUESTION:
{question}

ANSWER A:
{a}

ANSWER B:
{b}

Rate A relative to B on each criterion, using exactly one of: better, equivalent, worse.
  completeness — does it cover what the question asked?
  sourcing     — are claims attributed to identifiable sources?
  caveats      — are uncertainties and unconfirmed items flagged rather than asserted?
  invention    — "better" means LESS apparent invention (fewer unsupported specifics).

Reply with exactly four lines and nothing else:
completeness: <verdict>
sourcing: <verdict>
caveats: <verdict>
invention: <verdict>
"""


def build_pairs(baseline_answer, variant_answer, passes):
    """(A, B, swapped) per pass, alternating which text is shown first."""
    pairs = []
    for i in range(passes):
        swapped = bool(i % 2)
        pairs.append((variant_answer, baseline_answer, True) if swapped
                     else (baseline_answer, variant_answer, False))
    return pairs


def parse_verdict(text):
    out = {}
    for line in (text or "").splitlines():
        if ":" not in line:
            continue
        k, _, v = line.partition(":")
        k, v = k.strip().lower(), v.strip().lower()
        if k in CRITERIA and v in VALID:
            out[k] = v
    return out


def _flip(v):
    return {"better": "worse", "worse": "better"}.get(v, v)


def aggregate(verdicts, swapped):
    """Majority verdict per criterion, ABOUT THE VARIANT.

    When swapped, A was the variant, so the raw verdict already describes it;
    when not swapped, A was the baseline, so it must be flipped."""
    tally = {c: collections.Counter() for c in CRITERIA}
    for v, sw in zip(verdicts, swapped):
        for c, raw in v.items():
            tally[c][raw if sw else _flip(raw)] += 1
    out = {}
    for c in CRITERIA:
        if not tally[c]:
            out[c] = "equivalent"
            continue
        top, n = tally[c].most_common(1)[0]
        ties = [k for k, m in tally[c].items() if m == n]
        out[c] = top if len(ties) == 1 else "equivalent"
    return out


def ask_model(base_url, api_key, model, prompt):
    body = {"model": model, "temperature": 0,
            "messages": [{"role": "user", "content": prompt}]}
    req = urllib.request.Request(base_url.rstrip("/") + "/v1/chat/completions",
                                 data=json.dumps(body).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    if api_key:
        req.add_header("Authorization", "Bearer " + api_key)
    with urllib.request.urlopen(req, timeout=120) as r:
        d = json.loads(r.read().decode("utf-8", "replace"))
    return d["choices"][0]["message"]["content"]


def main():
    ap = argparse.ArgumentParser(description="Blind pairwise judge for campaign records.")
    ap.add_argument("--records", required=True, help="campaign JSONL")
    ap.add_argument("--variant", required=True, help="variant id to judge vs V0")
    ap.add_argument("--model", default=os.environ.get("JUDGE_MODEL", "Premium"))
    ap.add_argument("--base-url", default=os.environ.get("OPENAI_BASE_URL", ""))
    ap.add_argument("--api-key", default=os.environ.get("OPENAI_API_KEY", ""))
    ap.add_argument("--passes", type=int, default=3)
    args = ap.parse_args()

    if not args.base_url:
        sys.exit("OPENAI_BASE_URL is unset — run: set -a; . ./.env; set +a")

    by = collections.defaultdict(dict)
    with open(args.records) as f:
        for line in f:
            r = json.loads(line)
            if r.get("phase") == "witness-close":
                continue
            by[r["task"]].setdefault(r["variant"], r)

    overall = {}
    for task, rs in sorted(by.items()):
        base, var = rs.get("V0"), rs.get(args.variant)
        if not base or not var:
            print(f"{task}: missing V0 or {args.variant} record — skipped")
            continue
        question = "\n".join(base.get("prompts") or [task])
        verdicts, swaps = [], []
        for a, b, sw in build_pairs(base["answer"], var["answer"], args.passes):
            txt = ask_model(args.base_url, args.api_key, args.model,
                            PROMPT.format(question=question, a=a, b=b))
            verdicts.append(parse_verdict(txt))
            swaps.append(sw)
        agg = aggregate(verdicts, swaps)
        overall[task] = agg
        print(f"{task}: " + "  ".join(f"{c}={agg[c]}" for c in CRITERIA))

    worse = {t: {c: v for c, v in a.items() if v == "worse"} for t, a in overall.items()}
    worse = {t: v for t, v in worse.items() if v}
    print("\n===== verdict =====")
    if worse:
        print(f"{args.variant} REJECTED — worse on: {json.dumps(worse)}")
        sys.exit(2)
    print(f"{args.variant} accepted — >= equivalent on all four criteria, every task")


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd ../omnis-benches && python3 -m unittest discover -s squad-bench -v
```

Expected: PASS — 45 tests, `OK`.

- [ ] **Step 5: Run Phase 0 + Phase 1 (config variants V1, V3, V3b)**

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
python3 squad-bench/campaign.py --variants V0,V1,V3,V3b --repeat 2 \
  --tasks squad-bench/tasks-web.json --out reports/campaign-2026-08-30.jsonl
```

Expected: exits 0 with `drift witness: OK`. **A non-zero exit voids the run** — repeat the campaign rather than reporting its numbers.

- [ ] **Step 6: Apply and measure the two instruction variants (V2, V4)**

These are instruction edits, not config keys, so they are applied by hand and recorded verbatim in the report (an instruction variant is not reproducible from `variants.json`).

**V2** — in the running install's `registry/agents/web_agent/instruction.md`, replace the "Iterate strategically" bullet with a numbered procedure carrying an explicit cap and stop condition:

```markdown
  2. Iterate under a hard budget:
     a. Run ONE 'WebSearch'. Read the snippets before fetching anything.
     b. 'WebFetch' at most the 3 most promising results, one at a time.
     c. After each fetch, write down the quote that answers the question.
     d. STOP as soon as the question is answered. Fetch a 4th page only if a
        required fact is still missing, and never exceed 6 fetches in total.
     e. At 6 fetches, stop and report what is still unresolved under
        "open questions". Running longer is a failure, not diligence.
```

**V4** — in the running install's `registry/skills/deep-research/SKILL.md` frontmatter, narrow the `description` so a factual lookup no longer matches, by appending this sentence to it:

```
Do NOT use for a factual lookup with a single checkable answer (a price, a part
reference, a version number, a date, a regulation) — those are standard research.
```

Then, for each:

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
curl -s -X POST -H "Authorization: Bearer $OMNIS_SERVER_TOKEN" \
  http://127.0.0.1:8080/api/config/reload && \
python3 squad-bench/bench.py --tasks squad-bench/tasks-web.json --suite --repeat 2 \
  --out reports/campaign-2026-08-30.jsonl
```

Restore the original instruction files and reload after each measurement.

- [ ] **Step 7: Judge the survivors**

For each variant whose every run shows `quality_gate=PASS` and whose median `total_cost_usd` fell by ≥30%:

```bash
cd ../omnis-benches && set -a && . ./.env && set +a && \
python3 squad-bench/judge.py --records reports/campaign-2026-08-30.jsonl --variant V3
```

Expected: exit 0 (`accepted`) or exit 2 (`REJECTED — worse on: …`).

- [ ] **Step 8: Write the report against the pre-registered decision rule**

Create `../omnis-benches/reports/web-agent-cost-phase1-2026-08-30.md` containing, in this order:

1. **Measured baseline** — the real `delegations` (`W`) and `subagent_tools.web_agent.WebFetch` (`F`) values, stated **beside** the spec's estimates of `W≈10` / `F≈12`, with an explicit note on whether the estimate held.
2. A table: variant × task → median `total_cost_usd`, median `wall_ms`, `quality_gate` (n/n passed), `fetches`, `distinct_urls`.
3. The **decision-rule verdict per variant** — retained / disqualified — quoting the rule from the spec §6 verbatim and naming which clause each disqualified variant failed.
4. The judge verdicts for survivors.
5. **Whether Phase 2 (Go work) is warranted**, and if so which of A (sliding window) or the shaper cap the data points at — with the reasoning, not a preference.

- [ ] **Step 9: Commit**

```bash
cd ../omnis-benches && git add squad-bench/judge.py squad-bench/test_bench.py squad-bench/README.md reports/ && git commit -m "feat(squad-bench): blind pairwise judge + Phase 1 campaign results

Layer 2 of the quality gate, run only on variants that already passed the
deterministic layer-1 gate. Blind (no variant identity in the prompt) and
position-permuted across passes, because a judge shown the baseline first every
time would rank a position rather than a text; verdicts are un-swapped before
being counted, and a tie reads as equivalent.

Includes the Phase 1 campaign records and the report, which states the measured
W and F beside the spec's estimates and applies the pre-registered decision rule
clause by clause."
```

---

## Self-Review

**1. Spec coverage**

| Spec section | Task |
|---|---|
| §3.1 multi-turn tasks | Task 2 |
| §3.2 layer-1 fact checklist, `forbidden/unless` | Task 1 (logic), Task 3 (wiring) |
| §3.2 rejected "sources cited" metric → URL coverage | Task 3 |
| §3.3 layer-2 blind pairwise judge | Task 7 |
| §3.4 three task shapes + calibrated canary | Task 4 |
| §4 variants V0–V4 | Task 5 (V0/V1/V3/V3b), Task 7 Step 6 (V2/V4) |
| §4.1 variants as declarative patches over the API | Task 5 |
| §5.1 time-interleaving + drift witness | Task 6 |
| §5.2 medians only | Task 6 (`median`), Task 7 Step 8 |
| §5.3 judge stochasticity | Task 7 (`build_pairs`, `aggregate`) |
| §6 pre-registered decision rule | Task 7 Step 8 |
| §7 budget | Task 7 Steps 5–6 (2 repeats × 3 tasks) |
| §8 non-goals | Task 4 (ground-truth note in `web-deep-ds7`) |
| §4 V5/V6 (Go) | **Deliberately out of scope** — see Global Constraints |

**2. Placeholder scan.** The only bracketed tokens are `<CANARY_URL>`, `<CANARY_QUESTION>`, `<CANARY_REGEX>`, `<OFFSET>` in Task 4 Step 2. These are **outputs of the executable calibration in Step 1**, not deferred decisions: Step 1 prints the values and Step 3 fails the task if they are wrong. Every other step carries runnable code or an exact command.

**3. Type consistency.** `quality_gate` is a dict-returning function in `scoring.py` and a bool-or-None *record field* after `m.update(...)` in Task 3 — deliberate, and the only name reused across both meanings. `find_agent` / `apply_patch` signatures match between Tasks 5's tests and implementation. `build_pairs` returns `(A, B, swapped)` and `aggregate` consumes the parallel `swapped` list in the same order. `median` is defined once in `campaign.py` and used by `drift_ok`.

**4. Executable validation (run while writing this plan).** Every Python block was
extracted from this document, compiled, and the pure logic executed. It found four
real defects, now fixed here:

- `judge.py` had **no question to show the judge** — the record never stored the
  prompts. Fixed by adding `prompts` to the record in Task 2.
- `drift_ok` used `all(... is False)`, so a **single** task regressing in the
  closing witness would not have voided the campaign — which is precisely what the
  canary exists to catch. Now compared per task.
- Three "expected N tests" counts were wrong; an executor comparing against them
  would have read a mismatch as breakage.
- **`TestAggregate` was wrong, not the implementation.** The tests assumed raw
  verdicts already described the variant. An executor would have "fixed"
  `aggregate` to satisfy them and thereby **inverted every judge verdict** while
  everything still looked plausible. The tests now pin each direction separately
  and add a position-bias-cancellation case.

Verified passing: `scoring.py` 13/13, and the `campaign`/`judge` pure logic 13/13.
The remaining blocks (HTTP-driving code in `bench.py`, `variants.py`, `campaign.py`
`main`) are compile-checked only — they are exercised by the live-server steps.
