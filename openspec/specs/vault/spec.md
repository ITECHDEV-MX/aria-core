# ARIA Core — Vault Subsystem Spec

**Path**: `internal/vault/`

## Purpose

AES-256-GCM secrets store with HKDF per-client key derivation. The
canonical pattern for letting Claude (or any agent) USE secrets without
ever seeing them.

## Surface

- `aria_vault_save(key, plaintext)` — encrypt and store
- `aria_vault_use_in_cmd(key, command_template)` — execute a shell
  command with the secret injected as an env var; the agent never sees
  the plaintext

## Invariants

1. **No plaintext returned to callers** outside `vault_use_in_cmd`.
2. **Master key never logged**. Stored in
   `/etc/systemd/system/aria-core.service.d/override.conf` (chmod 600
   root-only on the server).
3. **Audit log per access**: who, when, key (not value), action.
4. **HKDF derivation per client/tenant** so cross-tenant key reuse is
   impossible.

## Out of scope

- Secret rotation automation (manual today)
- HSM integration
