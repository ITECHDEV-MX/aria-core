// DB-bound tests for pages/comments.Store using sqlmock. Targets the methods
// that don't depend on a full transactional INSERT pipeline (those are covered
// by the existing comments_test.go integration suite when ARIA_CORE_TEST_DSN is
// set). Focus here is on the simpler Exec/QueryRow paths so the unit-test pass
// remains hermetic.

package comments

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

func TestResolve_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	byUID := uuid.NewString()
	mock.ExpectExec(`
		UPDATE aria_page_comments
		SET is_resolved = TRUE, resolved_by_uid = $1, resolved_at = NOW(), updated_at = NOW()
		WHERE id = $2`).
		WithArgs(byUID, id).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.Resolve(context.Background(), id, byUID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestResolve_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	byUID := uuid.NewString()
	mock.ExpectExec(`
		UPDATE aria_page_comments
		SET is_resolved = TRUE, resolved_by_uid = $1, resolved_at = NOW(), updated_at = NOW()
		WHERE id = $2`).
		WithArgs(byUID, id).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.Resolve(context.Background(), id, byUID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestResolve_RejectsInvalidUID(t *testing.T) {
	s, _ := newMockStore(t)
	if err := s.Resolve(context.Background(), uuid.NewString(), "not-a-uuid"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput, got %v", err)
	}
}

func TestUnresolve_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	mock.ExpectExec(`
		UPDATE aria_page_comments
		SET is_resolved = FALSE, resolved_by_uid = NULL, resolved_at = NULL, updated_at = NOW()
		WHERE id = $1`).
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.Unresolve(context.Background(), id); err != nil {
		t.Fatalf("Unresolve: %v", err)
	}
}

func TestUnresolve_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	mock.ExpectExec(`
		UPDATE aria_page_comments
		SET is_resolved = FALSE, resolved_by_uid = NULL, resolved_at = NULL, updated_at = NOW()
		WHERE id = $1`).
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.Unresolve(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDelete_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	mock.ExpectExec(`DELETE FROM aria_page_comments WHERE id = $1`).
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	mock.ExpectExec(`DELETE FROM aria_page_comments WHERE id = $1`).
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.Delete(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestCountUnresolved_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	pageID := uuid.NewString()
	mock.ExpectQuery(`SELECT COUNT(*) FROM aria_page_comments WHERE page_id = $1 AND is_resolved = FALSE`).
		WithArgs(pageID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	got, err := s.CountUnresolved(context.Background(), pageID)
	if err != nil {
		t.Fatalf("CountUnresolved: %v", err)
	}
	if got != 7 {
		t.Errorf("want 7, got %d", got)
	}
}

func TestCreate_RejectsBlankContent(t *testing.T) {
	s, _ := newMockStore(t)
	_, err := s.Create(context.Background(), CreateParams{
		PageID:    uuid.NewString(),
		ContentMD: "   ",
		AuthorUID: uuid.NewString(),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput for blank content, got %v", err)
	}
}

func TestCreate_RejectsInvalidAuthorUID(t *testing.T) {
	s, _ := newMockStore(t)
	_, err := s.Create(context.Background(), CreateParams{
		PageID:    uuid.NewString(),
		ContentMD: "hello",
		AuthorUID: "not-a-uuid",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput for invalid author uid, got %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	id := uuid.NewString()
	mock.ExpectQuery(`
		SELECT id, page_id, COALESCE(block_anchor,''), COALESCE(parent_comment_id::text,''),
		       content_md, author_uid, is_resolved, COALESCE(resolved_by_uid::text,''),
		       resolved_at, created_at, updated_at
		FROM aria_page_comments WHERE id = $1`).
		WithArgs(id).
		WillReturnError(sql.ErrNoRows)

	if _, err := s.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
