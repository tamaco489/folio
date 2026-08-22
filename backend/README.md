# backend

日本語: [README.ja.md](README.ja.md)

Go code for the pipeline that turns paper PDFs into structured JSON. See [docs/README.md](../docs/README.md) for the big picture.

## Layout

| Directory       | Contents                                                           |
| --------------- | ------------------------------------------------------------------ |
| `cmd/pipeline/` | The 5 Lambda functions (what gets deployed)                        |
| `internal/`     | Logic shared by the Lambdas and the tools                          |
| `tools/`        | Local tools for evaluation (not deployed)                          |
| `layers/`       | Lambda Layer (poppler)                                             |
| `testdata/`     | Recorded AWS responses and evaluation PDFs (`pdf/` is git-ignored) |

## Lambdas

| Lambda            | Role                                                                                                      |
| ----------------- | --------------------------------------------------------------------------------------------------------- |
| `validator`       | Entry. Checks the file is a readable PDF and the jobId (= SHA-256) has not been processed                 |
| `preprocessor`    | Writes page images and the text layer to `work/`                                                          |
| `textract-parser` | Route A. Reads with Textract, then lets Bedrock organise metadata, sections and references (English only) |
| `bedrock-parser`  | Route B. Shows one page image to Bedrock and structures it (pages run in parallel)                        |
| `finalizer`       | Exit. Normalises both routes, verifies references with Crossref, writes `outputs/` and DynamoDB           |

## tools

Three tools for measuring extraction accuracy as numbers. Papers were chosen as the subject because ground truth can be generated from their LaTeX sources.

```text
fetch-corpus ──> PDF ──> (pipeline) ──> extraction ──┐
                  │                                   ├──> evaluate ──> match rates
                  └──> LaTeX source ──> build-truth ──> truth ──┘
```

| Tool           | Status                   | What it does                                                                                                                                                                    |
| -------------- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fetch-corpus` | Implemented              | Fetches papers matching the criteria (cs.CL / cs.LG, 8-20 pages) from arXiv and records pages, reference count, LaTeX source availability, license and SHA-256 in `corpus.json` |
| `build-truth`  | Phase 2, not implemented | Extracts title, authors, section headings and references from the LaTeX source into a truth JSON                                                                                |
| `evaluate`     | Phase 2, not implemented | Compares extraction output with the truth JSON and reports match rates per field; also used to compare routes A and B                                                           |

## Commands

```sh
cd backend
just build           # Build the 5 Lambdas
just upload          # Put the zips in S3 and swap the Lambda code
just submit <pdf>    # Submit a PDF to start the pipeline (Textract / Bedrock charges apply)
just fetch-corpus    # Fetch evaluation papers from arXiv
```

See `just --list` for the other recipes.
