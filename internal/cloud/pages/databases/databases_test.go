package databases

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

// ─── Pure-unit tests (no DB) ────────────────────────────────────────────────

func TestValidateSchema_Valid(t *testing.T) {
	schema := []PropDef{
		{Key: "title", Name: "Title", Type: PropText, Required: true},
		{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"todo", "doing", "done"}},
		{Key: "tags", Name: "Tags", Type: PropMultiSelect, Options: []string{"a", "b"}},
		{Key: "due", Name: "Due Date", Type: PropDate},
		{Key: "score", Name: "Score", Type: PropNumber},
		{Key: "owner", Name: "Owner", Type: PropPerson},
		{Key: "ref", Name: "Ref", Type: PropRelation, RelTo: uuid.NewString()},
	}
	errs := ValidateSchema(schema)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateSchema_DuplicateKey(t *testing.T) {
	schema := []PropDef{
		{Key: "x", Name: "X", Type: PropText},
		{Key: "x", Name: "Y", Type: PropNumber},
	}
	errs := ValidateSchema(schema)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Reason, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate error, got %v", errs)
	}
}

func TestValidateSchema_SelectNeedsOptions(t *testing.T) {
	schema := []PropDef{
		{Key: "status", Name: "Status", Type: PropSelect},
	}
	errs := ValidateSchema(schema)
	if len(errs) == 0 {
		t.Error("expected error for select with no options")
	}
}

func TestValidateSchema_RelationNeedsRelTo(t *testing.T) {
	schema := []PropDef{
		{Key: "ref", Name: "Ref", Type: PropRelation},
	}
	errs := ValidateSchema(schema)
	if len(errs) == 0 {
		t.Error("expected error for relation with no rel_to")
	}
	schema = []PropDef{
		{Key: "ref", Name: "Ref", Type: PropRelation, RelTo: "not-a-uuid"},
	}
	errs = ValidateSchema(schema)
	if len(errs) == 0 {
		t.Error("expected error for invalid rel_to UUID")
	}
}

func TestValidateRow_TypeMismatch(t *testing.T) {
	schema := []PropDef{
		{Key: "score", Name: "Score", Type: PropNumber},
	}
	errs := ValidateRow(schema, map[string]any{"score": "not a number"})
	if len(errs) == 0 {
		t.Error("expected type mismatch error")
	}
}

func TestValidateRow_RequiredMissing(t *testing.T) {
	schema := []PropDef{
		{Key: "title", Name: "Title", Type: PropText, Required: true},
	}
	errs := ValidateRow(schema, map[string]any{})
	if len(errs) == 0 {
		t.Error("expected required error")
	}
}

func TestValidateRow_UnknownProp(t *testing.T) {
	schema := []PropDef{
		{Key: "x", Name: "X", Type: PropText},
	}
	errs := ValidateRow(schema, map[string]any{"x": "ok", "rogue": "no"})
	if len(errs) == 0 {
		t.Error("expected unknown prop error")
	}
}

func TestValidateRow_SelectValidatesOption(t *testing.T) {
	schema := []PropDef{
		{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"a", "b"}},
	}
	if errs := ValidateRow(schema, map[string]any{"status": "a"}); len(errs) != 0 {
		t.Errorf("valid option should pass, got %v", errs)
	}
	if errs := ValidateRow(schema, map[string]any{"status": "c"}); len(errs) == 0 {
		t.Error("invalid option should fail")
	}
}

func TestValidateRow_DateAcceptsRFC3339AndDateOnly(t *testing.T) {
	schema := []PropDef{{Key: "d", Name: "D", Type: PropDate}}
	if errs := ValidateRow(schema, map[string]any{"d": "2026-04-26"}); len(errs) != 0 {
		t.Errorf("YYYY-MM-DD should be valid: %v", errs)
	}
	if errs := ValidateRow(schema, map[string]any{"d": "2026-04-26T10:00:00Z"}); len(errs) != 0 {
		t.Errorf("RFC3339 should be valid: %v", errs)
	}
	if errs := ValidateRow(schema, map[string]any{"d": "not a date"}); len(errs) == 0 {
		t.Error("invalid date should fail")
	}
}

func TestValidateRow_EmailValidates(t *testing.T) {
	schema := []PropDef{{Key: "e", Name: "E", Type: PropEmail}}
	if errs := ValidateRow(schema, map[string]any{"e": "x@y.com"}); len(errs) != 0 {
		t.Errorf("valid email failed: %v", errs)
	}
	if errs := ValidateRow(schema, map[string]any{"e": "not-email"}); len(errs) == 0 {
		t.Error("invalid email should fail")
	}
}

func TestValidateRow_URLValidates(t *testing.T) {
	schema := []PropDef{{Key: "u", Name: "U", Type: PropURL}}
	if errs := ValidateRow(schema, map[string]any{"u": "https://x.com"}); len(errs) != 0 {
		t.Errorf("valid url failed: %v", errs)
	}
	if errs := ValidateRow(schema, map[string]any{"u": "x.com"}); len(errs) == 0 {
		t.Error("invalid url should fail")
	}
}

func TestBuildWhereClause_EqAndContains(t *testing.T) {
	schema := []PropDef{
		{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"a", "b"}},
		{Key: "title", Name: "Title", Type: PropText},
	}
	frags, args, err := BuildWhereClause([]Filter{
		{Key: "status", Op: OpEq, Value: "a"},
		{Key: "title", Op: OpContains, Value: "hello"},
	}, schema, 1)
	if err != nil {
		t.Fatalf("build where: %v", err)
	}
	if !strings.Contains(frags, "props_json->>'status'") {
		t.Errorf("expected status fragment, got %q", frags)
	}
	if !strings.Contains(frags, "ILIKE") {
		t.Errorf("expected ILIKE for contains, got %q", frags)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
	if args[1] != "%hello%" {
		t.Errorf("expected wildcards on contains, got %q", args[1])
	}
}

func TestBuildWhereClause_UnknownKey(t *testing.T) {
	schema := []PropDef{{Key: "x", Name: "X", Type: PropText}}
	_, _, err := BuildWhereClause([]Filter{{Key: "y", Op: OpEq, Value: "z"}}, schema, 1)
	if err == nil {
		t.Error("expected error for unknown filter key")
	}
}

func TestBuildOrderBy_DefaultsToSortOrder(t *testing.T) {
	out, err := BuildOrderBy(nil, []PropDef{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "sort_order") {
		t.Errorf("default order should use sort_order: %q", out)
	}
}

func TestBuildOrderBy_NumericCast(t *testing.T) {
	schema := []PropDef{{Key: "n", Name: "N", Type: PropNumber}}
	out, err := BuildOrderBy([]Sort{{Key: "n", Direction: "desc"}}, schema)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "::numeric") {
		t.Errorf("numeric prop should cast: %q", out)
	}
	if !strings.Contains(out, "DESC") {
		t.Errorf("desc not propagated: %q", out)
	}
}

func TestGroupRowsByKey(t *testing.T) {
	rows := []*Row{
		{ID: "1", Props: map[string]any{"status": "todo"}},
		{ID: "2", Props: map[string]any{"status": "doing"}},
		{ID: "3", Props: map[string]any{"status": "todo"}},
		{ID: "4", Props: map[string]any{}},
	}
	g := GroupRowsByKey(rows, "status")
	if len(g["todo"]) != 2 {
		t.Errorf("expected 2 todo, got %d", len(g["todo"]))
	}
	if len(g["doing"]) != 1 {
		t.Errorf("expected 1 doing")
	}
	if len(g[""]) != 1 {
		t.Errorf("missing value should bucket as empty key, got %d", len(g[""]))
	}
}

func TestAsTextArray(t *testing.T) {
	if asTextArray(nil) != "{}" {
		t.Error("empty should be {}")
	}
	if asTextArray([]string{"a", "b"}) != `{"a","b"}` {
		t.Errorf("got %q", asTextArray([]string{"a", "b"}))
	}
	// Escape comilla doble
	if got := asTextArray([]string{`a"b`}); got != `{"a\"b"}` {
		t.Errorf("expected escaped quote, got %q", got)
	}
}

// ─── Integration tests (require ARIA_CORE_TEST_DSN) ─────────────────────────

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
	// Las migraciones las aplica cloudstore.New() en runtime real; aquí
	// asumimos que el DSN ya tiene las tablas (mismo patrón que vault).
	// Si falta, hacemos best-effort: crear tablas mínimas para tests.
	ddls := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,
		`CREATE TABLE IF NOT EXISTS aria_page_databases (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			page_id UUID NOT NULL UNIQUE,
			schema_json JSONB NOT NULL DEFAULT '[]'::jsonb,
			default_view TEXT NOT NULL DEFAULT 'table',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS aria_page_database_rows (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			database_id UUID NOT NULL REFERENCES aria_page_databases(id) ON DELETE CASCADE,
			props_json JSONB NOT NULL DEFAULT '{}'::jsonb,
			sort_order INT NOT NULL DEFAULT 0,
			created_by_uid UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS aria_page_database_views (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			database_id UUID NOT NULL REFERENCES aria_page_databases(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			view_type TEXT NOT NULL CHECK (view_type IN ('table','kanban','gallery','list','calendar')),
			config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
			sort_order INT NOT NULL DEFAULT 0,
			created_by_uid UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, q := range ddls {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return db
}

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	db := openTestDB(t)
	s := New(db)
	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM aria_page_database_rows WHERE created_by_uid IN (SELECT created_by_uid FROM aria_page_database_rows WHERE created_by_uid::text LIKE '%')`)
		_, _ = db.Exec(`DELETE FROM aria_page_database_views`)
		_, _ = db.Exec(`DELETE FROM aria_page_databases`)
		_ = db.Close()
	}
	return s, cleanup
}

func TestIntegration_CreateAndRows(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	pageID := uuid.NewString()
	creator := uuid.NewString()
	db, err := s.Create(context.Background(), CreateParams{
		PageID: pageID,
		Schema: []PropDef{
			{Key: "title", Name: "Title", Type: PropText, Required: true},
			{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"todo", "done"}},
		},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if db.ID == "" {
		t.Fatal("missing id")
	}
	if db.PageID != pageID {
		t.Errorf("pageID mismatch")
	}

	// Duplicate page → conflict
	if _, err := s.Create(context.Background(), CreateParams{PageID: pageID, CreatedByUID: creator}); !errors.Is(err, ErrConflict) {
		t.Errorf("expected conflict, got %v", err)
	}

	// Create row OK
	row, err := s.CreateRow(context.Background(), CreateRowParams{
		DatabaseID:   db.ID,
		Props:        map[string]any{"title": "Task 1", "status": "todo"},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create row: %v", err)
	}
	if row.ID == "" {
		t.Fatal("row missing id")
	}

	// Row missing required field → reject
	if _, err := s.CreateRow(context.Background(), CreateRowParams{
		DatabaseID:   db.ID,
		Props:        map[string]any{"status": "todo"},
		CreatedByUID: creator,
	}); !errors.Is(err, ErrInvalidRow) {
		t.Errorf("expected invalid row, got %v", err)
	}

	// List rows
	rows, err := s.ListRows(context.Background(), db.ID, ListRowsOpts{})
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}

	// Update props
	if _, err := s.UpdateRowProps(context.Background(), row.ID, map[string]any{"status": "done"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetRow(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Props["status"] != "done" {
		t.Errorf("expected status=done, got %v", got.Props["status"])
	}

	// Filter by status
	filtered, err := s.ListRows(context.Background(), db.ID, ListRowsOpts{
		Filters: []Filter{{Key: "status", Op: OpEq, Value: "done"}},
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered, got %d", len(filtered))
	}
	zero, _ := s.ListRows(context.Background(), db.ID, ListRowsOpts{
		Filters: []Filter{{Key: "status", Op: OpEq, Value: "todo"}},
	})
	if len(zero) != 0 {
		t.Errorf("expected 0 filtered, got %d", len(zero))
	}

	// Delete
	if err := s.DeleteRow(context.Background(), row.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetRow(context.Background(), row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestIntegration_Views(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	pageID := uuid.NewString()
	creator := uuid.NewString()
	db, err := s.Create(context.Background(), CreateParams{
		PageID:       pageID,
		Schema:       []PropDef{{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"a", "b"}}},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	v, err := s.CreateView(context.Background(), CreateViewParams{
		DatabaseID:   db.ID,
		Name:         "Kanban by Status",
		ViewType:     "kanban",
		Config:       ViewConfig{GroupByKey: "status"},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create view: %v", err)
	}
	if v.Config.GroupByKey != "status" {
		t.Errorf("config not persisted")
	}
	views, err := s.ListViews(context.Background(), db.ID)
	if err != nil {
		t.Fatalf("list views: %v", err)
	}
	if len(views) != 1 {
		t.Errorf("expected 1 view, got %d", len(views))
	}
	updated, err := s.UpdateView(context.Background(), v.ID, "Renamed", "kanban", ViewConfig{GroupByKey: "status", VisibleProps: []string{"status"}}, 1)
	if err != nil {
		t.Fatalf("update view: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("name not updated")
	}
	if len(updated.Config.VisibleProps) != 1 {
		t.Errorf("visible props not persisted")
	}
}

func TestIntegration_KanbanGrouping(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	creator := uuid.NewString()
	db, _ := s.Create(context.Background(), CreateParams{
		PageID:       pageID,
		Schema:       []PropDef{{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"todo", "done"}}},
		CreatedByUID: creator,
	})
	for _, st := range []string{"todo", "todo", "done"} {
		if _, err := s.CreateRow(context.Background(), CreateRowParams{
			DatabaseID:   db.ID,
			Props:        map[string]any{"status": st},
			CreatedByUID: creator,
		}); err != nil {
			t.Fatalf("create row: %v", err)
		}
	}
	rows, err := s.ListRows(context.Background(), db.ID, ListRowsOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	groups := GroupRowsByKey(rows, "status")
	if len(groups["todo"]) != 2 {
		t.Errorf("expected 2 todo, got %d", len(groups["todo"]))
	}
	if len(groups["done"]) != 1 {
		t.Errorf("expected 1 done, got %d", len(groups["done"]))
	}

	// Mover una row entre grupos = updatear status
	first := groups["todo"][0]
	if _, err := s.UpdateRowProps(context.Background(), first.ID, map[string]any{"status": "done"}); err != nil {
		t.Fatalf("move: %v", err)
	}
	rows2, _ := s.ListRows(context.Background(), db.ID, ListRowsOpts{})
	groups2 := GroupRowsByKey(rows2, "status")
	if len(groups2["done"]) != 2 {
		t.Errorf("expected 2 done after drag, got %d", len(groups2["done"]))
	}
}

func TestIntegration_UpdateSchemaPrune(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	creator := uuid.NewString()
	db, _ := s.Create(context.Background(), CreateParams{
		PageID: pageID,
		Schema: []PropDef{
			{Key: "title", Name: "Title", Type: PropText},
			{Key: "removed", Name: "Removed", Type: PropText},
		},
		CreatedByUID: creator,
	})
	row, err := s.CreateRow(context.Background(), CreateRowParams{
		DatabaseID:   db.ID,
		Props:        map[string]any{"title": "x", "removed": "y"},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update schema sin "removed", con prune
	if _, err := s.UpdateSchema(context.Background(), db.ID,
		[]PropDef{{Key: "title", Name: "Title", Type: PropText}}, true); err != nil {
		t.Fatalf("update schema: %v", err)
	}
	got, err := s.GetRow(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, present := got.Props["removed"]; present {
		t.Errorf("expected 'removed' to be pruned, got %v", got.Props)
	}
	if got.Props["title"] != "x" {
		t.Errorf("expected title preserved, got %v", got.Props)
	}
}
