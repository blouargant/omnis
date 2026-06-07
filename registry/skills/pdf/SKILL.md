---
name: pdf
description: Fallback PDF text extractor using pdftotext (poppler). Use ONLY when the preferred `liteparse` skill is unavailable or for a quick plain-text dump of a simple text PDF — for the best PDF parsing (scanned, multi-column, layout-heavy, or non-PDF formats) prefer the `liteparse` skill.
metadata:
  author: blouargant@chapsvision.com
  tags: "file, pdf, text extraction, fallback"
---

# PDF

> **This is the lightweight fallback parser.** Prefer the **`liteparse`** skill
> (`lit`) for PDF parsing — it handles scanned and complex PDFs far better and
> also covers DOCX/PPTX/XLSX/images.
>
> ⚠️ **Do not pre-empt LiteParse.** If `lit` is merely **not installed yet**, do
> **not** use this skill — go back to the `liteparse` skill and **offer to
> install LiteParse first**. Only use `pdftotext` here once the user has
> **declined** the LiteParse install, the install **failed**, or the user has
> **explicitly asked** for a quick pdftotext dump.

1. Check whether `pdftotext` is available on the host.

If `pdftotext` is available:

2. Use `Bash` to run `pdftotext -layout <input> -`.
3. Read the captured stdout.
4. Summarise each section using document headings when present; if no
  headings are detectable, summarise in sequential equal-length chunks.

If `pdftotext` is missing:

2. Suggest `brew install poppler` (macOS) or `apt-get install poppler-utils`
  (Debian/Ubuntu).
3. Stop.
