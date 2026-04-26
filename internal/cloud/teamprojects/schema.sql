-- ARIA Core teamprojects schema (wave 7)
-- Plataforma de gestión de proyectos del equipo iTechDev: proyectos internos
-- (con o sin cliente), repos GitHub auto-creados, miembros con roles, tasks
-- con Kanban, comentarios, knowledge capture al cerrar (obs+sesiones+commits).

CREATE TABLE IF NOT EXISTS aria_team_projects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT,
  client_id UUID,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','archived')),
  github_repo_url TEXT,
  github_repo_owner TEXT,
  github_repo_name TEXT,
  github_repo_private BOOLEAN NOT NULL DEFAULT TRUE,
  github_default_branch TEXT NOT NULL DEFAULT 'main',
  created_by_uid UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_team_projects_status ON aria_team_projects(status);
CREATE INDEX IF NOT EXISTS idx_team_projects_slug ON aria_team_projects(slug);

CREATE TABLE IF NOT EXISTS aria_team_project_members (
  project_id UUID NOT NULL REFERENCES aria_team_projects(id) ON DELETE CASCADE,
  user_uid UUID NOT NULL REFERENCES cloud_users(uid) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','lead','member','viewer')),
  added_by_uid UUID,
  added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (project_id, user_uid)
);

CREATE INDEX IF NOT EXISTS idx_project_members_user ON aria_team_project_members(user_uid);

CREATE TABLE IF NOT EXISTS aria_tasks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES aria_team_projects(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  description_md TEXT,
  status TEXT NOT NULL DEFAULT 'todo' CHECK (status IN ('todo','in_progress','review','done','cancelled')),
  priority TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('low','medium','high','urgent')),
  due_date DATE,
  estimate_hours DECIMAL(10,2),
  spent_hours DECIMAL(10,2) NOT NULL DEFAULT 0,
  github_issue_number INT,
  parent_task_id UUID REFERENCES aria_tasks(id) ON DELETE SET NULL,
  position INT NOT NULL DEFAULT 0,
  labels TEXT[] NOT NULL DEFAULT '{}',
  created_by_uid UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  closed_at TIMESTAMPTZ,
  closed_by_uid UUID
);

CREATE INDEX IF NOT EXISTS idx_tasks_project_status ON aria_tasks(project_id, status, position);
CREATE INDEX IF NOT EXISTS idx_tasks_due_date ON aria_tasks(due_date) WHERE status != 'done' AND status != 'cancelled';

CREATE TABLE IF NOT EXISTS aria_task_assignments (
  task_id UUID NOT NULL REFERENCES aria_tasks(id) ON DELETE CASCADE,
  user_uid UUID NOT NULL REFERENCES cloud_users(uid) ON DELETE CASCADE,
  assigned_by_uid UUID,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (task_id, user_uid)
);

CREATE INDEX IF NOT EXISTS idx_task_assignments_user ON aria_task_assignments(user_uid);

CREATE TABLE IF NOT EXISTS aria_task_observations (
  task_id UUID NOT NULL REFERENCES aria_tasks(id) ON DELETE CASCADE,
  observation_id TEXT NOT NULL,
  link_type TEXT NOT NULL DEFAULT 'work' CHECK (link_type IN ('work','prompt','outcome','reference')),
  linked_by_uid UUID,
  linked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (task_id, observation_id, link_type)
);

CREATE TABLE IF NOT EXISTS aria_task_sessions (
  task_id UUID NOT NULL REFERENCES aria_tasks(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  linked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (task_id, session_id)
);

CREATE TABLE IF NOT EXISTS aria_task_comments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES aria_tasks(id) ON DELETE CASCADE,
  author_uid UUID NOT NULL,
  content_md TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_comments_task ON aria_task_comments(task_id, created_at DESC);
