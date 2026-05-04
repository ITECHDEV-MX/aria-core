# Spec — /Historias artifact protocol

## Slug contract

Slug MUST match `^[a-z0-9]+(-[a-z0-9]+)*$`. No spaces, no uppercase,
no double hyphens.

## Filename contract

Artifact filenames MUST match `^\d+-[a-z0-9-]+\.md$`. The numeric
prefix MUST equal the `position` argument supplied to save.

## Manifest contract

`MANIFEST.yaml` MUST exist after the first successful save and MUST
contain:

- `slug`, `created_at`, `created_by`, `status`, `chain[]`.
- Each entry: `position`, `artifact`, `skill`, `agent_model`,
  `content_sha256`, `inputs[]`, `duration_ms`, `created_at`.

`chain[]` MUST be sorted by `position` ascending.

## Tool exit/error contracts

- Invalid slug → `ErrSlugInvalid`.
- Filename / position mismatch → typed error.
- Position taken without overwrite → `ErrPositionTaken`.
- Missing manifest on read → `ErrManifestMissing`.
- Missing entry → `ErrArtifactMissing`.

## Out of scope

- Concurrency control (locks).
- Cloud mirror to aria_pages (F2.1).
- CLI commands (F2.1+).
