// Tests covering DB-bound PgStore methods using sqlmock.
// Pure-function helpers live alongside in pure_funcs_test.go; this file focuses on
// SQL-shaped methods (CreateProject, GetProject, ListProjects, AddMember, IsMember).

package teamprojects

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

func newMockStore(t *testing.T) (*PgStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPgStore(db), mock
}

// projectColumns mirrors the RETURNING / SELECT clause of CreateProject + GetProject.
func projectColumns() []string {
	return []string{
		"id", "slug", "name", "description", "client_id", "status",
		"github_repo_url", "github_repo_owner", "github_repo_name",
		"github_repo_private", "github_default_branch",
		"created_by_uid", "created_at", "updated_at", "archived_at",
	}
}

func TestCreateProject_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	creator := uuid.NewString()

	now := time.Now().UTC().Truncate(time.Microsecond)
	rows := sqlmock.NewRows(projectColumns()).
		AddRow("11111111-1111-1111-1111-111111111111", "my-project", "My Project", "",
			"", "active", "", "", "", false, "main", creator, now, now, nil)

	// CreateProject uses sqlmock.QueryMatcherEqual so we must pass the exact SQL.
	mock.ExpectQuery(`
		INSERT INTO aria_team_projects (
			id, slug, name, description, client_id, status,
			github_repo_url, github_repo_owner, github_repo_name,
			github_repo_private, github_default_branch, created_by_uid
		) VALUES (
			$1::uuid, $2, $3, NULLIF($4,''), $5, 'active',
			NULLIF($6,''), NULLIF($7,''), NULLIF($8,''),
			$9, $10, $11::uuid
		)
		RETURNING id::text, slug, name, COALESCE(description,''),
		          COALESCE(client_id::text,''), status,
		          COALESCE(github_repo_url,''), COALESCE(github_repo_owner,''), COALESCE(github_repo_name,''),
		          github_repo_private, github_default_branch,
		          created_by_uid::text, created_at, updated_at, archived_at`).
		WillReturnRows(rows)

	// AddMember (auto-add creator as owner) uses ExecContext.
	mock.ExpectExec(`
		INSERT INTO aria_team_project_members (project_id, user_uid, role, added_by_uid)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid)
		ON CONFLICT (project_id, user_uid) DO UPDATE SET role = EXCLUDED.role`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	pr, err := s.CreateProject(ctx, CreateProjectParams{
		Name:         "My Project",
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if pr.Slug != "my-project" || pr.Status != "active" || pr.GitHubDefaultBranch != "main" {
		t.Errorf("unexpected project: %+v", pr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestCreateProject_ValidationErrors(t *testing.T) {
	s, _ := newMockStore(t)
	ctx := context.Background()

	if _, err := s.CreateProject(ctx, CreateProjectParams{Name: "", CreatedByUID: uuid.NewString()}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("empty name: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.CreateProject(ctx, CreateProjectParams{Name: "ok"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("missing creator: want ErrInvalidInput, got %v", err)
	}
}

func TestGetProject_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	id := uuid.NewString()

	mock.ExpectQuery(`SELECT
		id::text, slug, name, COALESCE(description,''),
		COALESCE(client_id::text,''), status,
		COALESCE(github_repo_url,''), COALESCE(github_repo_owner,''), COALESCE(github_repo_name,''),
		github_repo_private, github_default_branch,
		created_by_uid::text, created_at, updated_at, archived_at FROM aria_team_projects WHERE id = $1::uuid`).
		WithArgs(id).
		WillReturnError(errNoRowsForSqlmock())

	if _, err := s.GetProject(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestGetProject_InvalidID(t *testing.T) {
	s, _ := newMockStore(t)
	if _, err := s.GetProject(context.Background(), "not-a-uuid"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput, got %v", err)
	}
}

func TestAddMember_RoleValidation(t *testing.T) {
	s, _ := newMockStore(t)
	ctx := context.Background()
	pid := uuid.NewString()
	uid := uuid.NewString()

	if err := s.AddMember(ctx, "bad-id", uid, "member", uid); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("invalid project id: want ErrInvalidInput, got %v", err)
	}
	if err := s.AddMember(ctx, pid, uid, "captain", uid); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("invalid role: want ErrInvalidInput, got %v", err)
	}
}

func TestAddMember_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	pid := uuid.NewString()
	uid := uuid.NewString()

	mock.ExpectExec(`
		INSERT INTO aria_team_project_members (project_id, user_uid, role, added_by_uid)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid)
		ON CONFLICT (project_id, user_uid) DO UPDATE SET role = EXCLUDED.role`).
		WithArgs(pid, uid, "lead", uid).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.AddMember(ctx, pid, uid, "lead", uid); err != nil {
		t.Errorf("AddMember: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestIsMember_True(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	pid := uuid.NewString()
	uid := uuid.NewString()

	mock.ExpectQuery(`SELECT 1 FROM aria_team_project_members WHERE project_id=$1::uuid AND user_uid=$2::uuid`).
		WithArgs(pid, uid).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))

	got, err := s.IsMember(ctx, pid, uid)
	if err != nil || !got {
		t.Errorf("IsMember=true expected, got=%v err=%v", got, err)
	}
}

func TestIsMember_False(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	pid := uuid.NewString()
	uid := uuid.NewString()

	mock.ExpectQuery(`SELECT 1 FROM aria_team_project_members WHERE project_id=$1::uuid AND user_uid=$2::uuid`).
		WithArgs(pid, uid).
		WillReturnError(errNoRowsForSqlmock())

	got, err := s.IsMember(ctx, pid, uid)
	if err != nil || got {
		t.Errorf("IsMember=false expected, got=%v err=%v", got, err)
	}
}

// errNoRowsForSqlmock surfaces sql.ErrNoRows from QueryRow.Scan(...) so the
// production-side errors.Is(err, sql.ErrNoRows) path is exercised end-to-end.
func errNoRowsForSqlmock() error { return sql.ErrNoRows }
