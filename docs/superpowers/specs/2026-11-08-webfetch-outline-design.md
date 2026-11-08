# WebFetch: outline instead of a cut page (design, 2026-11-08)

## Intent

When a fetched page is longer than the result limit, `WebFetch` today returns
the first 85 % and the last 15 % of a 32 000-character budget, cut blindly, with
a note telling the model to "fetch it again with a CSS `selector`". This design
replaces that with: **return the page's outline, not a slice of its content**,
and let the model ask for the section it needs. The same rule applies again to
a section that is itself too long, so a very large page is navigated level by
level.

Why it matters: a fetched page stays in the caller's context for every model
call that follows (a sub-agent re-sends its whole context at each step), so
27 000 characters of the wrong part of a page are paid many times over. And
the current cut is blind: the model cannot see what was removed, so it has to
guess a selector.

### The case that started this

Session `select-walrus` ("Fonctionnement du routage MoE multi-GPU", Knowledge
squad). The leader fetched
`https://docs.vllm.ai/en/latest/serving/expert_parallel_deployment/` and
answered: "I did not read the section « Layer Behavior with EP Enabled » (the
page was truncated)". Measured on that page:

- The body converts to 55 772 characters of Markdown. The article itself
  starts at character **29 818**: everything before it is the site navigation
  and two tables of contents. The section heading sits at character 33 950.
  The shaper kept the first 27 200 characters: navigation only, no section.
- The `<article>` alone is **25 862** characters: it fits under the limit, and
  the section would have been read on the first call.
- Following the cut note, the model re-fetched with
  `selector: "#layer-behavior-with-ep-enabled"`. On MkDocs the id is on the
  `<h3>` itself, so the selector returned the heading text and nothing else
  (105 bytes). The model then reported the gap instead of trying again.

So two defects: the page's boilerplate eats the budget, and the advice given
when the page is cut leads to a dead end on the most common documentation
generators.

## Measurements on real documentation pages

Markdown length of the whole `<body>` versus the content root once the
navigation is removed (`main` minus `nav`, `aside`, `header`, `footer`,
`[role=navigation]`), with goquery + html-to-markdown v2 (the libraries
`WebFetch` uses):

| Page | Generator | Body | Content | Headings | Where ids are |
|---|---|---:|---:|---:|---|
| vLLM expert parallel deployment | MkDocs Material | 55 772 | 25 857 | 32 | on `h2`–`h3` |
| vLLM engine args | MkDocs Material | 148 011 | 120 053 | 280 (260 `h4`) | on headings |
| Kubernetes Deployment | Hugo/Docsy | 148 455 | 61 636 | 53 | on headings |
| Docker `container run` | Hugo | 146 344 | 65 565 | 55 | on headings |
| MDN Cache-Control | custom | 69 255 | 26 987 | 38 | on headings |
| Python asyncio tasks | Sphinx | 62 587 | 58 779 | 26 | on `<section>` wrappers |
| PostgreSQL transaction isolation | DocBook | 28 993 | (no `main`) | 7 | on `div.sect1/sect2` wrappers |

What this shows:

- Removing navigation alone brings 2 of the 6 over-limit pages under 32 000
  characters (vLLM EP, MDN). It is the cheapest and largest gain.
- MkDocs Material puts its sidebars **inside** `<main>`: "take `main`" is not
  enough, navigation must be removed inside the content root too.
- Ids are either on the heading (MkDocs, Hugo, MDN) or on a wrapping
  container whose first heading is the section title (Sphinx `<section>`,
  DocBook `div.sectN`). Both shapes must be supported.
- The vLLM engine args page has 280 headings; a full outline is about
  20 000 characters. The outline must be depth-limited.
- That page is also the precise-lookup case that set the 32 000 limit
  (`--gpu-memory-utilization`, an `h4` among 260): the option sits at
  character 76 679 of the body. A text search inside the page (`find`, below)
  answers it without reading anything else.

## Goals

1. **A page that fits after navigation is removed is returned whole, in one
   call.** No change in the number of calls for most pages.
2. **A page that does not fit returns its outline only**, with an explicit
   notice that the content has not been read, the size of each section and an
   identifier to request it. No slice of content.
3. **A requested section that does not fit returns the outline of its
   sub-sections**, by the same rule, recursively.
4. **A section with no sub-headings that does not fit** (a long table, a
   code block, a changelog without headings) is returned in **parts**.
5. **A text search inside a page** (`find`) returns the passages containing a
   term, each labelled with the section it belongs to.
6. Content with no HTML structure (plain text, JSON, Markdown files, PDF) gets
   the same treatment where it can: outline from Markdown headings, parts and
   `find` otherwise.

## Non-goals

- Pages rendered by JavaScript (single-page apps). Out of scope, unchanged.
- Any model call inside `WebFetch` (no summarisation): the tool stays
  deterministic and free.
- Changing the 32 000-character limit. The 2026-11-01 bench showed 20 000
  loses precise lookups; the goal is to fill 32 000 better, not to raise or
  lower it.
- `WebSearch`, the web cache's TTL, the per-call tool caps (re-checked by the
  bench, not redesigned here).

## Anti-goals (a failure even if it works)

- **The caller answers from memory more often.** With a cut page it happened
  (the 2026-11-01 lookup at a 20 000 limit); with an outline the model has no
  content at all, so the temptation is stronger. Measured by the bench.
- **Needed content removed by extraction**, for example a table or a note
  inside an `aside`. Removal is limited to navigation (see below), and the
  model can always ask for the unextracted page.
- **More tokens or more calls in total** on the benchmark tasks.
- **Silent behaviour.** Today a `selector` that matches nothing returns the
  whole body without saying so. Every fallback must say what happened.

## Constraints

- Everything happens inside `WebFetch` (`core/tools/web.go`). The outline needs
  the document's structure, which the output shaper (text only) does not have.
  The shaper's `WebFetch` budget stays as a safety net that should no longer
  fire.
- The parameters stay simple enough for the weak models that call `WebFetch`
  (`web_agent` and `web_fetcher` on cheap tiers): one optional parameter per
  intent, ids copied verbatim from the outline. Same single-task schema.
- The tool description is re-sent on every model call of every agent that has
  the tool: keep it short (see "Fixed prompt of every agent" in CLAUDE.md).
- No new dependency: goquery and html-to-markdown v2 are already used.

## Decided in the conversation

The user decided:

- When the page exceeds the limit, return the outline with explanations
  instead of content.
- A section request whose result exceeds the limit returns that section's
  outline, so a very large page with large sub-sections can be navigated.

Proposed by me during the conversation and not contested (the user asked for
this spec right after); worth checking while reading:

- Remove navigation before measuring the page.
- A `section` parameter taking the identifier shown in the outline, and the
  size of each section in the outline.
- `part` for content that cannot be split by headings, `find` for precise
  lookups.
- Keep the 32 000 limit.

## Design

### Parameters

```
url       string, required.
section   string, optional. An identifier from an outline. A URL fragment
          (…/page#some-id) is treated as section="some-id" when section is
          not given.
find      string, optional. Return the passages containing this text
          (case-insensitive, literal), within the page or within `section`.
part      int, optional, 1-based. One part of a selection that has no
          sub-headings and does not fit.
selector  string, optional, kept for advanced use (a CSS selector). Its
          result goes through the same rules as a section.
raw       bool, optional. Skip navigation removal (escape hatch when
          extraction removed something needed). (My call; see Open points.)
```

`section` and `selector` are mutually exclusive (error if both). `find` and
`part` may be combined with either.

### Pipeline

1. Fetch as today (status check, size cap, content-type dispatch).
2. **Content root** (HTML): the first match of `main`, `[role=main]`, a
   single `article`, else `body`. Inside it, remove `nav`,
   `[role=navigation]`, `script`, `style`, `noscript`, and `header`, `footer`,
   `aside` elements that are direct layout children of the root (not inside
   the article's sections). If the root is `main` and it contains exactly one
   `article`, the article is the root. The exact rule is the builder's to tune
   against the fixtures and the bench; the invariant is "navigation and site
   chrome out, content in".
3. **Section tree**: a section is a heading (`h1`–`h6`) plus everything
   after it **in document order** up to the next heading of the same or a
   higher level, whatever the element nesting (Docsy, for example, wraps
   content in several `div`s, so "following siblings" is not enough). Levels
   are relative (an `h4` directly under an `h2` is one level down). Content
   before the first heading is the page's lead. A container (`section`, `div`)
   with an id whose first heading is its title gives that id to the heading's
   section (Sphinx, DocBook).

   Suggested implementation (builder's call): before converting, replace each
   heading's text with a unique sentinel carrying its level and id, convert the
   whole content root to Markdown **once**, then split the Markdown at the
   sentinels. This gives the tree, each section's Markdown and its size in one
   pass, and handles any nesting.
4. **Identifiers**: the heading's `id`, else the wrapper's `id`, else a
   generated positional id (`s2.3` for the third sub-section of the second
   top-level section). Positional ids are stable as long as the document is
   the same, which the document cache below guarantees within a session.
5. **Selection**: the whole root, or the section named by `section`, or the
   first `selector` match. A `selector` that matches a heading selects that
   heading's section.
6. **Rendering**: the selection's Markdown, blank lines collapsed.
   - It fits the limit (32 000): return it as today.
   - It does not fit and has sub-sections: return the outline (below).
   - It does not fit and has none: return part 1 of N.
   - `part` given: return that part, whether or not the selection has
     sub-sections (a model that wants to read sequentially can; the outline's
     notice points to `section` and `find` first). (My call.)
7. **`find`**: when given, the result is the matches only, never the whole
   selection, even if it fits. Search the selection's Markdown; return each
   matching block (paragraph, list item, table row with its header row,
   code-block lines with a few lines of context), labelled with its section id
   and title, at most 20 matches, total within the limit. The notice says how
   many matches there are and how many are shown.

**The limit** is one constant in `core/tools`, measured like the shaper
measures (string length), and the shaper's `WebFetch` budget
(`webFetchMaxChars` in `agent/coding_efficiency_plugin.go`) uses that constant
so the two cannot drift. Every result, notices included, stays within it.
`truncate` (the 50 000 head cut `WebFetch` applies today) is no longer used
by the HTML path.

### The outline result

```
[Page too long to return whole: 120 053 characters, limit 32 000. You have NOT
read its content yet. Fetch the part you need with section="<id>" (a parent
section returns everything under it if it fits), or search it with find="<text>".
Do not answer from this outline alone.]

# vLLM Engine Arguments
<lead, at most 1 000 characters>

- JSON CLI Arguments (json-cli-arguments) — 2.1k
- EngineArgs (engineargs) — 96k
  - ModelConfig (modelconfig) — 18k, 41 sub-sections
  - CacheConfig (cacheconfig) — 6.4k, 12 sub-sections
  …
- AsyncEngineArgs (asyncengineargs) — 1.2k
```

(Sizes in this example are illustrative.)

- Sizes are the Markdown length of each section, rounded (`2.1k`).
- Depth: as many levels as fit in an outline budget of about 4 000
  characters; deeper levels are folded into a count ("41 sub-sections"), which
  a `section` request on that entry opens. The first level is always listed
  whole, even past 4 000 characters; if it does not fit in the limit either
  (hundreds of top-level headings), the outline lists the entries that fit and
  ends with "… and N more top-level sections not shown: use find to locate
  one". (My call. Paging the outline with `part` was considered and dropped:
  `part` already means a slice of content.)
- A section outline (goal 3) has the same shape, starting from that section,
  with a breadcrumb line (`EngineArgs > CacheConfig`).
- The lead (title + first text before any heading, at most 1 000 characters)
  is included because it is often what tells the model whether this is the
  right page. (My call; cheap, can be dropped if the bench says so.)

### Parts

A selection with no sub-headings that does not fit is split into parts of at
most the limit, cut between top-level blocks (paragraphs, list items, table
rows — a table split keeps its header row in each part, code blocks between
lines). The result starts with
`[Part 2 of 4 of "<section title>" — fetch part=3 for the next]`.

### Non-HTML content

- **Markdown** (`text/markdown`, or a `text/plain` URL ending in `.md`): the
  section tree comes from ATX headings (`#`…), ignoring lines inside fenced
  code. Ids are GitHub-style slugs, de-duplicated with `-1`, `-2`.
- **Other text and JSON**: no tree; parts by lines, and `find`.
- **PDF**: parts by page ranges (`pdftotext -f/-l`), the notice giving the
  page count; `find` over the extracted text, reporting page numbers.

### Errors and fallbacks (all explicit)

- `section` not found: an error result naming the id, followed by the outline
  of the whole page.
- `selector` matches nothing: an error result saying so, followed by the
  outline (today it silently returns the whole body).
- `part` out of range: an error giving the number of parts.
- `find` with no match: "no match for <text> in <section/page>", plus the
  outline when the page did not fit.
- A page with no headings at all that does not fit: parts, with the notice.
- Every error string starts with `error ` so it stays out of the web cache, as
  today.

### Document cache

Today the cache stores the final result string per (root session, URL,
selector). Navigating an outline means several requests on the same page, which
must not download it again and must see the same positional ids. So:

- Cache the **processed document** (section tree + Markdown per section) per
  (root session, URL), with the same 15-minute TTL and session scoping as the
  existing web cache, and a byte budget across entries (my call: 64 MiB, evict
  oldest first).
- Results keep their own cache entry keyed by
  (URL, section, selector, part, find), as today.
- The URL fragment is stripped from both keys (the server never sees it), so
  `page#a` and `page#b` share one downloaded document.

### Prompt and agent changes

- Tool description: rewrite to state the new behaviour in two or three short
  sentences (long pages return an outline; `section`, `find`, `part`). Measure
  with `omnis-benches/prompt-size` before/after.
- Instructions that give the old advice ("pass a `selector` on long pages",
  "a section id"): `web_agent`, `web_fetcher`, `code_docs`, `knowledge_leader`.
  Replace with: read the outline, fetch the section you need, use `find` for a
  specific value.
- The shaper's `WebFetch` cut note (`budgetFor` in
  `agent/coding_efficiency_plugin.go`) points to `section`/`find` instead of
  `selector`.

## Verification

- **Unit tests** in `core/tools` on small synthetic HTML fixtures, one per
  structure measured above: ids on headings (MkDocs/Hugo), `<section>`
  wrappers (Sphinx), `div.sectN` wrappers (DocBook), headings without ids,
  sidebars inside `<main>`, an `aside` inside the article (must be kept), a
  page with no headings, a giant table, a Markdown file, a URL fragment, a
  selector on a heading, a selector that matches nothing. Fixtures are
  synthetic (not copies of the third-party pages).
- **Bench** (`omnis-benches/squad-bench`), before/after on the same models:
  `tasks-callvolume.json` (contains the engine-args lookup and a fetch of the
  same page by `web_fetcher`) and the Knowledge questions of
  `tasks-modelcost.json`, plus the MoE question of the originating session.
  Compare correctness, answers given without the source being read (found by
  reading the transcripts of the tasks where an outline was returned: did a
  section or `find` call follow before the answer?), number of `WebFetch`
  calls, tokens of `web_agent`/`web_fetcher`, and how often a call ends on its
  per-call cap (`web_agent` 20, `web_fetcher` 8).
- Ship only if correctness is not lower and total tokens are not higher.

## Left to the builder

- The exact content-root and removal rules, tuned on the fixtures.
- The outline depth algorithm and the 4 000-character outline budget.
- Match context for `find` and its limits (20 matches is a starting point).
- Positional id format.
- Whether `html_to_markdown` (the other tool in `NewWebTools`) shares the
  content-root extraction (probably not: it converts what it is given).

## Open points (mine, flagged for the reader)

- `raw` parameter: an escape hatch if extraction removes something needed.
  It adds a parameter to a schema meant to stay small; dropping it is
  reasonable if the fixtures and the bench show extraction never loses
  content.
- Lead in the outline: kept small (1 000 characters) and measurable.

## Documentation to update when shipping

- CLAUDE.md: the `WebFetch` paragraphs (budget, cut note, cache).
- `internal/features/FEATURES.md`: one bullet under the current in-development
  section ("Long web pages: research agents read the page's outline and fetch
  the section they need").
