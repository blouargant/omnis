---
name: pdf
description: Extract or summarise text from a local PDF file.
metadata:
  author: blouargant@chapsvision.com
  tags: "file, pdf, text extraction"
---

# PDF

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
