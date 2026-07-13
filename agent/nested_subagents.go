package agent

import (
	"fmt"
	"sort"
	"strings"
)

// Nested sub-agents — `subagents` on any agent, not just a squad root.
//
// Until now only a squad ROOT could delegate: toolsForAgentConfig resolves tool
// GROUPS, and the agenttool wrappers were appended exclusively to the root's tool
// list. That restriction was arbitrary (an agenttool is just a tool, and an
// llmagent can hold one at any depth) and expensive: it forced the agent with the
// strongest — and costliest — model to also be the one accumulating raw retrieved
// data.
//
// Retrieval cost is QUADRATIC in an agent's tool calls: it runs its own flow loop
// and re-sends its whole accumulated context — every fetched page, grep hit, or
// pod log — on each model call. So the expensive model must never be the one
// holding the bulk. `subagents` lets ANY agent push bulk retrieval into a cheap
// gatherer, exactly the way a squad leader already delegates to code_scout /
// investigator / k8s_investigator.
//
// The contract that makes it safe: the cheap model does RETRIEVAL, the expensive
// one does JUDGMENT, and the interface between them is evidence with provenance —
// a quote + URL, a file:line + snippet, a pod + timestamp + log line — never a
// verdict and never a summary. A gatherer allowed to conclude puts the judgment
// you are paying the strong model for into the weak one.
//
// Squad `members` = what the LEADER may delegate to. An agent's `subagents` =
// what IT may delegate to. The build resolves the transitive closure of both, but
// only direct members become leader tools — so a pure gatherer can serve one
// specialist without cluttering the coordinator's tool list.

// validateSubAgentGraph rejects, at config-resolution time, any `subagents` graph
// the builder could not construct: an unknown or disabled target, a
// self-reference, a duplicate edge, the process-wide curator, or a cycle. Reported
// here (rather than mid-turn) so a bad edit fails loudly on the next reload.
func validateSubAgentGraph(agents []RuntimeAgentConfig) error {
	byName := make(map[string]RuntimeAgentConfig, len(agents))
	for _, a := range agents {
		byName[a.Name] = a
	}
	for _, a := range agents {
		if !a.Enabled || len(a.SubAgents) == 0 {
			continue
		}
		seen := make(map[string]bool, len(a.SubAgents))
		for _, dep := range a.SubAgents {
			switch {
			case dep == a.Name:
				return fmt.Errorf("agent %q lists itself in subagents", a.Name)
			case dep == "curator":
				return fmt.Errorf("agent %q lists the curator in subagents: the curator is a process-wide post-session hook, not a delegable agent", a.Name)
			case seen[dep]:
				return fmt.Errorf("agent %q lists %q twice in subagents", a.Name, dep)
			}
			seen[dep] = true

			d, ok := byName[dep]
			if !ok {
				return fmt.Errorf("agent %q lists unknown agent %q in subagents", a.Name, dep)
			}
			if !d.Enabled {
				return fmt.Errorf("agent %q lists disabled agent %q in subagents", a.Name, dep)
			}
		}
	}
	return detectSubAgentCycle(byName)
}

// detectSubAgentCycle reports the first cycle in the delegation graph. A cycle is
// unbuildable by construction — wiring a's nested tool needs b's agent object to
// exist, and b's needs a's — so it must be a config error, not a runtime surprise.
func detectSubAgentCycle(byName map[string]RuntimeAgentConfig) error {
	const (
		unvisited = iota
		onPath
		done
	)
	state := make(map[string]int, len(byName))
	var path []string

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case onPath:
			// Trim the path down to the cycle itself so the message names only the
			// agents actually involved.
			start := 0
			for i, n := range path {
				if n == name {
					start = i
					break
				}
			}
			return fmt.Errorf("subagents cycle: %s", strings.Join(append(append([]string{}, path[start:]...), name), " → "))
		case done:
			return nil
		}
		state[name] = onPath
		path = append(path, name)
		for _, dep := range byName[name].SubAgents {
			if _, ok := byName[dep]; !ok {
				continue // unknown name: already reported by validateSubAgentGraph
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return nil
	}

	// Deterministic iteration so a config with several cycles always reports the
	// same one.
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}

// subAgentClosure expands a squad's member set with every agent reachable through
// `subagents` edges, so a nested-only gatherer gets BUILT even though it is not a
// squad member. Roots keep their declared order; pulled-in agents are appended
// breadth-first.
func subAgentClosure(roots, catalogue []RuntimeAgentConfig) ([]RuntimeAgentConfig, error) {
	byName := make(map[string]RuntimeAgentConfig, len(catalogue))
	for _, a := range catalogue {
		byName[a.Name] = a
	}

	out := make([]RuntimeAgentConfig, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	add := func(cfg RuntimeAgentConfig) {
		if !seen[cfg.Name] {
			seen[cfg.Name] = true
			out = append(out, cfg)
		}
	}
	for _, r := range roots {
		if r.Enabled {
			add(r)
		}
	}
	// out doubles as the BFS queue: appends inside the loop extend it.
	for i := 0; i < len(out); i++ {
		for _, dep := range out[i].SubAgents {
			cfg, ok := byName[dep]
			if !ok || !cfg.Enabled {
				return nil, fmt.Errorf("agent %q: subagent %q is not an enabled agent", out[i].Name, dep)
			}
			add(cfg)
		}
	}
	return out, nil
}

// topoOrderSubAgents orders a closure so every agent comes AFTER the agents it
// delegates to — the build needs that, since wiring an agent's nested delegation
// tool requires the target's agent object to already exist. The graph is a DAG
// (validateSubAgentGraph rejects cycles), so a DFS post-order is a valid
// topological order.
func topoOrderSubAgents(cfgs []RuntimeAgentConfig) ([]RuntimeAgentConfig, error) {
	byName := make(map[string]RuntimeAgentConfig, len(cfgs))
	for _, c := range cfgs {
		byName[c.Name] = c
	}

	const (
		unvisited = iota
		onPath
		done
	)
	state := make(map[string]int, len(cfgs))
	out := make([]RuntimeAgentConfig, 0, len(cfgs))

	var visit func(c RuntimeAgentConfig) error
	visit = func(c RuntimeAgentConfig) error {
		switch state[c.Name] {
		case onPath:
			return fmt.Errorf("subagents cycle at %q", c.Name)
		case done:
			return nil
		}
		state[c.Name] = onPath
		for _, dep := range c.SubAgents {
			d, ok := byName[dep]
			if !ok {
				return fmt.Errorf("agent %q: subagent %q missing from the build closure", c.Name, dep)
			}
			if err := visit(d); err != nil {
				return err
			}
		}
		state[c.Name] = done
		out = append(out, c)
		return nil
	}

	for _, c := range cfgs {
		if err := visit(c); err != nil {
			return nil, err
		}
	}
	return out, nil
}
