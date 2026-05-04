// DB-bound tests for cotizador.Store using sqlmock. Pure-function helpers
// stay in pure_funcs_test.go; this file targets the SQL-shaped methods that
// were previously uncovered (CreateLead, GetLead, ListLeads, UpdateLeadStatus).

package cotizador

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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

func leadColumns() []string {
	return []string{
		"id", "name", "company", "email", "phone", "source", "status", "notes",
		"owner_uid", "created_by_uid", "created_by_role", "client_id", "created_at", "updated_at",
	}
}

func TestCreateLead_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	rows := sqlmock.NewRows(leadColumns()).
		AddRow("11111111-1111-1111-1111-111111111111", "ACME", "ACME Corp", "ops@acme.test",
			"", "web", "new", "", nil, nil, "cotizador", nil, now, now)

	mock.ExpectQuery(`
		INSERT INTO cotizador_leads (name, company, email, phone, source, notes, owner_uid, created_by_uid, created_by_role)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,'')::uuid, NULLIF($8,'')::uuid, $9)
		RETURNING id::text, name, company, email, phone, source, status, notes,
		          owner_uid::text, created_by_uid::text, created_by_role,
		          client_id::text, created_at, updated_at
	`).
		WithArgs("ACME", "ACME Corp", "ops@acme.test", "", "web", "", "", "", "cotizador").
		WillReturnRows(rows)

	mock.ExpectExec(`
		INSERT INTO cotizador_lead_history (lead_id, action, to_status, by_uid)
		VALUES ($1::uuid, 'created', 'new', NULLIF($2,'')::uuid)
	`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	lead, err := s.CreateLead(ctx, CreateLeadParams{
		Name:    "ACME",
		Company: "ACME Corp",
		Email:   "ops@acme.test",
		Source:  "web",
	})
	if err != nil {
		t.Fatalf("CreateLead: %v", err)
	}
	if lead.Status != "new" || lead.CreatedByRole != "cotizador" {
		t.Errorf("unexpected lead: %+v", lead)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestCreateLead_RequiresName(t *testing.T) {
	s, _ := newMockStore(t)
	if _, err := s.CreateLead(context.Background(), CreateLeadParams{Name: "  "}); err == nil {
		t.Errorf("blank name: expected error, got nil")
	}
}

func TestGetLead_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery(`
		SELECT id::text, name, company, email, phone, source, status, notes,
		       owner_uid::text, created_by_uid::text, created_by_role,
		       client_id::text, created_at, updated_at
		FROM cotizador_leads WHERE id::text = $1 LIMIT 1
	`).
		WithArgs("missing-id").
		WillReturnError(sql.ErrNoRows)

	if _, err := s.GetLead(context.Background(), "missing-id"); !errors.Is(err, ErrLeadNotFound) {
		t.Errorf("want ErrLeadNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestListLeads_AllStatuses(t *testing.T) {
	s, mock := newMockStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	rows := sqlmock.NewRows(leadColumns()).
		AddRow("a", "Lead A", "", "", "", "", "new", "", nil, nil, "cotizador", nil, now, now).
		AddRow("b", "Lead B", "", "", "", "", "won", "", nil, nil, "cotizador", nil, now, now)

	mock.ExpectQuery(`
		SELECT id::text, name, company, email, phone, source, status, notes,
		       owner_uid::text, created_by_uid::text, created_by_role,
		       client_id::text, created_at, updated_at
		FROM cotizador_leads ORDER BY created_at DESC
	`).WillReturnRows(rows)

	leads, err := s.ListLeads(context.Background(), "")
	if err != nil {
		t.Fatalf("ListLeads: %v", err)
	}
	if len(leads) != 2 {
		t.Errorf("want 2 leads, got %d", len(leads))
	}
}

func TestListLeads_RejectsInvalidStatusFilter(t *testing.T) {
	s, _ := newMockStore(t)
	if _, err := s.ListLeads(context.Background(), "fictional"); !errors.Is(err, ErrInvalidLeadStatus) {
		t.Errorf("want ErrInvalidLeadStatus, got %v", err)
	}
}

func TestUpdateLeadStatus_TransitionsAndAuditLog(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	leadID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_leads WHERE id::text = $1`).
		WithArgs(leadID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("new"))
	mock.ExpectExec(`UPDATE cotizador_leads SET status = $1, updated_at = NOW() WHERE id::text = $2`).
		WithArgs("contacted", leadID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`
		INSERT INTO cotizador_lead_history (lead_id, action, from_status, to_status, by_uid, notes)
		VALUES ($1::uuid, 'status_change', $2, $3, NULLIF($4,'')::uuid, $5)
	`).
		WithArgs(leadID, "new", "contacted", "", "first call").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := s.UpdateLeadStatus(ctx, leadID, "contacted", "", "first call"); err != nil {
		t.Errorf("UpdateLeadStatus: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestUpdateLeadStatus_NoOpWhenSameStatus(t *testing.T) {
	s, mock := newMockStore(t)
	leadID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_leads WHERE id::text = $1`).
		WithArgs(leadID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("won"))
	mock.ExpectCommit()

	if err := s.UpdateLeadStatus(context.Background(), leadID, "won", "", ""); err != nil {
		t.Errorf("UpdateLeadStatus same-status: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestUpdateLeadStatus_RejectsInvalidStatus(t *testing.T) {
	s, _ := newMockStore(t)
	if err := s.UpdateLeadStatus(context.Background(), "any-id", "invalid", "", ""); !errors.Is(err, ErrInvalidLeadStatus) {
		t.Errorf("want ErrInvalidLeadStatus, got %v", err)
	}
}

func TestUpdateLeadStatus_LeadNotFound(t *testing.T) {
	s, mock := newMockStore(t)
	leadID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_leads WHERE id::text = $1`).
		WithArgs(leadID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	if err := s.UpdateLeadStatus(context.Background(), leadID, "contacted", "", ""); !errors.Is(err, ErrLeadNotFound) {
		t.Errorf("want ErrLeadNotFound, got %v", err)
	}
}
