package agent

import (
	"sort"
	"strings"
)

// squadMCPServers returns the MCP server names declared by the agents of a
// squad — its leader, members and their nested `subagents`, transitively —
// sorted and deduplicated. Backs the environment panel's Connecteurs card.
func squadMCPServers(rs RuntimeSettings, squadName string) []string {
	byName := map[string]RuntimeAgentConfig{}
	for _, a := range rs.Agents {
		byName[strings.ToLower(a.Name)] = a
	}
	var queue []string
	for _, sq := range rs.Squads {
		if strings.EqualFold(sq.Name, squadName) {
			if sq.Leader != "" {
				queue = append(queue, sq.Leader)
			}
			queue = append(queue, sq.Members...)
		}
	}
	seenAgent := map[string]bool{}
	servers := map[string]bool{}
	for len(queue) > 0 {
		name := strings.ToLower(queue[0])
		queue = queue[1:]
		if seenAgent[name] {
			continue
		}
		seenAgent[name] = true
		a, ok := byName[name]
		if !ok {
			continue
		}
		for _, s := range a.MCPServers {
			servers[s] = true
		}
		queue = append(queue, a.SubAgents...)
	}
	out := make([]string, 0, len(servers))
	for s := range servers {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SquadMCPServers returns the MCP servers available to a session running the
// named squad, from the generation the session is pinned to (never pins).
func (m *Manager) SquadMCPServers(sessionID, squadName string) []string {
	inst := m.Peek(sessionID)
	if inst == nil {
		return nil
	}
	return squadMCPServers(inst.Settings, squadName)
}
