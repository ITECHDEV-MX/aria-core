// DB-bound tests for pages/databases.Store using sqlmock. Targets the simpler
// Exec/QueryRow paths (CountRows, MoveRow, DeleteRow, GetByPage/GetByID NotFound)
// plus validation gates that don't require DB access. Full happy-path
// transactional flows live in databases_test.go behind ARIA_CORE_TEST_DSN.

package databases

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func TestGetByPage_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	pageID := uuid.NewString()
	mock.ExpectQuery(`
		SELECT id, page_id, schema_json, default_view, created_at, updated_at
		FROM aria_page_databases WHERE page_id = $1`).
		WithArgs(pageID).
		WillReturnError(sql.ErrNoRows)

	if _, err := s.GetByPage(context.Background(), pageID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	mock.ExpectQuery(`
		SELECT id, page_id, schema_json, default_view, created_at, updated_at
		FROM aria_page_databases WHERE id = $1`).
		WithArgs(id).
		WillReturnError(sql.ErrNoRows)

	if _, err := s.GetByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMoveRow_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	rowID := uuid.NewString()
	mock.ExpectExec(`UPDATE aria_page_database_rows SET sort_order = $1, updated_at = NOW() WHERE id = $2`).
		WithArgs(7, rowID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.MoveRow(context.Background(), rowID, 7); err != nil {
		t.Errorf("MoveRow: %v", err)
	}
}

func TestMoveRow_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	rowID := uuid.NewString()
	mock.ExpectExec(`UPDATE aria_page_database_rows SET sort_order = $1, updated_at = NOW() WHERE id = $2`).
		WithArgs(0, rowID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.MoveRow(context.Background(), rowID, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteRow_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	rowID := uuid.NewString()
	mock.ExpectExec(`DELETE FROM aria_page_database_rows WHERE id = $1`).
		WithArgs(rowID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.DeleteRow(context.Background(), rowID); err != nil {
		t.Errorf("DeleteRow: %v", err)
	}
}

func TestDeleteRow_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	rowID := uuid.NewString()
	mock.ExpectExec(`DELETE FROM aria_page_database_rows WHERE id = $1`).
		WithArgs(rowID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.DeleteRow(context.Background(), rowID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestCreate_RejectsNonUUIDPageID(t *testing.T) {
	s, _ := newMockStore(t)
	_, err := s.Create(context.Background(), CreateParams{PageID: "not-a-uuid"})
	if !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("want ErrInvalidSchema, got %v", err)
	}
}

func TestCreate_RejectsInvalidDefaultView(t *testing.T) {
	s, _ := newMockStore(t)
	_, err := s.Create(context.Background(), CreateParams{
		PageID:      uuid.NewString(),
		DefaultView: "spreadsheet",
	})
	if !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("want ErrInvalidSchema, got %v", err)
	}
}

func TestCreateView_ValidationGates(t *testing.T) {
	s, _ := newMockStore(t)
	ctx := context.Background()

	if _, err := s.CreateView(ctx, CreateViewParams{ViewType: "spreadsheet"}); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("invalid view_type: want ErrInvalidSchema, got %v", err)
	}
	if _, err := s.CreateView(ctx, CreateViewParams{ViewType: "table", Name: "  "}); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("blank name: want ErrInvalidSchema, got %v", err)
	}
	if _, err := s.CreateView(ctx, CreateViewParams{ViewType: "table", Name: "v1", CreatedByUID: "not-uuid"}); !errors.Is(err, ErrInvalidSchema) {
		t.Errorf("invalid uid: want ErrInvalidSchema, got %v", err)
	}
}
