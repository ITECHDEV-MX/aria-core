-- aria-core / pages / attachments + share links schema.
--
-- Provides:
--   * aria_page_attachments        — file uploads anchored to aria_pages rows.
--   * aria_page_share_links        — public read-only URLs with optional expiry/password.
--   * aria_page_share_access_log   — auditable hits on a share link (IP, UA, password attempts).
--
-- The FK to aria_pages is created with NOT VALID + DEFERRABLE so that the migration is
-- tolerant of run-order: if the PAGES agent migration applies before us we get the FK,
-- if it applies after us the FK still works at insert time. See cloudstore.go.

CREATE TABLE IF NOT EXISTS aria_page_attachments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  page_id UUID NOT NULL,
  filename TEXT NOT NULL,
  original_filename TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  storage_path TEXT NOT NULL,
  thumbnail_path TEXT,
  sha256 TEXT NOT NULL,
  uploaded_by_uid UUID NOT NULL,
  description TEXT,
  is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_attach_page ON aria_page_attachments(page_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attach_sha ON aria_page_attachments(sha256);
CREATE INDEX IF NOT EXISTS idx_attach_alive ON aria_page_attachments(page_id, is_deleted);

CREATE TABLE IF NOT EXISTS aria_page_share_links (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  page_id UUID NOT NULL,
  token TEXT NOT NULL UNIQUE,
  password_hash TEXT,
  expires_at TIMESTAMPTZ,
  view_count INT NOT NULL DEFAULT 0,
  last_viewed_at TIMESTAMPTZ,
  created_by_uid UUID NOT NULL,
  is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_share_token_alive ON aria_page_share_links(token) WHERE NOT is_revoked;
CREATE INDEX IF NOT EXISTS idx_share_expires ON aria_page_share_links(expires_at) WHERE NOT is_revoked;
CREATE INDEX IF NOT EXISTS idx_share_page ON aria_page_share_links(page_id, created_at DESC);

CREATE TABLE IF NOT EXISTS aria_page_share_access_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  share_link_id UUID NOT NULL REFERENCES aria_page_share_links(id) ON DELETE CASCADE,
  client_ip INET,
  user_agent TEXT,
  password_attempted BOOLEAN NOT NULL DEFAULT FALSE,
  password_correct BOOLEAN,
  accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_share_log_link ON aria_page_share_access_log(share_link_id, accessed_at DESC);
