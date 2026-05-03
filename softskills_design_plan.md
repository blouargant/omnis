# Soft-Skills Autonomous Creation System: Design Plan

This document outlines the architecture and implementation strategy for a self-evolving skill library that allows an LLM agent to learn from its sessions without the overhead of a RAG system.

## 1. Architecture: The Hierarchical Manifest Tree
Skills are stored in a nested directory structure. Each level contains a "Manifest" file that acts as a router for the agent.

**Root Directory:** `softskills/`

### Tree Structure:
```text
softskills/
├── library.md              # Root Manifest (Lists Subjects)
└── <subject>/
    ├── subject.md          # Subject Manifest (Lists Intentions)
    └── <intention>/
        ├── intention.md    # Intention Manifest (Lists specific Skills)
        └── <skill_name>.md # The actual executable skill/procedure
```

## 2. File Templates

### Level 1: `library.md` (The Librarian)
- **Role:** High-level domain routing.
- **Content:** A list of available subjects with brief "Trigger Keywords" for each.
- **Example:**
  ```markdown
  # Soft-Skills Library
  - **Kubernetes**: Use for cluster management, pod debugging, and helm charts. (Path: softskills/kubernetes/subject.md)
  - **Python**: Use for script optimization and library troubleshooting. (Path: softskills/python/subject.md)
  ```

### Level 2: `subject.md` (The Domain Specialist)
- **Role:** Narrowing down the user's intent within a domain.
- **Content:** List of intentions with "When to use" descriptions.

### Level 3: `intention.md` (The Playbook Index)
- **Role:** Selecting a specific procedure.
- **Content:** List of atomic skills.

### Level 4: `<skill_name>.md` (The Expert Procedure)
- **Role:** Execution.
- **Content:**
  - **Context:** Why this skill exists.
  - **Steps:** The optimized sequence of actions or commands.
  - **Constraints:** What to avoid.
  - **Validation:** How to know the skill succeeded.

## 3. The Curator Sub-Agent (The "Distiller")

The Curator is a specialized agent triggered at the end of a session or upon a specific directive.

### Workflow:
1. **Session Analysis:** Identifies successful multi-step solutions or non-trivial insights.
2. **Redundancy Audit:** 
   - **Cross-Platform Check:** Before creation, the Curator must verify if the functionality is already provided by a standard **Skill**, an existing **Soft-Skill**, or an **MCP/A2A agent**.
   - **Action:** If a match is found, creation is aborted to prevent duplication of labor.
3. **Generalization:** Removes session-specific data (IDs, paths, usernames).
4. **Hierarchy Mapping:** 
   - Finds or creates the correct `<subject>` and `<intention>`.
5. **Creation or Strategic Update:**
   - **If New:** Writes the new `<skill_name>.md`.
   - **If Existing:** Evaluates if the new experience offers a **Real Improvement** (e.g., handles an edge case, reduces steps, improves reliability). If the change is trivial or purely cosmetic, the update is rejected to prevent "incessant modification."
6. **Manifest Back-Propagation:**
   - Appends the new skill to `intention.md`.
   - Ensures `subject.md` and `library.md` are updated if new branches were created.

## 4. Navigation Protocol (The "Navigator")

To keep context light, the main agent follows these rules:
1. **Initial Check:** On complex tasks, the agent reads `softskills/library.md`.
2. **Pathing:** It follows the paths provided in the manifests turn-by-turn.
3. **Caching:** Once a skill is loaded, the path is kept in the current session memory to avoid re-navigation.

## 5. Implementation Phases

### Phase 1: Infrastructure
- Create `softskills/` directory.
- Initialize `softskills/library.md` with a "Meta" subject (how to use the library itself).

### Phase 2: The Curator Prompt
- Develop the System Prompt for the Curator Sub-Agent.
- Test it on a sample session log.

### Phase 3: Integration
- Add a "Navigation Directive" to the main agent's system prompt.
- Implement the "Back-Propagation" tool logic (safe appending to Markdown files).

## 6. Best Practices & Safety
- **Atomic Updates:** Manifests should be updated using surgical `replace` calls or structured appends to prevent corruption.
- **The "High-Bar" Update Rule:** Soft-skills should only be modified when new data significantly changes the success rate or efficiency of the procedure.
- **Tool-First Preference:** Always prefer a hard-coded Skill or MCP tool over a Soft-Skill if both cover the same use case.
- **Human Audit:** Periodically review `library.md` to ensure the taxonomy remains logical.
