# External PDF regression data

This directory has two intentionally different layers:

- The flat source directories are a small, committed CI suite. Their exact
  bytes are recorded in [`SHA256SUMS`](SHA256SUMS).
- `corpora/`, `sample-files/`, and `test_documents/` are complete pinned Git
  checkouts. They are ignored because the combined working trees are well over
  a gigabyte, but can be reproduced without choosing files by hand.

Run `task corpus-fixtures-verify` to check the committed files, `task
corpus-layout-fetch` for the project fixture repositories, `task
corpus-benchmark-fetch` for the two public benchmarks, or `task corpus-fetch`
for everything (Git LFS is required).

## Committed CI cross-section

| Directory | Committed cases | What they exercise |
| --- | ---: | --- |
| `camelot/` | 3 PDFs | Nested tables, row spans, column spans, and partial rules |
| `markitdown/` | 6 PDF/Markdown pairs | Borderless and sparse tables, multipage invoices, ordinary prose, and an image-only scan |
| `pdfplumber/` | 3 PDFs | Non-zero page boxes, three-table ordering, and curved/path-based rules |
| `tabula/` | 3 PDF/CSV pairs | Multiple tables, spanning section rows, sparse financial records, and multilingual text |
| `pymupdf4llm/` | 5 PDF/Markdown pairs | Two-table layout, OCR routing, scientific multicolumn layout, and a hostile-link fixture |
| `synthetic-table-bench/` | 5 PDF/Markdown pairs | No borders, 50% and 90% sparse cells, mixed row/column merges, and three-level headers |
| `olmocr-bench/` | 1 PDF + 7 verified JSONL checks | Real-world table cell values and adjacency relationships used by Marker’s public benchmark |

Expected Markdown and CSV files are upstream oracles. They are not necessarily
byte-for-byte goldens for this package: different projects make different
choices about heading syntax, inline styling, and whether spanning tables use
HTML. The Go regressions assert the structural facts that should agree.

## Complete pinned sources

| Source | Revision | Local contents at this pin | Upstream license | Task |
| --- | --- | --- | --- | --- |
| [Camelot](https://github.com/camelot-dev/camelot/tree/7779eecfdab7a85ba12ec3d1285b305c7e327e3a/tests/files) | `7779eecfdab7a85ba12ec3d1285b305c7e327e3a` | Complete repository; 171 PDFs | MIT | `task corpus-camelot` |
| [MarkItDown](https://github.com/microsoft/markitdown/tree/9dc0d6579b8739c9d0671ff205e071e3053c7df1/packages/markitdown/tests/test_files) | `9dc0d6579b8739c9d0671ff205e071e3053c7df1` | Complete repository, fixtures, and expected outputs | MIT | `task corpus-markitdown` |
| [Tabula Java](https://github.com/tabulapdf/tabula-java/tree/2cdf3b4fd3f7e921dca8cc6814cdd9316be40f0f/src/test/resources/technology/tabula) | `2cdf3b4fd3f7e921dca8cc6814cdd9316be40f0f` | Complete repository; PDF, CSV, JSON, and TSV oracles | MIT | `task corpus-tabula` |
| [PyMuPDF4LLM](https://github.com/pymupdf/pymupdf4llm/tree/6e511542f672438215cbde2363199b864e772987/tests) | `6e511542f672438215cbde2363199b864e772987` | Complete repository and full `tests/` tree (10 PDFs, including every deterministic PDF/Markdown pair) | AGPL-3.0 | `task corpus-pymupdf4llm` |
| [pdfplumber](https://github.com/jsvine/pdfplumber/tree/4c64b92d5caccd71c645e98e0fabb0c4dba7ff45/tests) | `4c64b92d5caccd71c645e98e0fabb0c4dba7ff45` | Complete repository; 85 PDFs and assertion-based table oracles | MIT | `task corpus-pdfplumber` |
| [Marker](https://github.com/datalab-to/marker/tree/e1a6226adfaab4cd573cfa96e12d60905ee38036) | `e1a6226adfaab4cd573cfa96e12d60905ee38036` | Complete repository, tests, and benchmark adapters | Apache-2.0 | `task corpus-marker` |
| [olmOCR-Bench](https://huggingface.co/datasets/allenai/olmOCR-bench/tree/54a96a6fb6a2bd3b297e59869491db4d3625b711) | `54a96a6fb6a2bd3b297e59869491db4d3625b711` | 1,403 PDFs and 7,018 JSONL checks at this revision | ODC-BY-1.0 | `task corpus-olmocr` |
| [Synthetic Table Bench](https://huggingface.co/datasets/roma2025/synthetic-table-bench/tree/59455cf9f431df7e2812738ce2c359ecd90b8bca) | `59455cf9f431df7e2812738ce2c359ecd90b8bca` | 246 deterministic PDF/Markdown pairs at this revision | CC-BY-4.0 | `task corpus-synthetic-tables` |

Marker’s `tests/conftest.py` downloads `datalab-to/pdf_benchmark`, which is not
publicly accessible without credentials. The repository itself is included,
and its public benchmark path is covered by the complete olmOCR-Bench checkout.

The source repositories’ licenses describe their distributions. Individual
fixture documents can also retain third-party copyright or provenance; keep
the pinned upstream source and its attribution when redistributing them.
