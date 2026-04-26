-- ARIA Core mini-Notion: páginas largas con jerarquía + markdown + FTS.
-- Reemplaza Notion+Confluence+Drive como single source of truth del equipo.
--
-- Diseño:
--   * aria_pages: tree padre-hijo (parent_id ON DELETE CASCADE) con sort_order
--     manual para reordenamiento drag-and-drop.
--   * aria_page_revisions: cada Update inserta una snapshot — historial completo.
--   * search_vector: FTS spanish, A=title, B=content_md.
--     NOTA: NO uso unaccent() en la GENERATED column (postgres exige IMMUTABLE
--     y unaccent es STABLE — el truco que usa ariamem es aplicar unaccent
--     on-the-fly en el query). Aún así expongo search_vector para indexar.
--   * page_type='database' es un placeholder reservado para el agente ATTACH/DB
--     (otro wave) — mi handler simplemente pinta un mount-point.

CREATE TABLE IF NOT EXISTS aria_pages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id UUID REFERENCES aria_pages(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  content_md TEXT NOT NULL DEFAULT '',
  icon TEXT,
  project TEXT,
  scope TEXT NOT NULL DEFAULT 'project' CHECK (scope IN ('personal','project','team','client_knowledge')),
  client_id UUID,
  page_type TEXT NOT NULL DEFAULT 'doc',
  template_key TEXT,
  sensitivity TEXT NOT NULL DEFAULT 'internal' CHECK (sensitivity IN ('public','internal','client','confidential')),
  sort_order INT NOT NULL DEFAULT 0,
  is_archived BOOLEAN NOT NULL DEFAULT FALSE,
  created_by_uid UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_by_uid UUID,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('spanish', coalesce(title,'')), 'A') ||
    setweight(to_tsvector('spanish', coalesce(content_md,'')), 'B')
  ) STORED
);
CREATE INDEX IF NOT EXISTS idx_pages_parent ON aria_pages(parent_id);
CREATE INDEX IF NOT EXISTS idx_pages_project ON aria_pages(project) WHERE NOT is_archived;
CREATE INDEX IF NOT EXISTS idx_pages_scope ON aria_pages(scope);
CREATE INDEX IF NOT EXISTS idx_pages_search ON aria_pages USING GIN(search_vector);
CREATE INDEX IF NOT EXISTS idx_pages_updated ON aria_pages(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_pages_template ON aria_pages(template_key) WHERE template_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS aria_page_revisions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  page_id UUID NOT NULL REFERENCES aria_pages(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  content_md TEXT NOT NULL,
  edited_by_uid UUID NOT NULL,
  edit_summary TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_page_revisions_page ON aria_page_revisions(page_id, created_at DESC);
