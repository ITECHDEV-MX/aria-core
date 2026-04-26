package teamprojects

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ─── Unit tests (siempre corren, no requieren DB) ────────────────────────

func TestSlugFromName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"basic", "Hello World", "hello-world"},
		{"specials", "ARIA Core: Wave 7!", "aria-core-wave-7"},
		{"unicode-strip", "Café Móvil", "caf-mvil"},
		{"trim-dashes", "  --foo--bar--  ", "foo-bar"},
		{"empty-fallback", "   ", "project-"}, // prefix only — uuid suffix variable
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlugFromName(tc.in)
			if tc.name == "empty-fallback" {
				if !strings.HasPrefix(got, "project-") {
					t.Errorf("want prefix project-, got %q", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("SlugFromName(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsUUID(t *testing.T) {
	if !isUUID(uuid.NewString()) {
		t.Error("valid uuid should pass")
	}
	if isUUID("not-a-uuid") {
		t.Error("invalid string should not pass")
	}
}

// ─── Integration tests (requieren ARIA_CORE_TEST_DSN) ────────────────────

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_TEST_DSN"))
	if dsn == "" {
		t.Skip("ARIA_CORE_TEST_DSN not set; skipping integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("ping db failed (skipping): %v", err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedUser ensures cloud_users row exists (FK requirement).
func seedUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	uid := uuid.NewString()
	_, err := db.Exec(`INSERT INTO cloud_users (uid, email, name, password_hash, roles, is_active)
		VALUES ($1::uuid, $2, $3, '', ARRAY['dev'], TRUE)
		ON CONFLICT DO NOTHING`,
		uid, "user-"+uid[:8]+"@test", "Test User")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return uid
}

func cleanupTestRows(t *testing.T, db *sql.DB) {
	t.Helper()
	_, _ = db.Exec(`DELETE FROM aria_team_projects WHERE slug LIKE 'test-%'`)
}

func setupStore(t *testing.T) (*PgStore, *sql.DB, func()) {
	t.Helper()
	db := openTestDB(t)
	cleanupTestRows(t, db)
	return NewPgStore(db), db, func() {
		cleanupTestRows(t, db)
		_ = db.Close()
	}
}

func TestCreateProject_Success(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, err := s.CreateProject(context.Background(), CreateProjectParams{
		Slug:         "test-cp-1",
		Name:         "Test Create Project 1",
		Description:  "Just a test",
		CreatedByUID: uid,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if pr.Slug != "test-cp-1" {
		t.Errorf("slug = %q", pr.Slug)
	}
	// Creator debe ser owner automáticamente.
	members, err := s.ListMembers(context.Background(), pr.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 || members[0].Role != "owner" {
		t.Errorf("expected creator as owner, got %+v", members)
	}
}

func TestCreateProject_DuplicateSlug(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	_, err := s.CreateProject(context.Background(), CreateProjectParams{
		Slug:         "test-dup",
		Name:         "Dup 1",
		CreatedByUID: uid,
	})
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	_, err = s.CreateProject(context.Background(), CreateProjectParams{
		Slug:         "test-dup",
		Name:         "Dup 2",
		CreatedByUID: uid,
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestAddMember_RoleUpdate(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	uid2 := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-mem", Name: "Mem", CreatedByUID: uid})
	if err := s.AddMember(context.Background(), pr.ID, uid2, "member", uid); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := s.AddMember(context.Background(), pr.ID, uid2, "lead", uid); err != nil {
		t.Fatalf("update role: %v", err)
	}
	members, _ := s.ListMembers(context.Background(), pr.ID)
	for _, m := range members {
		if m.UserUID == uid2 && m.Role != "lead" {
			t.Errorf("role not updated: %+v", m)
		}
	}
}

func TestArchiveProject(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-arch", Name: "Arch", CreatedByUID: uid})
	if err := s.ArchiveProject(context.Background(), pr.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, _ := s.GetProject(context.Background(), pr.ID)
	if got.Status != "archived" {
		t.Errorf("status = %q", got.Status)
	}
	if got.ArchivedAt == nil {
		t.Errorf("archived_at not set")
	}
}
