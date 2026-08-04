---
name: pdf
description: Extract text, tables and metadata from PDF files without guessing at layout.
license: MIT
---

# Extracting from PDFs

Pull **text**, *tables* and metadata out of a PDF, and say so plainly when the
document does not carry what was asked for.

## When to reach for it

- A document arrives as a PDF and the task needs its prose.
- A table has to survive the trip into Markdown.
- A citation must keep the page it came from.

## Steps

1. Establish whether the PDF carries a text layer at all.
2. If it does not, report that. Do not invent one, and do not silently return
   an empty string.
3. Extract, then check the extraction against the page count before using it.

| Input | Tool | Output |
| --- | --- | --- |
| Text-layer PDF | `pdftotext -layout` | Markdown |
| Scanned PDF | none — report it | an explanation |
| Encrypted PDF | none | an error naming the file |

```bash
epos install <registry>/demo/agent-skills/pdf:1.2.0
pdftotext -layout report.pdf -
```

> A table that has lost its columns is worse than no table at all: the reader
> cannot tell that anything went missing.

See the [house style](references/style.md) for how citations are formatted, and
the [OCI distribution spec](https://github.com/opencontainers/distribution-spec)
for how this skill is packaged.
