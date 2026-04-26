-- ARIA Vault — bóveda de secretos con encryption-at-rest.
-- Per-secret nonce + per-cliente HKDF-derived key.
-- ACL via aria_secret_grants. Audit completo en aria_secret_access_log.

CREATE TABLE IF NOT EXISTS aria_secrets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  category TEXT NOT NULL CHECK (category IN ('db_password','api_token','ssh_key','cert','env','webhook','generic')),
  scope TEXT NOT NULL CHECK (scope IN ('personal','project','team','client_knowledge')),
  project TEXT,
  client_id UUID,
  description TEXT,
  ciphertext BYTEA NOT NULL,
  nonce BYTEA NOT NULL,
  key_id TEXT NOT NULL,
  metadata JSONB DEFAULT '{}'::jsonb,
  expires_at TIMESTAMPTZ,
  rotation_policy TEXT DEFAULT 'manual',
  superseded_by UUID REFERENCES aria_secrets(id),
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_by_uid UUID NOT NULL
);
-- Unicidad lógica de un secret activo: (name, project, client_id). Active rotations
-- supersede el viejo (is_active=FALSE) antes de meter el nuevo, así no rompe.
CREATE UNIQUE INDEX IF NOT EXISTS aria_secrets_active_uidx
  ON aria_secrets (name, COALESCE(project,''), COALESCE(client_id::text,''))
  WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_aria_secrets_project ON aria_secrets(project) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_aria_secrets_client ON aria_secrets(client_id) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_aria_secrets_creator ON aria_secrets(created_by_uid) WHERE is_active;

CREATE TABLE IF NOT EXISTS aria_secret_grants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  secret_id UUID NOT NULL REFERENCES aria_secrets(id) ON DELETE CASCADE,
  granted_to_uid UUID,
  granted_to_role TEXT,
  permission TEXT NOT NULL CHECK (permission IN ('read','rotate','delete')),
  granted_by_uid UUID NOT NULL,
  expires_at TIMESTAMPTZ,
  granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (granted_to_uid IS NOT NULL OR granted_to_role IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_aria_grants_secret ON aria_secret_grants(secret_id);
CREATE INDEX IF NOT EXISTS idx_aria_grants_uid ON aria_secret_grants(granted_to_uid);
CREATE INDEX IF NOT EXISTS idx_aria_grants_role ON aria_secret_grants(granted_to_role);

CREATE TABLE IF NOT EXISTS aria_secret_access_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  secret_id UUID NOT NULL,
  accessed_by_uid UUID NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('read','rotate','delete','create','grant','revoke','use_in_cmd','denied')),
  client_ip INET,
  user_agent TEXT,
  reason TEXT,
  command_hash TEXT,
  accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_access_log_secret ON aria_secret_access_log(secret_id, accessed_at DESC);
CREATE INDEX IF NOT EXISTS idx_access_log_user ON aria_secret_access_log(accessed_by_uid, accessed_at DESC);
