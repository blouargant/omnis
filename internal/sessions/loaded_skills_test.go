package sessions

import "testing"

func TestRecordLoadedSkillDedupsAndCuts(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	const id = "skills-sess"

	if err := RecordLoadedSkill(id, LoadedSkill{Name: "review", Kind: SkillKindSkill, Agent: "coder", Instructions: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendConversationTurn(id, "q1", "a1"); err != nil {
		t.Fatal(err)
	}
	// Same skill again, same agent → one entry, count 2, newest body kept.
	_ = RecordLoadedSkill(id, LoadedSkill{Name: "review", Kind: SkillKindSkill, Agent: "coder", Instructions: "v2"})
	// Same name as a soft-skill is a distinct entry, first loaded in turn 1.
	_ = RecordLoadedSkill(id, LoadedSkill{Name: "review", Kind: SkillKindSoftskill, Agent: "coder", Instructions: "s"})
	// Failed/nameless loads are ignored.
	_ = RecordLoadedSkill(id, LoadedSkill{Name: "  ", Kind: SkillKindSkill})

	got, err := LoadConversationSkills(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Count != 2 || got[0].Instructions != "v2" || got[0].Turn != 0 {
		t.Errorf("skill entry = %+v", got[0])
	}
	if got[1].Kind != SkillKindSoftskill || got[1].Turn != 1 {
		t.Errorf("softskill entry = %+v", got[1])
	}

	// A turn appended after the loads must keep them.
	if err := AppendConversationTurn(id, "q2", "a2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadConversationSkills(id); len(got) != 2 {
		t.Fatalf("appending a turn dropped skills: %+v", got)
	}

	// Fork before turn 1 keeps only the skill loaded in turn 0.
	if _, err := ForkConversation(id, "skills-fork", "", 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadConversationSkills("skills-fork"); len(got) != 1 || got[0].Kind != SkillKindSkill {
		t.Errorf("fork skills = %+v", got)
	}

	// Rewinding to before turn 0 drops everything.
	if _, err := TruncateConversationTurns(id, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadConversationSkills(id); len(got) != 0 {
		t.Errorf("rewind kept skills: %+v", got)
	}
}
