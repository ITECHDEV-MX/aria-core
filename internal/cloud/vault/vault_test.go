package vault

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

func TestPrincipal_HasRoleIsAdmin(t *testing.T) {
	p := Principal{UID: "u1", Roles: []string{"dev", "admin"}}
	if !p.IsAdmin() {
		t.Error("admin role should set IsAdmin true")
	}
	if !p.HasRole("DEV") {
		t.Error("HasRole should be case-insensitive")
	}
	if p.HasRole("cotizador") {
		t.Error("non-existing role should return false")
	}
}

func TestValidateCreate(t *testing.T) {
	cases := []struct {
		name    string
		p       CreateParams
		wantErr bool
	}{
		{
			"valid",
			CreateParams{Name: "X", Category: "api_token", Scope: "personal", Value: "v", CreatedByUID: uuid.NewString()},
			false,
		},
		{
			"missing name",
			CreateParams{Category: "api_token", Scope: "personal", Value: "v", CreatedByUID: uuid.NewString()},
			true,
		},
		{
			"invalid category",
			CreateParams{Name: "X", Category: "lol", Scope: "personal", Value: "v", CreatedByUID: uuid.NewString()},
			true,
		},
		{
			"invalid scope",
			CreateParams{Name: "X", Category: "api_token", Scope: "weird", Value: "v", CreatedByUID: uuid.NewString()},
			true,
		},
		{
			"client_knowledge needs client_id",
			CreateParams{Name: "X", Category: "api_token", Scope: "client_knowledge", Value: "v", CreatedByUID: uuid.NewString()},
			true,
		},
		{
			"empty value",
			CreateParams{Name: "X", Category: "api_token", Scope: "personal", CreatedByUID: uuid.NewString()},
			true,
		},
		{
			"empty creator",
			CreateParams{Name: "X", Category: "api_token", Scope: "personal", Value: "v"},
			true,
		},
		{
			"invalid policy",
			CreateParams{Name: "X", Category: "api_token", Scope: "personal", Value: "v", CreatedByUID: uuid.NewString(), RotationPolicy: "yearly"},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCreate(&tc.p)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(errors.New("ERROR: duplicate key value violates unique constraint (SQLSTATE 23505)")) {
		t.Error("expected true for 23505 error")
	}
	if isUniqueViolation(errors.New("syntax error")) {
		t.Error("expected false for unrelated error")
	}
	if isUniqueViolation(nil) {
		t.Error("expected false for nil")
	}
}

// ─── Integration tests (require ARIA_CORE_TEST_DSN) ────────────────────────

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

func setupTestStore(t *testing.T) (*PgStore, func()) {
	t.Helper()
	db := openTestDB(t)
	hexKey, _ := GenerateMasterKeyHex()
	c, _ := NewCrypto(hexKey)
	s := New(db, c)
	cleanup := func() {
		// Clean up rows created by these tests; conservative.
		_, _ = db.Exec(`DELETE FROM aria_secret_access_log WHERE accessed_by_uid IN (SELECT created_by_uid FROM aria_secrets WHERE name LIKE 'test_%')`)
		_, _ = db.Exec(`DELETE FROM aria_secret_grants WHERE secret_id IN (SELECT id FROM aria_secrets WHERE name LIKE 'test_%')`)
		_, _ = db.Exec(`DELETE FROM aria_secrets WHERE name LIKE 'test_%'`)
		_ = db.Close()
	}
	return s, cleanup
}

func TestStoreIntegration_CreateAndReveal(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	creator := uuid.NewString()
	sec, err := s.Create(context.Background(), CreateParams{
		Name: "test_db_password", Category: "db_password", Scope: "personal",
		Value: "supersecret", CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sec.ID == "" {
		t.Fatal("create should return ID")
	}

	pt, err := s.Reveal(context.Background(), sec.ID, Principal{UID: creator, Roles: []string{"dev"}}, "test")
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if pt != "supersecret" {
		t.Errorf("reveal mismatch: %q", pt)
	}
}

func TestStoreIntegration_ACL_DenyDefault(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	creator := uuid.NewString()
	other := uuid.NewString()
	sec, err := s.Create(context.Background(), CreateParams{
		Name: "test_api_key_acl", Category: "api_token", Scope: "personal",
		Value: "v", CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Otro user no-admin sin grant: deny.
	if _, err := s.Reveal(context.Background(), sec.ID, Principal{UID: other, Roles: []string{"dev"}}, "test"); !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Creator: allow.
	if _, err := s.Reveal(context.Background(), sec.ID, Principal{UID: creator, Roles: []string{"dev"}}, "test"); err != nil {
		t.Errorf("creator should reveal: %v", err)
	}

	// Admin (no creator): allow.
	if _, err := s.Reveal(context.Background(), sec.ID, Principal{UID: uuid.NewString(), Roles: []string{"admin"}}, "test"); err != nil {
		t.Errorf("admin should reveal: %v", err)
	}
}

func TestStoreIntegration_GrantThenAccess(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	creator := uuid.NewString()
	other := uuid.NewString()
	sec, _ := s.Create(context.Background(), CreateParams{
		Name: "test_grant_secret", Category: "api_token", Scope: "team",
		Value: "v", CreatedByUID: creator,
	})

	// Sin grant: deny.
	if ok, _ := s.CanAccess(context.Background(), sec.ID, Principal{UID: other, Roles: []string{"dev"}}, PermRead); ok {
		t.Error("expected deny without grant")
	}

	// Grant uid=other read.
	if err := s.Grant(context.Background(), GrantParams{
		SecretID: sec.ID, GrantedToUID: other, Permission: PermRead, GrantedByUID: creator,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if ok, err := s.CanAccess(context.Background(), sec.ID, Principal{UID: other, Roles: []string{"dev"}}, PermRead); err != nil || !ok {
		t.Errorf("expected allow after grant, ok=%v err=%v", ok, err)
	}
}

func TestStoreIntegration_RotateSupersedes(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	creator := uuid.NewString()
	sec, _ := s.Create(context.Background(), CreateParams{
		Name: "test_rotate_secret", Category: "api_token", Scope: "personal",
		Value: "v1", CreatedByUID: creator,
	})

	newSec, err := s.Rotate(context.Background(), sec.ID, "v2", Principal{UID: creator, Roles: []string{"dev"}})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newSec.ID == sec.ID {
		t.Error("rotate should produce new id")
	}

	pt, err := s.Reveal(context.Background(), newSec.ID, Principal{UID: creator, Roles: []string{"dev"}}, "after rotate")
	if err != nil {
		t.Fatalf("reveal new: %v", err)
	}
	if pt != "v2" {
		t.Errorf("rotate mismatch: %q", pt)
	}

	// El viejo debería seguir descifrable (ya que la encryption usa keys ligadas a id+name+key_id),
	// pero is_active=FALSE.
	old, _ := s.GetMetadata(context.Background(), sec.ID)
	if old.IsActive {
		t.Error("rotated old should be inactive")
	}
}

func TestStoreIntegration_AuditLogPersists(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	creator := uuid.NewString()
	sec, _ := s.Create(context.Background(), CreateParams{
		Name: "test_audit_secret", Category: "api_token", Scope: "personal",
		Value: "v", CreatedByUID: creator,
	})
	_, _ = s.Reveal(context.Background(), sec.ID, Principal{UID: creator, Roles: []string{"dev"}}, "test")

	entries, err := s.AccessLog(context.Background(), sec.ID, 50)
	if err != nil {
		t.Fatalf("access log: %v", err)
	}
	// Esperamos al menos: create + read.
	if len(entries) < 2 {
		t.Errorf("expected >=2 entries, got %d", len(entries))
	}
	hasCreate, hasRead := false, false
	for _, e := range entries {
		if e.Action == "create" {
			hasCreate = true
		}
		if e.Action == "read" {
			hasRead = true
		}
	}
	if !hasCreate || !hasRead {
		t.Errorf("expected create+read in log, got %v", entries)
	}
}

func TestStoreIntegration_DegradedModeBlocksCreate(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	c, _ := NewCrypto("") // empty → degraded
	s := New(db, c)
	if s.Available() {
		t.Fatal("expected degraded")
	}
	_, err := s.Create(context.Background(), CreateParams{
		Name: "test_degraded", Category: "api_token", Scope: "personal",
		Value: "v", CreatedByUID: uuid.NewString(),
	})
	if !errors.Is(err, ErrDegraded) {
		t.Errorf("expected ErrDegraded, got %v", err)
	}
}
