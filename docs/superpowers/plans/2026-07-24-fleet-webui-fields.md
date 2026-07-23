# Fleet Web-UI Fields — Implementation Plan (Plan 4b of the Fleet feature)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Configure a fleet project from the web UI instead of hand-editing `collections.json` — extend the existing collection context editor with a **Fleet project** section: a role toggle, an **engine** dropdown (omnis/claude), a **depends_on** multi-select, and (for the claude engine) a **claude_allowed_tools** override editor.

**Architecture:** Purely additive on top of Plans 1-3. The collection profile already stores `role`/`engine`/`depends_on`/`claude_allowed_tools` (Plans 1-3). Task 1 extends the existing `PATCH /api/collections/:name` to accept them and `GET …/context` to return them (mirroring how `squad`/`cwd`/`memory_size` are already handled via `UpdateCollectionProfile`). Task 2 adds the Fleet section to the existing `collectionContextDialog` ([web/app.js](web/app.js)) + its save path + the i18n keys. No new routes, no new backend concepts.

**Tech Stack:** Go (gin route); vanilla JS (`web/app.js`, no framework); the i18n runtime (`web/i18n/<locale>.json` + `make i18n`); standard `go test`.

## Global Constraints

- **Purely additive / no-op:** a collection where the Fleet toggle is off behaves exactly as today; existing non-fleet collections are unaffected. The PATCH keeps its pointer-field "only fields present in the body change" semantics.
- **Engine values** exactly `""` | `"omnis"` | `"claude"`; **role** exactly `""` | `"project"`. The PATCH rejects anything else.
- **i18n mandatory** (CLAUDE.md web-UI policy): every new user-facing string is a `tr("collections.…")` key added to **all four** locales (`en`/`fr`/`es`/`de`), then `make i18n` regenerates `web/i18n/locales.js`, and the `?v=` query on the `app.js` + `i18n/locales.js` script tags in `web/index.html` is bumped. Do NOT translate `omnis`/`claude` (product/tool nouns — glossary).
- **Follow the existing `.cc-*` editor conventions** in `collectionContextDialog` (label rows, `ccHelpButton`, the `submit()` return object, the `editCollectionContext` PATCH body).
- **No new backend concept:** reuse `UpdateCollectionProfile` + `CollectionProfileFull` (the same setters the fleet profile fields already use).
- **English only** for Go strings/comments.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `server/collections.go` (modify) | `handleUpdateCollection` body gains `Role`/`Engine`/`DependsOn`/`ClaudeAllowedTools` (validated, threaded through `UpdateCollectionProfile`); `handleGetCollectionContext` returns them. | 1 |
| `server/collections_test.go` (create/extend) | PATCH round-trips the fleet fields; bad engine/role rejected. | 1 |
| `web/app.js` (modify) | `collectionContextDialog` Fleet section (toggle + engine + depends_on + allowlist); snapshot wiring; `submit()` + `editCollectionContext` PATCH body. | 2 |
| `web/i18n/{en,fr,es,de}.json` (modify) | New `collections.*` keys. | 2 |
| `web/i18n/locales.js` (regen) | `make i18n`. | 2 |
| `web/index.html` (modify) | Bump `?v=` on `app.js` + `i18n/locales.js`. | 2 |

---

### Task 1: PATCH + GET the fleet profile fields

**Files:** `server/collections.go` (`handleUpdateCollection` ~136-241; `handleGetCollectionContext` ~300-321); test `server/collections_test.go`

**Interfaces produced:** `PATCH /api/collections/:name` accepts `role`/`engine`/`depends_on`/`claude_allowed_tools`; `GET …/context` returns them.

- [ ] **Step 1: Write the failing route test**

Add to `server/collections_test.go` (grep the file for how existing collection-route tests build the gin router + `serverDeps`; reuse that exact harness — if none exists, mirror `server/fleet_dispatch_test.go`'s direct-handler style by calling the sessions setters and asserting via `CollectionProfileFull`, plus one router-level test if a router helper exists):

```go
func TestUpdateCollectionFleetFields(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddCollection("Svc"); err != nil {
		t.Fatal(err)
	}
	// Drive the PATCH handler with a fleet body (use the same router/deps harness
	// the other collection-route tests use; if they call the handler via httptest,
	// do that — otherwise assert the profile-setter path the handler runs).
	// After a PATCH with role=project, engine=claude, depends_on=[Other],
	// claude_allowed_tools=[Read,Bash(go test:*)]:
	got := sessions.CollectionProfileFull("Svc")
	if got.Role != "project" || got.Engine != "claude" {
		t.Fatalf("role/engine not set: %+v", got)
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != "Other" {
		t.Fatalf("depends_on not set: %+v", got.DependsOn)
	}
	if len(got.ClaudeAllowedTools) != 2 {
		t.Fatalf("allowlist not set: %+v", got.ClaudeAllowedTools)
	}
}

func TestUpdateCollectionRejectsBadEngine(t *testing.T) {
	// A PATCH with engine="python" must 400 (or the validation helper returns an error).
}
```

> Fill the two tests in against the real router harness in `server/collections_test.go` (or `server/*_test.go`). The KEY assertions: fleet fields round-trip through the PATCH → `CollectionProfileFull`; `engine="python"` (and `role="folder"`) are rejected with `400`. If the collection-route tests use `httptest.NewRequest`/`router.ServeHTTP`, use that; the point is to exercise `handleUpdateCollection`, not just the sessions setter.

- [ ] **Step 2: Run to verify it fails** — `go test ./server/ -run 'TestUpdateCollectionFleetFields|TestUpdateCollectionRejectsBadEngine' -v` → FAIL (fields ignored / not rejected).

- [ ] **Step 3: Extend the PATCH body + validation + threading**

In `handleUpdateCollection`'s `body` struct (server/collections.go:143-150), add:

```go
		Role               *string   `json:"role"`
		Engine             *string   `json:"engine"`
		DependsOn          *[]string `json:"depends_on"`
		ClaudeAllowedTools *[]string `json:"claude_allowed_tools"`
```

After the existing scalar block (before the `d.PushEvents` broadcast at ~232), add a fleet block:

```go
		// Fleet project fields (role/engine/depends_on/claude_allowed_tools). Only
		// fields present in the body change; validate role + engine against their
		// closed sets. Applied via UpdateCollectionProfile like the other scalars.
		if body.Role != nil || body.Engine != nil || body.DependsOn != nil || body.ClaudeAllowedTools != nil {
			if body.Role != nil {
				r := strings.TrimSpace(*body.Role)
				if r != "" && r != "project" {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role (want \"project\" or empty)"})
					return
				}
			}
			if body.Engine != nil {
				e := strings.TrimSpace(*body.Engine)
				if e != "" && e != "omnis" && e != "claude" {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid engine (want omnis|claude or empty)"})
					return
				}
			}
			if err := sessions.UpdateCollectionProfile(current, func(p *sessions.CollectionProfileData) {
				if body.Role != nil {
					p.Role = strings.TrimSpace(*body.Role)
				}
				if body.Engine != nil {
					p.Engine = strings.TrimSpace(*body.Engine)
				}
				if body.DependsOn != nil {
					p.DependsOn = *body.DependsOn
				}
				if body.ClaudeAllowedTools != nil {
					p.ClaudeAllowedTools = *body.ClaudeAllowedTools
				}
			}); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
```

> `UpdateCollectionProfile` already trims/dedups slices via `cleanStrings` on write (Plan 1/3), so passing the raw slices through is correct — do not re-clean here.

- [ ] **Step 4: Return the fields from GET context**

In `handleGetCollectionContext`'s `gin.H` (server/collections.go:308-319), add:

```go
			"role":                 prof.Role,
			"engine":               prof.Engine,
			"depends_on":           prof.DependsOn,
			"claude_allowed_tools": prof.ClaudeAllowedTools,
```

- [ ] **Step 5: Run to verify it passes** — the two tests + `go test ./server/... -run Collection` + `go build ./...` → PASS.

- [ ] **Step 6: Commit**
```bash
git add server/collections.go server/collections_test.go
git commit -m "feat(fleet): PATCH/GET fleet project fields on the collection route"
```

---

### Task 2: Fleet section in the collection editor + i18n

**Files:** `web/app.js` (`collectionContextDialog` ~6325; `editCollectionContext` ~6219); `web/i18n/{en,fr,es,de}.json`; `web/i18n/locales.js` (regen); `web/index.html` (`?v=` bump)

**Interfaces consumed:** the Task-1 PATCH/GET fields; the in-memory collections list (for depends_on candidates).

- [ ] **Step 1: Add the i18n keys (en first)**

In `web/i18n/en.json`, add under the `collections.*` group:

```json
"collections.fleetSection": "Fleet project",
"collections.fleetToggle": "Make this collection a fleet project",
"collections.fleetToggleHelp": "Fleet projects can be coordinated together by the Fleet squad: the Conductor plans work across them and dispatches each project's task to its own driver.",
"collections.fleetEngine": "Engine",
"collections.fleetEngineOmnis": "omnis (Coding squad)",
"collections.fleetEngineClaude": "claude (external Claude Code)",
"collections.fleetDependsOn": "Depends on",
"collections.fleetDependsOnHelp": "Other fleet projects this one depends on (e.g. it consumes their shared contract). The Conductor changes dependencies before the projects that depend on them.",
"collections.fleetAllowlist": "Claude tool allowlist",
"collections.fleetAllowlistHelp": "One permission rule per line (Claude Code --allowedTools syntax, e.g. Bash(go test:*)). Empty = the safe default (read/edit + read-only git). Only used by the claude engine.",
"collections.fleetAllowlistPlaceholder": "Read\nEdit\nBash(go test:*)",
"collections.fleetNoProjects": "No other fleet projects yet."
```

Then add the SAME keys to `fr.json`, `es.json`, `de.json` with translations (do NOT translate `omnis`, `claude`, `Claude Code`, `--allowedTools`, `Bash(go test:*)`, `Fleet`, `Conductor` — glossary nouns/identifiers). Keep the engine option *labels*' parenthetical translated but the engine ids `omnis`/`claude` literal.

- [ ] **Step 2: Regenerate the bundle** — `make i18n` (regenerates `web/i18n/locales.js`). Expect a warning only if a locale is missing a key — fix any it reports.

- [ ] **Step 3: Add the Fleet section markup to the dialog**

In `collectionContextDialog` (web/app.js), insert a Fleet section into the `body.innerHTML` template — place it right after the `.cc-defaults-row` block (after line ~6345, before the instructions field):

```javascript
      <div class="cc-fleet" >
        <label class="cc-fleet-toggle">
          <input type="checkbox" class="cc-fleet-cb"> <span class="cc-label-row cc-fleet-label">${escHtml(tr("collections.fleetToggle"))}</span>
        </label>
        <div class="cc-fleet-fields" hidden>
          <label class="user-cmd-field">
            <span class="user-cmd-field-label">${escHtml(tr("collections.fleetEngine"))}</span>
            <select class="cc-fleet-engine">
              <option value="omnis">${escHtml(tr("collections.fleetEngineOmnis"))}</option>
              <option value="claude">${escHtml(tr("collections.fleetEngineClaude"))}</option>
            </select>
          </label>
          <div class="user-cmd-field">
            <span class="user-cmd-field-label">${escHtml(tr("collections.fleetDependsOn"))}</span>
            <div class="cc-fleet-deps"></div>
          </div>
          <label class="user-cmd-field cc-fleet-allow-wrap" hidden>
            <span class="user-cmd-field-label">${escHtml(tr("collections.fleetAllowlist"))}</span>
            <textarea class="cc-fleet-allow" spellcheck="false" placeholder="${escHtml(tr("collections.fleetAllowlistPlaceholder"))}"></textarea>
          </label>
        </div>
      </div>`;
```

(append it to the existing template string — mind the existing closing backtick: fold this block in *before* the final `` `; ``.)

- [ ] **Step 4: Wire the section (snapshot → controls, toggle/engine visibility, depends_on checkboxes)**

After the existing field-population lines (~6372-6380, after `body.querySelector(".cc-mem-label")…`), add:

```javascript
    // ── Fleet project section ──
    const fleetCb = body.querySelector(".cc-fleet-cb");
    const fleetFields = body.querySelector(".cc-fleet-fields");
    const engineSel = body.querySelector(".cc-fleet-engine");
    const allowWrap = body.querySelector(".cc-fleet-allow-wrap");
    const allowTa = body.querySelector(".cc-fleet-allow");
    const depsBox = body.querySelector(".cc-fleet-deps");
    body.querySelector(".cc-fleet-label").prepend(ccHelpButton(tr("collections.fleetToggleHelp")));

    fleetCb.checked = (snap.role || "") === "project";
    engineSel.value = (snap.engine === "claude") ? "claude" : "omnis";
    allowTa.value = (snap.claude_allowed_tools || []).join("\n");
    // depends_on candidates = every OTHER known collection (General excluded).
    const depSet = new Set((snap.depends_on || []).map((s) => s.toLowerCase()));
    const others = (lastCollections || []).filter((c) => c.name !== name && c.name !== "General");
    if (others.length === 0) {
      depsBox.innerHTML = `<span class="cc-fleet-empty">${escHtml(tr("collections.fleetNoProjects"))}</span>`;
    } else {
      depsBox.innerHTML = others.map((c) =>
        `<label class="cc-fleet-dep"><input type="checkbox" value="${escHtml(c.name)}"${depSet.has(c.name.toLowerCase()) ? " checked" : ""}> ${escHtml(c.name)}</label>`
      ).join("");
    }
    const syncFleetVis = () => {
      fleetFields.hidden = !fleetCb.checked;
      allowWrap.hidden = !(fleetCb.checked && engineSel.value === "claude");
    };
    fleetCb.addEventListener("change", syncFleetVis);
    engineSel.addEventListener("change", syncFleetVis);
    syncFleetVis();
```

> `lastCollections` is the in-memory collections list the rail already caches. Grep `web/app.js` for the variable that holds the last `/api/collections` payload (it may be `lastCollections`, `collectionsCache`, or the arg passed to `renderCollections`). Use whatever actually exists; if there's no cache, call `loadCollections()`'s data or fetch `/api/collections` once at dialog open. Report which you used.

- [ ] **Step 5: Include the fields in `submit()`**

Extend the `submit()` return object (web/app.js ~6479-6486) with:

```javascript
      role: fleetCb.checked ? "project" : "",
      engine: fleetCb.checked ? engineSel.value : "",
      depends_on: fleetCb.checked ? Array.from(depsBox.querySelectorAll("input:checked")).map((i) => i.value) : [],
      claude_allowed_tools: (fleetCb.checked && engineSel.value === "claude")
        ? allowTa.value.split("\n").map((s) => s.trim()).filter(Boolean) : [],
```

(Turning the toggle OFF sends `role:""` + cleared fleet fields, so a collection stops being a project cleanly.)

- [ ] **Step 6: Send them in the PATCH**

In `editCollectionContext` (web/app.js ~6229-6231), extend the PATCH body:

```javascript
      body: JSON.stringify({
        squad: chosen.squad, cwd: chosen.cwd, memory_size: chosen.memory_size, auto_update: chosen.auto_update,
        role: chosen.role, engine: chosen.engine, depends_on: chosen.depends_on, claude_allowed_tools: chosen.claude_allowed_tools,
      }),
```

- [ ] **Step 7: Minimal CSS** — add to the collections CSS partial (grep for `.cc-defaults-row` to find the file, e.g. `web/css/features/dialogs.css` or `collections.css`):

```css
.cc-fleet { border-top: 1px solid var(--border, #333); padding-top: .6rem; margin-top: .2rem; }
.cc-fleet-toggle { display: flex; align-items: center; gap: .4rem; }
.cc-fleet-fields { display: flex; flex-direction: column; gap: .5rem; margin-top: .5rem; }
.cc-fleet-deps { display: flex; flex-wrap: wrap; gap: .5rem; }
.cc-fleet-dep { display: inline-flex; align-items: center; gap: .3rem; }
.cc-fleet-allow textarea, textarea.cc-fleet-allow { min-height: 4rem; font-family: var(--mono, monospace); }
.cc-fleet-empty { opacity: .6; font-size: .85em; }
```

- [ ] **Step 8: Bump the cache-buster** — in `web/index.html`, bump the `?v=` query on the `i18n/locales.js` and `app.js` `<script>` tags (find the current value and increment it).

- [ ] **Step 9: Verify** — `make i18n` (clean, no missing-key warnings for the new keys), `go build ./...` (unchanged Go still builds), and load-test in the browser per the memory recipe: `OMNIS_WEB_DIR=$(pwd)/web bin/omnis-server` (no token) → open a collection's **Edit context…**, toggle Fleet on, pick engine=claude, tick a dependency, enter an allowlist line, Save; reopen and confirm it round-trips; toggle off and confirm role clears. (If a browser smoke isn't possible in the exec env, state that and rely on: the JSON parses, `make i18n` is clean, and the PATCH/GET contract is covered by Task 1's Go test.)

- [ ] **Step 10: Commit**
```bash
git add web/app.js web/i18n/ web/index.html web/css/
git commit -m "feat(fleet): configure fleet projects from the collection editor (engine/deps/allowlist)"
```

---

## Self-Review

**Spec coverage (§6 "Web UI minimal"):**
- Engine dropdown + depends_on multi-select + allowlist editor on the collection editor → Task 2. ✓
- Backed by the existing profile fields via the existing PATCH/GET route (no new backend concept) → Task 1. ✓
- Role toggle makes a collection a fleet project / clears it → Task 2 `submit()` + Task 1 role validation. ✓
- i18n across en/fr/es/de + `make i18n` + `?v=` bump (CLAUDE.md web policy) → Task 2. ✓
- **Deferred (out of scope):** worktree fork isolation (Plan 4a); a dedicated fleet dashboard; depends_on validation that targets *are* projects (the fleet `Validate`/`fleet_projects` already reports bad edges at plan time — the editor stays permissive).

**Placeholder scan:** the grep-the-existing-pattern notes (the collections-route test harness, the `lastCollections` cache var, the CSS partial path, the current `?v=` value) each name the exact thing to find + a fallback. No `TBD`.

**Type consistency:** the PATCH body keys (`role`/`engine`/`depends_on`/`claude_allowed_tools`) match the GET snapshot keys (Task 1) and the web `submit()`/PATCH body + snapshot reads (Task 2). Profile fields are the ones Plans 1-3 added (`CollectionProfileData.{Role,Engine,DependsOn,ClaudeAllowedTools}`). Engine values `omnis`/`claude` match `fleet.Engine` + the editor's option values.
