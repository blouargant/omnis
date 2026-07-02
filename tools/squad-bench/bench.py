#!/usr/bin/env python3
"""
squad-bench — measure how an omnis squad handles a task, for benchmarking
different models on different tasks.

Dependency-free (Python stdlib only), mirroring tools/model-probe/probe.py.

It drives the running omnis-server HTTP API exactly like the web UI does:
create a session pinned to a squad, send one task prompt, stream the SSE, and
reduce the event stream to a metrics record — so you can change an agent's
model (via Settings / config + reload) and re-run the same task to compare.

What it measures per run
------------------------
  wall_ms / ttfb_ms      total time, and time to first content event
  token_events           # of streamed `token` frames  (high => model streams
                         token-by-token; ~1-3 => coarse/buffered/non-streaming)
  delegated / redispatches  did the leader call sub-agents, and did it call the
                         SAME sub-agent more than once (a retry / flailing signal)
  leader_tools           {tool: count} the leader called directly
  subagent_tools         {agent: {tool: count}} each sub-agent ran internally
  models                 {agent: {in$/M, out$/M, prompt_tok, out_tok, calls,
                         est_cost_usd}}  (price IS the model's identity here)
  subagent_errors        sub-agent results that were empty or carried an error
                         (e.g. "context deadline exceeded" == endpoint too slow)
  ask_user               # of permission prompts the run raised (want 0 for a
                         squad whose read-only members are allow-listed)
  correct                if the task declares `expect`, whether the final answer
                         matched (substring, or /regex/)
  status                 done | timeout | cancelled | error

Usage
-----
  python3 bench.py --suite                      # run tasks.json against defaults
  python3 bench.py --task search-single         # one task by id
  python3 bench.py --suite --repeat 3           # 3 samples each (models vary run-to-run)
  python3 bench.py --task search-single --json  # machine-readable record only
  python3 bench.py --suite --out results.jsonl  # append one JSON record per run

  # Bench a different model: change the agent's model_ref (Settings → Agent, or
  # edit models.json / agent.json + POST /api/config/reload), then re-run the
  # same task. The `models` block in each record captures which model was active.

Env / flags: --server (default http://127.0.0.1:8080), --token (or
OMNIS_SERVER_TOKEN), --deadline seconds (default 420), --keep (don't delete the
session), --cwd (override task cwd).
"""
import argparse
import copy
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))


# ----------------------------------------------------------------------------- HTTP / SSE
def _req(method, base, path, token, body=None, timeout=600):
    url = base.rstrip("/") + path
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    return urllib.request.urlopen(r, timeout=timeout)


def api(method, base, path, token, body=None, timeout=60):
    with _req(method, base, path, token, body, timeout) as resp:
        raw = resp.read().decode("utf-8", "replace")
    return json.loads(raw) if raw.strip() else {}


def iter_sse(resp):
    """Yield (seq:int|None, event:str, data:dict) frames from an SSE stream."""
    seq = ev = None
    buf = []
    for raw in resp:
        line = raw.decode("utf-8", "replace").rstrip("\r\n")
        if line == "":
            if ev is not None:
                blob = "\n".join(buf)
                try:
                    data = json.loads(blob) if blob else {}
                except Exception:
                    data = {"_raw": blob}
                yield seq, ev, data
            seq = ev = None
            buf = []
            continue
        if line.startswith("id:"):
            try:
                seq = int(line[3:].strip())
            except ValueError:
                pass
        elif line.startswith("event:"):
            ev = line[6:].strip()
        elif line.startswith("data:"):
            buf.append(line[5:].lstrip())


# ----------------------------------------------------------------------------- metrics
def fresh():
    return {
        "status": "error",
        "ttfb_ms": None,
        "token_events": 0,
        "leader_tools": {},
        "subagent_tools": {},          # agent -> {tool: count}
        "delegations": {},             # agent -> count (leader called it)
        "models": {},                  # agent -> usage/cost
        "subagent_errors": [],
        "ask_user": 0,
        "answer": "",
        "_answer_parts": [],
        "_seq": 0,
    }


def note_model(m, u):
    a = u.get("agent") or "?"
    e = m["models"].setdefault(
        a, {"in_per_m": u.get("in_price_per_m"), "out_per_m": u.get("out_price_per_m"),
            "prompt_tok": 0, "out_tok": 0, "cache_read_tok": 0, "calls": 0, "est_cost_usd": 0.0})
    e["prompt_tok"] += u.get("prompt_tokens") or 0
    e["out_tok"] += u.get("output_tokens") or 0
    e["cache_read_tok"] += u.get("cache_read_tokens") or 0
    e["calls"] += 1
    inp = (u.get("in_price_per_m") or 0) / 1e6
    outp = (u.get("out_price_per_m") or 0) / 1e6
    crp = (u.get("cache_read_price_per_m") or 0) / 1e6
    e["est_cost_usd"] = round(
        e["est_cost_usd"]
        + (u.get("prompt_tokens") or 0) * inp
        + (u.get("output_tokens") or 0) * outp
        + (u.get("cache_read_tokens") or 0) * crp, 6)


def consume(resp, m, agents, t0, deadline):
    """Fold an SSE stream into m. Return True once a `done` frame is seen."""
    for seq, ev, d in iter_sse(resp):
        if seq is not None:
            m["_seq"] = max(m["_seq"], seq)
        if ev in ("token", "message"):
            if m["ttfb_ms"] is None:
                m["ttfb_ms"] = int((time.time() - t0) * 1000)
            if ev == "token":
                m["token_events"] += 1
            txt = d.get("text") or d.get("message") or ""
            if txt:
                m["_answer_parts"].append(txt)
        elif ev == "tool_call":
            name = d.get("name") or "?"
            if name in agents:                       # leader delegating to a sub-agent
                m["delegations"][name] = m["delegations"].get(name, 0) + 1
            else:
                m["leader_tools"][name] = m["leader_tools"].get(name, 0) + 1
        elif ev == "agent_tool_call":
            a = d.get("agent") or "?"
            t = d.get("name") or "?"
            m["subagent_tools"].setdefault(a, {})
            m["subagent_tools"][a][t] = m["subagent_tools"][a].get(t, 0) + 1
        elif ev == "tool_result":
            _scan_subagent_result(m, d, agents)
        elif ev == "turn_usage":
            note_model(m, d)
        elif ev == "ask_user":
            m["ask_user"] += 1
        elif ev == "done":
            m["status"] = "done"
            return True
        if time.time() - t0 > deadline:
            return False
    return False


def _scan_subagent_result(m, d, agents):
    name = d.get("name") or d.get("tool") or ""
    if name not in agents:
        return
    resp = d.get("response")
    blob = json.dumps(resp) if not isinstance(resp, str) else resp
    low = blob.lower()
    empty = (not blob) or blob in ("{}", '{"results": []}', '{"results":[]}')
    if empty or "deadline exceeded" in low or '"error"' in low or "timeout" in low:
        snippet = blob[:200] if blob else "(empty result)"
        m["subagent_errors"].append({"agent": name, "detail": snippet})


# ----------------------------------------------------------------------------- run one task
def prepare_cwd(task, override):
    if override:
        return override, None
    cwd = task.get("cwd", "")
    if cwd == "sandbox":
        src = os.path.join(HERE, "sandbox")
        tmp = tempfile.mkdtemp(prefix="squadbench-")
        for f in os.listdir(src):
            shutil.copy2(os.path.join(src, f), tmp)
        try:  # isolate + make any accidental edit trivially revertible
            subprocess.run(["git", "init", "-q"], cwd=tmp, check=False)
            subprocess.run(["git", "add", "-A"], cwd=tmp, check=False)
            subprocess.run(["git", "-c", "user.email=b@b", "-c", "user.name=b",
                            "commit", "-qm", "seed"], cwd=tmp, check=False)
        except Exception:
            pass
        return tmp, tmp
    return cwd or os.getcwd(), None


def run_task(base, token, task, agents, deadline, keep, cwd_override):
    cwd, tmp = prepare_cwd(task, cwd_override)
    sess = api("POST", base, "/api/sessions", token,
               {"squad": task["squad"], "dir": cwd, "name": "bench-" + task["id"]})
    sid = sess.get("session_id") or sess.get("id")
    m = fresh()
    m["task"] = task["id"]
    m["squad"] = task["squad"]
    m["cwd"] = cwd
    m["session_id"] = sid
    t0 = time.time()
    done = False
    try:
        resp = _req("POST", base, f"/api/sessions/{sid}/messages", token,
                    {"prompt": task["prompt"]}, timeout=deadline)
        done = consume(resp, m, agents, t0, deadline)
    except (urllib.error.URLError, ConnectionError, TimeoutError):
        pass
    # resilient reconnect for long / disconnected turns
    while not done and time.time() - t0 < deadline:
        try:
            resp = _req("GET", base,
                        f"/api/sessions/{sid}/messages/stream?from={m['_seq']}",
                        token, timeout=deadline)
            if getattr(resp, "status", 200) == 204:
                done = True
                m["status"] = "done"
                break
            done = consume(resp, m, agents, t0, deadline)
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            time.sleep(1)
    if not done:
        m["status"] = "timeout"
        try:
            api("POST", base, f"/api/sessions/{sid}/cancel", token, {})
            m["status"] = "cancelled"
        except Exception:
            pass
    m["wall_ms"] = int((time.time() - t0) * 1000)
    m["redispatches"] = sum(v - 1 for v in m["delegations"].values() if v > 1)
    m["answer"] = "".join(m["_answer_parts"]).strip()
    m["total_cost_usd"] = round(sum(x["est_cost_usd"] for x in m["models"].values()), 6)
    m["correct"] = check_expect(task.get("expect"), m["answer"])
    for k in ("_answer_parts", "_seq"):
        m.pop(k, None)
    if not keep and sid:
        try:
            api("DELETE", base, f"/api/sessions/{sid}", token)
        except Exception:
            pass
    if tmp:
        shutil.rmtree(tmp, ignore_errors=True)
    return m


def check_expect(expect, answer):
    if not expect:
        return None
    if expect.startswith("/") and expect.endswith("/") and len(expect) > 1:
        return bool(re.search(expect[1:-1], answer or "", re.I))
    return (expect.lower() in (answer or "").lower())


# ----------------------------------------------------------------------------- output
def summarize(m):
    ok = {True: "PASS", False: "FAIL", None: "n/a"}[m.get("correct")]
    print(f"\n=== {m['task']}  [squad={m['squad']}]  status={m['status']}  correct={ok} ===")
    print(f"  wall={m['wall_ms']}ms  ttfb={m['ttfb_ms']}ms  token_events={m['token_events']}"
          f"  ask_user={m['ask_user']}")
    if m["delegations"]:
        deleg = ", ".join(f"{a}×{n}" for a, n in m["delegations"].items())
        print(f"  delegated: {deleg}   redispatches={m['redispatches']}")
    else:
        print("  delegated: (none — leader did it itself)")
    for a, u in m["models"].items():
        print(f"  model[{a}]: ${u['in_per_m']}/{u['out_per_m']} per M"
              f"  tok={u['prompt_tok']}in/{u['out_tok']}out  calls={u['calls']}"
              f"  ~${u['est_cost_usd']}")
    print(f"  total est cost: ${m['total_cost_usd']}")
    if m["subagent_tools"]:
        for a, tools in m["subagent_tools"].items():
            print(f"  {a} ran: " + ", ".join(f"{t}×{n}" for t, n in tools.items()))
    if m["subagent_errors"]:
        print(f"  ⚠ sub-agent errors ({len(m['subagent_errors'])}):")
        for e in m["subagent_errors"][:3]:
            print(f"      {e['agent']}: {e['detail']}")
    ans = (m["answer"] or "").replace("\n", " ")
    print(f"  answer: {ans[:180]}")


def main():
    ap = argparse.ArgumentParser(description="Benchmark an omnis squad on a task.")
    ap.add_argument("--server", default=os.environ.get("OMNIS_SERVER", "http://127.0.0.1:8080"))
    ap.add_argument("--token", default=os.environ.get("OMNIS_SERVER_TOKEN", ""))
    ap.add_argument("--tasks", default=os.path.join(HERE, "tasks.json"))
    ap.add_argument("--task", help="run a single task by id")
    ap.add_argument("--suite", action="store_true", help="run every task in the file")
    ap.add_argument("--repeat", type=int, default=1, help="samples per task")
    ap.add_argument("--deadline", type=int, default=420, help="per-run wall-clock cap (s)")
    ap.add_argument("--cwd", help="override the task working directory")
    ap.add_argument("--keep", action="store_true", help="don't delete the bench session")
    ap.add_argument("--out", help="append one JSON record per run to this file")
    ap.add_argument("--json", action="store_true", help="print JSON records only")
    args = ap.parse_args()

    with open(args.tasks) as f:
        catalog = json.load(f)["tasks"]
    if args.task:
        sel = [t for t in catalog if t["id"] == args.task]
        if not sel:
            sys.exit(f"no task '{args.task}' in {args.tasks}")
    elif args.suite:
        sel = catalog
    else:
        sys.exit("choose --suite or --task <id>  (list: "
                 + ", ".join(t["id"] for t in catalog) + ")")

    # all agent names across all squads => used to classify a leader tool_call as a delegation
    squads = api("GET", args.server, "/api/squads", args.token)
    agents = set()
    for s in squads.get("squads", []):
        agents.update(s.get("members", []))
        if s.get("leader"):
            agents.add(s["leader"])

    records = []
    for task in sel:
        for i in range(args.repeat):
            m = run_task(args.server, args.token, copy.deepcopy(task),
                         agents, args.deadline, args.keep, args.cwd)
            if args.repeat > 1:
                m["sample"] = i + 1
            records.append(m)
            if args.json:
                print(json.dumps(m))
            else:
                summarize(m)
            if args.out:
                with open(args.out, "a") as f:
                    f.write(json.dumps(m) + "\n")

    if not args.json and len(records) > 1:
        print("\n===== summary =====")
        for m in records:
            tag = {True: "PASS", False: "FAIL", None: " -- "}[m.get("correct")]
            print(f"  [{tag}] {m['task']:<16} {m['status']:<9} {m['wall_ms']:>6}ms"
                  f"  ${m['total_cost_usd']:<8} tok_ev={m['token_events']:<4}"
                  f" redispatch={m.get('redispatches',0)}")


if __name__ == "__main__":
    main()
