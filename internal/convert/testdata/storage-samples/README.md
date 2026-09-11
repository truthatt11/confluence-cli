# Storage format fixtures

Copied from [pchuri/confluence-cli](https://github.com/pchuri/confluence-cli) v2.23.0
(`tests/fixtures/storage-samples/`), MIT License, Copyright (c) 2025 Confluence CLI Contributors.
See the repository `NOTICE` file for the full license text.

Each `NN-name.xml` is Confluence storage format; `NN-name.expected.md` is the Markdown
that `convert.StorageToMarkdown` must produce with default options. Regenerate after an
intentional change with `go test ./internal/convert -update`, then review the diff.

Expected outputs deliberately changed from the original:

- `06`: include links point at `/display/SPACE/Title`, which works on Data Center,
  instead of the Cloud-only `[PAGE_ID_HERE]` placeholder.
- `08`: nested lists keep their structure instead of being flattened onto one line.
- `11`: unknown macros keep their body and are marked with `<!-- macro: NAME -->`
  instead of being dropped silently.
