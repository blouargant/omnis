# WebFetch Outline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `WebFetch` returns a page whole when it fits once its navigation is removed, and otherwise its outline, so the caller fetches only the section it needs (`section`), a text search (`find`) or a slice (`part`).

**Architecture:** A fetched page becomes a `webDoc`: a tree of sections, each holding its own Markdown, built once per page (HTML: content root extraction, then one Markdown conversion split at heading sentinels; Markdown files: ATX headings; text/PDF: no tree). The processed page is cached per (root session, URL). Rendering picks the whole selection, its outline, a part or `find` matches, always within one limit shared with the output shaper.

**Tech Stack:** Go, goquery v1.12.0, html-to-markdown v2.5.1 (both already in `go.mod`), poppler `pdftotext`.

**Spec:** [docs/superpowers/specs/2026-11-08-webfetch-outline-design.md](../specs/2026-11-08-webfetch-outline-design.md)

## Global Constraints

- Work on a branch `feat/webfetch-outline` from `develop`; commit per task, never push.
- Limit: `WebFetchLimit = 32000` (string length, as the shaper measures). Every result, notices included, is ≤ `WebFetchLimit`.
- Outline budget 4 000; lead at most 1 000; `find` at most 20 matches; page cache 64 MiB, TTL 15 min (= `webCacheTTL`).
- No model call inside `WebFetch`; no new dependency.
- Every error result starts with `error ` (so `cacheableWebResult` keeps it out of the cache).
- All new files in package `tools` (`core/tools`), tests beside them; fixtures under `core/tools/testdata/webfetch/`, synthetic (never copies of third-party pages).
- Tool description and agent instructions in English; keep them short (they are re-sent on every model call).
- Release notes: one bullet in `internal/features/FEATURES.md` under `## 2.1 (in development)`.

## Review Focus

1. **Extraction leaves almost nothing** (a site that puts its content in an `aside`, or wraps everything in `nav`): fall back to the unstripped body rather than return an empty page — test in Task 1.
2. **Heading titles with code, links or permalink marks** (`` ### `--gpu-memory-utilization`¶ ``): the outline shows clean text and the id the model copies works — test in Task 2.
3. **The model passes a title or an id with the wrong case or a leading `#`** (`section: "Layer Behavior with EP Enabled"`, `"#xact-read-committed"` for `XACT-READ-COMMITTED`): it is found — test in Task 2.
4. **Multi-byte text at a part or lead boundary**: no split UTF-8 sequence — test in Task 3.
5. **`find` with regex-looking text** (`a.b`, `--max-model-len`): matched literally — test in Task 5.

---

### Task 1: Content root extraction

**Files:**
- Create: `core/tools/webcontent.go`
- Test: `core/tools/webcontent_test.go`
- Create fixtures: `core/tools/testdata/webfetch/{mkdocs,docsy,sphinx,docbook,nav-only}.html`

**Interfaces:**
- Produces: `func contentRoot(doc *goquery.Document, raw bool) *goquery.Selection` — returns a detached clone safe to mutate.

Fixtures (each small, a few KB):
- `mkdocs.html`: `<header><nav>` site menu; `<main>` holding `div.md-sidebar > nav` (≥ 50 links), a second sidebar `nav` (a table of contents of the article), and one `<article>` with `h1`, `h2 id=a`, `h3 id=b` (with `<a class="headerlink" href="#b">¶</a>`), paragraphs, and `<aside class="note">KEEP-ASIDE</aside>` inside the article.
- `docsy.html`: `body > header > nav`, `div > aside.sidebar > nav`, `<main>` with no article: `div.td-content > div > h1, h2 id=x…`; `<footer>FOOTER-TEXT</footer>` outside main.
- `sphinx.html`: no `main`; `div[role=main] > section#top > h1 + section#sub1 > h2` (headings without id).
- `docbook.html`: no `main`, no article; `div.navheader` links, `div.sect1#TRANSACTION-ISO > h2 + div.sect2#XACT-READ-COMMITTED > h3`.
- `nav-only.html`: everything (≥ 1 000 characters of real text) inside `<nav>` elements.

- [ ] **Step 1: Write failing tests** `TestContentRoot` (table over fixtures). Assertions on `root.Text()`:
  - mkdocs: contains article text and `KEEP-ASIDE`; contains none of the sidebar link texts.
  - docsy: contains `td-content` text; no `FOOTER-TEXT`, no sidebar links.
  - sphinx: root is the `[role=main]` div (contains `section#top`).
  - docbook: contains `sect1` text (body fallback).
  - nav-only: text length ≥ 1 000 (fallback to the unstripped body).
  - `raw=true` on mkdocs: contains the sidebar link texts.
  - Mutating the returned selection does not change `doc` (call twice, same result).
- [ ] **Step 2: Run** `go test ./core/tools -run TestContentRoot` — expect FAIL (undefined).
- [ ] **Step 3: Implement `contentRoot`.** Rule:
  1. `raw` ⇒ clone of `body`, nothing removed.
  2. Root = first of `main`, `[role=main]`; else the single `article` if exactly one; else `body`. If the root is `main`/`[role=main]` and holds exactly one `article`, the article is the root.
  3. Clone it. Remove everywhere: `script, style, noscript, template, nav, [role=navigation], [role=search]`. If the root is not an `article`, also remove `header, footer, aside` that have no `article` or `section` ancestor inside the root.
  4. If the stripped root's trimmed text is < 200 characters while the body's is ≥ 1 000, return a clone of `body` (unstripped).
- [ ] **Step 4: Run** the test — expect PASS.
- [ ] **Step 5: Commit** `feat(webfetch): extract the content root of a page`.

### Task 2: Section tree from HTML

**Files:**
- Create: `core/tools/webdoc.go`
- Test: `core/tools/webdoc_test.go`
- Create fixture: `core/tools/testdata/webfetch/noids.html`

**Interfaces:**
- Consumes: `contentRoot` (Task 1) in tests.
- Produces:
```go
type webSection struct {
	ID       string        // copied from the page, or positional ("s2.3")
	Title    string        // clean heading text; "" for the document root
	Level    int           // 0 = document root, 1 = top-level section
	Own      string        // Markdown before the first sub-section, heading line excluded
	Children []*webSection
	Parent   *webSection
	size     int           // len(Markdown()), set by finalize
	count    int           // number of descendants, set by finalize
}
func (s *webSection) Markdown() string // heading line ("#"×min(Level+1,6) + " " + Title, none for Level 0) + Own + children, blank lines collapsed
func (s *webSection) Size() int
func (s *webSection) Descendants() int

type webDoc struct {
	Title     string
	Root      *webSection
	Pages     []string            // PDF only: text per page (Task 3)
	byID      map[string]*webSection
	byHeading map[int]*webSection // data-omnis-h index → section (HTML only)
}

type rawSection struct { ID, Title string; HNum int; HIndex int; Body string } // HNum = 1..6, HIndex = -1 when not from HTML
func assembleDoc(title, lead string, parts []rawSection) *webDoc // shared by Tasks 2 and 3
func markHeadings(doc *goquery.Document)                         // data-omnis-h="0..n" on every h1–h6, document order
func buildHTMLDoc(root *goquery.Selection, pageTitle string) (*webDoc, error)
func (d *webDoc) Lookup(ref string) (*webSection, bool)          // used by Task 6
```

Fixture `noids.html`: an `article` with `h2` (no id) → `h4` (level skip) → `h2`, headings nested in `div`s at different depths with paragraphs between them in document order, two headings with the same id, an `h3` whose text is `` <code>--gpu-memory-utilization</code><a class="headerlink">¶</a> ``.

- [ ] **Step 1: Write failing tests.**
  - `TestBuildHTMLDocIDs`: mkdocs fixture → section `b` exists, `Title == "…"` without `¶`; docbook → `Lookup("TRANSACTION-ISO")` holds `XACT-READ-COMMITTED` as child (wrapper ids); sphinx → sections `top`/`sub1` from `<section>` wrappers.
  - `TestBuildHTMLDocStructure` (noids): positional ids `s1`, `s1.1`, `s2`; the `h4` is a direct child of the first `h2` (relative levels); paragraphs between nested `div`s land in the right section's `Own` (document order); duplicate id → second becomes `<id>-2`; the code heading's title is `--gpu-memory-utilization`.
  - `TestBuildHTMLDocSingleH1Promoted`: a page whose only `h1` wraps everything → `d.Title` is the h1 text, `Root.Children` are its `h2`s, `Root.Own` holds the text before the first `h2`.
  - `TestSectionSizes`: `Root.Size() == len(Root.Markdown())`; a parent's size ≥ the sum of its children's; `Descendants()` counts all levels.
  - `TestLookup`: exact id; case-insensitive id; leading `#` stripped; unique case-insensitive title; ambiguous title → not found.
- [ ] **Step 2: Run** `go test ./core/tools -run 'TestBuildHTMLDoc|TestSectionSizes|TestLookup'` — expect FAIL.
- [ ] **Step 3: Implement.**
  - `buildHTMLDoc`: for each heading of `root` in document order, compute id (heading `id`; else the nearest ancestor `section`/`div` with an `id` whose first heading descendant is this heading; else ""), title (heading text with `a.headerlink, a.anchor, .anchor-link` removed, trailing `¶`/`#` trimmed, spaces collapsed), `HNum`, `HIndex` (its `data-omnis-h`, -1 if absent); replace the heading element with `<p>OMNISSEC<i>END</p>`; convert the root once with `htmltomarkdown.ConvertString`; split the Markdown at lines equal to `OMNISSEC<i>END`; text before the first sentinel is the lead.
  - `assembleDoc`: stack by `HNum` (pop while top's HNum ≥ current), relative `Level`, single-`h1` promotion, positional ids for empty ids (`s` + 1-based path joined by `.`), duplicate ids suffixed `-2`, `-3`, then `finalize` (sizes, counts, `byID`, `byHeading`).
- [ ] **Step 4: Run** the tests — expect PASS.
- [ ] **Step 5: Commit** `feat(webfetch): build a section tree from HTML`.

### Task 3: Markdown, text and PDF documents; parts

**Files:**
- Create: `core/tools/webdoc_text.go`, `core/tools/webparts.go`
- Test: `core/tools/webdoc_text_test.go`, `core/tools/webparts_test.go`

**Interfaces:**
- Consumes: `assembleDoc`, `webDoc`, `webSection` (Task 2).
- Produces:
```go
func buildMarkdownDoc(md, title string) *webDoc   // ATX headings, fenced code ignored, GitHub slugs
func buildTextDoc(text string) *webDoc            // no headings: Root.Own = text
func buildPDFDoc(pages []string) *webDoc          // Root.Own = pages joined, d.Pages = pages
func mdBlocks(md string) []string                 // blank-line separated blocks, a fenced code block is one block
func splitParts(md string, max int) []string      // each ≤ max; also used by Task 5
func pdfParts(pages []string, max int) []pdfPart
type pdfPart struct { First, Last int; Text string } // 1-based page numbers
```

- [ ] **Step 1: Write failing tests.**
  - `TestBuildMarkdownDoc`: `# T\nintro\n## A\n…\n```\n# not a heading\n```\n## A\n` → two top-level sections with ids `a` and `a-1`; the fenced `#` line stays in `A`'s `Own`.
  - `TestSplitParts`: 100 paragraphs of 1 000 chars with max 32 000 → every part ≤ max, joined parts == input blocks; a 50 000-char table (header + separator + rows) → every part starts with the header and separator lines; a 50 000-char fenced code block → every part is a complete fence; a single 40 000-char line → cut without splitting a UTF-8 sequence (use `"é"` repeated; every part is `utf8.ValidString`).
  - `TestPDFParts`: 10 pages of 8 000 chars, max 32 000 → parts cover pages 1–3, 4–6, 7–9, 10 (whole pages only); one page of 40 000 chars → split by lines, `First == Last`.
- [ ] **Step 2: Run** `go test ./core/tools -run 'TestBuildMarkdownDoc|TestSplitParts|TestPDFParts'` — expect FAIL.
- [ ] **Step 3: Implement.** `splitParts`: greedy packing of `mdBlocks`; an oversize block is split by rows (table, header 2 lines repeated), by lines re-wrapped in the fence (code), else by lines, else a rune-safe cut (`truncateAt`). Slugs: lower-case, keep letters/digits/`-`/`_`, spaces → `-`, de-duplicated `-1`, `-2`.
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `feat(webfetch): markdown, text and PDF documents, parts`.

### Task 4: Outline and section rendering

**Files:**
- Create: `core/tools/webrender.go`
- Test: `core/tools/webrender_test.go`

**Interfaces:**
- Consumes: Tasks 2–3.
- Produces:
```go
const outlineBudget = 4000
const leadMax = 1000
func renderSection(d *webDoc, s *webSection, part int, limit int) string
func renderOutline(d *webDoc, s *webSection, limit int) string
func fmtSize(n int) string // 850 → "850", 2140 → "2.1k", 96000 → "96k"
```

Exact copy:
- Page outline header: `[Page too long to return whole: <size> characters, limit <limit>. You have NOT read its content yet. Fetch the part you need with section="<id>" (a section returns everything under it when it fits), or search it with find="<text>". Do not answer from this outline alone.]`
- Section outline header: same with `Section "<title>" too long…`, then a line `Path: <A> > <B>`.
- Lead: `# <title>` then `Own` cut to `leadMax` on a line boundary (rune-safe) with `…` when cut.
- Entry: `<2 spaces × (depth-1)>- <Title> (<id>) — <fmtSize>`, plus `, <n> sub-sections` when its children are folded.
- Overflow: `… and <n> more top-level sections not shown: use find to locate one.`
- Part header: `[Part <i> of <n> of "<title>" — fetch part=<i+1> for the next.]`, last: `[Part <n> of <n> of "<title>" — last part.]`; PDF: `[Part <i> of <n> (pages <a>–<b> of <total>) — …]`. `<title>` is the doc title for the root.
- Part out of range: `error part <i> out of range: "<title>" has <n> parts`.

- [ ] **Step 1: Write failing tests.**
  - `TestRenderSectionFits`: selection ≤ limit → exactly `s.Markdown()`.
  - `TestRenderOutlineDepth`: a doc with 3 top-level × 16 sub × 15 sub-sub sections → result ≤ `outlineBudget` + header + lead, top level all present, folded entries carry `sub-sections`; a doc with 3 × 2 → all levels shown.
  - `TestRenderOutlineOverflow`: 2 000 top-level sections → result ≤ limit, ends with `more top-level sections not shown`.
  - `TestRenderSectionOutlineRecursive`: a 100k section with sub-sections → section outline with `Path:` line; its 20k child → content.
  - `TestRenderSectionParts`: a 70k leaf → part 1 header `Part 1 of 3`; `part=3` → `last part`; `part=4` → `error part 4 out of range`; `part=2` on a section that has sub-sections returns content, not an outline; every result ≤ limit.
  - `TestRenderLeadUTF8`: a lead of 2 000 `é` → valid UTF-8, ends with `…`.
- [ ] **Step 2: Run** `go test ./core/tools -run 'TestRender(Section|Outline|Lead)'` — expect FAIL.
- [ ] **Step 3: Implement.** Depth: try depths from the tree's height down to 1, keep the deepest whose entries fit `outlineBudget` (depth 1 always kept); then trim entries to the limit with the overflow line. Parts reserve 300 characters for the header (`splitParts(md, limit-300)`); PDF root uses `pdfParts`.
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `feat(webfetch): outline and section rendering`.

### Task 5: `find`

**Files:**
- Create: `core/tools/webfind.go`
- Test: `core/tools/webfind_test.go`

**Interfaces:**
- Consumes: `webDoc`, `webSection`, `mdBlocks` (Tasks 2–3), `renderOutline` (Task 4).
- Produces: `func renderFind(d *webDoc, s *webSection, term string, limit int) string`; `const findMaxMatches = 20`.

Result format (exact):
```
[find "<term>" in <scope>: <N> matches, showing <M>.]

--- <Breadcrumb A > B> (section="<id>")
<excerpt>
```
`<scope>` is `the page` for the root, else `section "<title>"`; PDF labels are `--- page <n>`; text before any heading is labelled `--- (top of page)`. No match: `[find "<term>" in <scope>: no match.]`, followed by a blank line and `renderOutline(d, s, limit)` when `s.Size() > limit`.

- [ ] **Step 1: Write failing tests** `TestRenderFind`:
  - Term in a paragraph of section `b` → one match labelled `(section="b")`, case-insensitive.
  - Term in one row of a table → excerpt = header + separator + that row only.
  - Term in a 200-line code block → excerpt ≤ 7 lines (±3) inside a fence.
  - `a.b` matches only the literal `a.b`, not `axb`.
  - 30 matching paragraphs → header says `30 matches, showing 20`, result ≤ limit.
  - `find` scoped to a section ignores matches outside it.
  - PDF doc → labels `--- page 3`.
  - No match on a section larger than the limit → `no match.` then its outline; on a small one → `no match.` only.
- [ ] **Step 2: Run** `go test ./core/tools -run TestRenderFind` — expect FAIL.
- [ ] **Step 3: Implement** (literal `strings.Contains` on lower-cased text; a paragraph excerpt is capped at 1 500 chars around the first hit; stop adding matches when the next would pass `limit`).
- [ ] **Step 4: Run** — expect PASS.
- [ ] **Step 5: Commit** `feat(webfetch): find text inside a page`.

### Task 6: Page cache and WebFetch wiring

**Files:**
- Modify: `core/tools/web.go` (whole fetch path; `pdfToText` becomes `pdfPages`)
- Modify: `core/tools/webcache.go` (add `pageCache`)
- Modify: `agent/coding_efficiency_plugin.go:91-103` (limit + hint)
- Modify: `agent/coding_efficiency_plugin_test.go:198-210`
- Test: `core/tools/web_test.go`, `core/tools/webcache_test.go`

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
```go
const WebFetchLimit = 32000
type WebFetchIn struct {
	URL      string `json:"url" jsonschema:"required"`
	Section  string `json:"section,omitempty" jsonschema:"an id from the page outline"`
	Find     string `json:"find,omitempty" jsonschema:"text to search for in the page or section"`
	Part     int    `json:"part,omitempty" jsonschema:"1-based part of a long section"`
	Selector string `json:"selector,omitempty" jsonschema:"CSS selector (advanced)"`
	Raw      bool   `json:"raw,omitempty" jsonschema:"keep the page navigation"`
}
type fetchedPage struct { doc *webDoc; html []byte; size int }
type pageCache struct { /* mu, ttl, maxBytes, used, m, now */ }
func newPageCache(ttl time.Duration, maxBytes int) *pageCache
func (c *pageCache) get(scope, key string, load func() (*fetchedPage, string)) (*fetchedPage, string) // non-empty string = error, not cached
func fetchPage(ctx context.Context, url string, raw bool) (*fetchedPage, string)
func pdfPages(ctx context.Context, data []byte) ([]string, string) // replaces pdfToText; same error strings
func webFetchWith(ctx context.Context, in WebFetchIn, pages *pageCache, scope string) string
func runWebFetch(ctx context.Context, in WebFetchIn) (string, error) // = webFetchWith(ctx, in, nil, ""), kept for the existing tests
```

Tool description (exact): `Fetch a web page as Markdown (PDF: its text). A page longer than the limit returns its outline instead of its content: then fetch one part with section (an id from the outline), search it with find, or read a long section part by part.`

Shaper: `webFetchMaxChars = fstools.WebFetchLimit`; hint: `fetch it again with section (an id from the page outline) or find to read a specific part`.

Rules in `webFetchWith`:
- The URL fragment is stripped (HTTP and both cache keys); `section` defaults to it. A fragment that is not a section falls back to the whole page with the line `[#<frag> is not a section of this page; showing the whole page.]` before the result. An explicit `section` not found → `error section "<ref>" not found on this page.` + blank line + root outline.
- `section` and `selector` both set → `error section and selector cannot be used together`.
- `selector` on a non-HTML page → `error selector needs an HTML page`; matching nothing → `error selector "<sel>" matched nothing on this page.` + root outline; matching a heading → that heading's section via `byHeading`; any other element → `buildHTMLDoc(match, title)` (no chrome removal) rendered from its root.
- `find` set → `renderFind`, else `renderSection(d, sel, in.Part, WebFetchLimit)`.
- `fetchPage`: HTTP as today (status, size cap, content-type); PDF → `pdfPages` → `buildPDFDoc`; HTML → `markHeadings`, `contentRoot(doc, raw)`, `buildHTMLDoc`; `text/markdown`, `text/x-markdown`, or a text URL whose path ends in `.md`/`.markdown` → `buildMarkdownDoc`; other text and JSON → `buildTextDoc`. `truncate` is no longer applied.
- Page cache key: `url + "\x00" + raw`; size = `len(html) + doc.Root.Size()`; `sharedPageCache = newPageCache(webCacheTTL, 64<<20)`; evict expired, then oldest; a page larger than the budget is not cached.
- Result cache key: `"fetch\x00" + url + "\x00" + selector + "\x00" + section + "\x00" + find + "\x00" + part + "\x00" + raw`.

- [ ] **Step 1: Write failing tests** in `web_test.go` (httptest, serving the Task 1 fixtures and generated pages):
  - `TestWebFetchSmallPageWhole`: mkdocs fixture → article text, no sidebar links, no outline header.
  - `TestWebFetchLongPageOutline`: a generated 120k-char MkDocs-shaped page → starts with `[Page too long`, contains no paragraph text beyond the lead, ≤ `WebFetchLimit`.
  - `TestWebFetchSectionAndFragment`: `section="b"` and `url+"#b"` give the same content; `"#B"`, `"Layer Behavior"`-style title lookup works; unknown explicit section → `error section`; unknown fragment → whole page with the `is not a section` line.
  - `TestWebFetchSelector`: selector on a heading → its section (the MkDocs `#b` dead end is gone); selector matching nothing → `error selector` + outline; selector + section → error.
  - `TestWebFetchMarkdownFile`: `.md` URL served as `text/plain` → outline from `#` headings when long.
  - `TestWebFetchPDFParts` (skip without `pdftotext`): a 3-page PDF with `part=2` → header mentions `pages`.
  - Existing tests still pass (HTTP error, plain text, binary, PDF text).
  - `TestPageCacheBudget` in `webcache_test.go`: two 40 MiB pages with a 64 MiB budget → only the newest kept; an error is not cached; scopes are separate.
  - `TestWebFetchNavigationReusesDownload`: a counting httptest handler; outline + two `section` requests through `webFetchWith` with a `pageCache` → one HTTP request.
  - Update `TestShapeResultWebFetchCapAndHint` to assert the hint contains `section`.
- [ ] **Step 2: Run** `go test ./core/tools ./agent -run 'TestWebFetch|TestPageCache|TestShapeResultWebFetch'` — expect FAIL.
- [ ] **Step 3: Implement** per the rules above; the tool handler keeps `context.Background()` for the fetch and wraps `webFetchWith(…, sharedPageCache, scope)` in `sharedWebCache.get` with the new key.
- [ ] **Step 4: Run** `go test ./core/tools ./agent` then `make vet` — expect PASS.
- [ ] **Step 5: Commit** `feat(webfetch): return an outline for long pages, section/find/part`.

### Task 7: Agent instructions and documentation

**Files:**
- Modify: `registry/agents/web_agent/instruction.md:16,20`
- Modify: `registry/agents/web_fetcher/instruction.md:19`
- Modify: `registry/agents/code_docs/instruction.md:37-38`
- Modify: `registry/agents/knowledge_leader/instruction.md:16`
- Modify: `CLAUDE.md` (the `WebFetch` paragraphs under "Web search provider precedence" and the shaper's `WebFetch` budget under "Coding squad")
- Modify: `internal/features/FEATURES.md` (2.1 in development)

Replacement text:
- web_agent L16: `- **To retrieve page content**: use 'WebFetch' on the URL. A long page returns its outline: fetch the section you need with \`section\`, or \`find\` a specific value or option.`
- web_agent L20: `- **On a long page, read the outline and fetch only the section you need** (\`section\`), or \`find\` the term you are looking for — never answer from an outline alone.`
- web_fetcher L19, the parenthesis: `(on a long page, \`find\` the claim's key term or fetch the relevant \`section\` from its outline)`.
- code_docs L37-38: `2. **Retrieve content** with \`WebFetch\` on real result URLs. A long page returns its outline: fetch the relevant \`section\`, or \`find\` the API name or option you need.`
- knowledge_leader L16: replace `pass a \`selector\` on long pages` with `on a long page fetch only the \`section\` you need from its outline, or \`find\` a specific value`.
- FEATURES.md bullet: `- **Long web pages read section by section** — research agents get a long page's outline and fetch only the section they need, or search the page for a term, instead of a page cut in the middle.`

- [ ] **Step 1: Apply the replacements** and update CLAUDE.md (describe the outline/section/find/part behaviour, the shared `WebFetchLimit`, navigation removal, the page cache; drop the "fetch it again with a selector" advice).
- [ ] **Step 2: Measure the fixed prompt**: `../omnis-benches/prompt-size/run.sh . /tmp/ps-after` on this branch and on `develop` (`git worktree add`), then `python3 ../omnis-benches/prompt-size/report.py <dir>/requests.jsonl`. Expected: `web_agent`, `web_fetcher`, `code_docs`, `knowledge_leader` grow by less than 150 tokens each.
- [ ] **Step 3: Run** `make test` — expect PASS (instruction tests included).
- [ ] **Step 4: Commit** `docs(webfetch): agents read outlines; CLAUDE.md, FEATURES.md`.

### Task 8: Bench before/after (live models, gateway credentials needed)

**Files:**
- Modify (omnis-benches): `squad-bench/setup-modelcost.sh` (variants `W2="premium balanced high develop"`, `W3="premium balanced high feat/webfetch-outline"`)
- Modify (omnis-benches): `squad-bench/tasks-modelcost.json` (task `mc-moe`, squad `knowledge`, prompt `Comment le MOE Routing fonctionne avec plusieurs GPUs ?`)

- [ ] **Step 1: Role probes** on each ref: `OMNIS_SRC=<develop worktree|branch worktree> squad-bench/setup-role.sh <work> web_agent <port> balanced`, same for `web_fetcher`; run the written task files with `bench.py --tasks <work>/tasks-balanced.json --repeat 3 --out <ref>-<role>.jsonl`.
- [ ] **Step 2: Knowledge squad**: `setup-modelcost.sh <work> start W2,W3`, then `bench.py --task mc-netbird --task mc-kserve --task mc-moe --repeat 2` against each, `--out`.
- [ ] **Step 3: Compare** correctness (judge), `WebFetch` calls, `web_agent`/`web_fetcher` prompt tokens, per-call caps hit, and in transcripts where an outline was returned whether a `section`/`find` call followed before the answer. Write `omnis-benches/reports/webfetch-outline-2026-11.md`.
- [ ] **Step 4: Decide**: merge only if correctness is not lower and total tokens are not higher; otherwise report and stop. Drop `raw` if no run used it and no fixture needed it (spec open point).
- [ ] **Step 5: Commit** the bench changes and report in omnis-benches.
