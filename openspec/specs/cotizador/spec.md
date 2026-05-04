# ARIA Core — Cotizador (RFP→Quote) Subsystem Spec

**Path**: `internal/cotizador/`

## Purpose

Turn an RFP (PDF, DOCX, plain text) into a structured quote: scope
breakdown, deliverables, pricing matrix, exportable DOCX/PDF.

## Surface

- `aria_quote_export_docx(quote_id)` — render to DOCX via Gotenberg
- (more tools as the pipeline matures)

## Invariants

1. **Redactor runs on RFP upload** before any LLM extraction.
2. **Pricing rules live in the project's KB**, not hardcoded in the
   pipeline.
3. **Quote versions are immutable**: edits create a new version.
4. **Export uses Gotenberg** (`docker run gotenberg/gotenberg:8`)
   bound to `127.0.0.1:3001` on the VPS.
