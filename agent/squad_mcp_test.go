package agent

import (
	"reflect"
	"testing"
)

func TestSquadMCPServersUnionsMembersAndNestedSubAgents(t *testing.T) {
	rs := RuntimeSettings{
		Agents: []RuntimeAgentConfig{
			{Name: "lead", MCPServers: []string{"github"}},
			{Name: "coder", MCPServers: []string{"fs", "github"}},
			{Name: "critic", SubAgents: []string{"fetcher"}},
			{Name: "fetcher", MCPServers: []string{"browser"}},
			{Name: "other", MCPServers: []string{"jira"}},
		},
		Squads: []RuntimeSquadConfig{
			{Name: "dev", Leader: "lead", Members: []string{"coder", "critic"}},
			{Name: "omnis", Members: []string{"omnis"}},
		},
	}
	if got := squadMCPServers(rs, "dev"); !reflect.DeepEqual(got, []string{"browser", "fs", "github"}) {
		t.Fatalf("dev servers = %v", got)
	}
	if got := squadMCPServers(rs, "omnis"); len(got) != 0 {
		t.Fatalf("router squad servers = %v, want none", got)
	}
	if got := squadMCPServers(rs, "nope"); len(got) != 0 {
		t.Fatalf("unknown squad servers = %v", got)
	}
}
