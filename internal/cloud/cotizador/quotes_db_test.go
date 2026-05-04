// DB-bound tests for cotizador Quote/RFP store methods using sqlmock.

package cotizador

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func rfpColumns() []string {
	return []string{
		"id", "lead_id", "source_type", "source_content", "analysis_json",
		"created_by_uid", "created_at", "updated_at",
	}
}

func TestCreateRFP_HappyPath(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	rows := sqlmock.NewRows(rfpColumns()).
		AddRow("rfp-1", "lead-1", "text", "Source text", `{"key":"value"}`, nil, now, now)

	mock.ExpectQuery(`
		INSERT INTO cotizador_rfps (lead_id, source_type, source_content, analysis_json, created_by_uid)
		VALUES ($1::uuid, $2, $3, $4::jsonb, NULLIF($5,'')::uuid)
		RETURNING id::text, lead_id::text, source_type, source_content, analysis_json::text,
		          created_by_uid::text, created_at, updated_at
	`).
		WithArgs("lead-1", "text", "Source text", `{"key":"value"}`, "").
		WillReturnRows(rows)

	rfp, err := s.CreateRFP(ctx, CreateRFPParams{
		LeadID:        "lead-1",
		SourceContent: "Source text",
		AnalysisJSON:  `{"key":"value"}`,
	})
	if err != nil {
		t.Fatalf("CreateRFP: %v", err)
	}
	if rfp.SourceType != "text" {
		t.Errorf("expected default source_type=text, got %q", rfp.SourceType)
	}
}

func TestCreateRFP_RequiresLeadID(t *testing.T) {
	s, _ := newMockStore(t)
	if _, err := s.CreateRFP(context.Background(), CreateRFPParams{}); err == nil {
		t.Errorf("expected error for missing lead_id")
	}
}

func TestCreateRFP_RejectsInvalidJSON(t *testing.T) {
	s, _ := newMockStore(t)
	if _, err := s.CreateRFP(context.Background(), CreateRFPParams{
		LeadID:       "lead-1",
		AnalysisJSON: "not-json",
	}); err == nil {
		t.Errorf("expected error for invalid analysis_json")
	}
}

func TestGetRFP_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery(`
		SELECT id::text, lead_id::text, source_type, source_content, analysis_json::text,
		       created_by_uid::text, created_at, updated_at
		FROM cotizador_rfps WHERE id::text = $1
	`).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	if _, err := s.GetRFP(context.Background(), "missing"); !errors.Is(err, ErrRFPNotFound) {
		t.Errorf("want ErrRFPNotFound, got %v", err)
	}
}

func TestUpdateRFPAnalysis_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec(`
		UPDATE cotizador_rfps SET analysis_json = $1::jsonb, updated_at = NOW() WHERE id::text = $2
	`).
		WithArgs(`{"k":"v"}`, "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.UpdateRFPAnalysis(context.Background(), "missing", `{"k":"v"}`); !errors.Is(err, ErrRFPNotFound) {
		t.Errorf("want ErrRFPNotFound, got %v", err)
	}
}

func TestUpdateRFPAnalysis_RejectsInvalidJSON(t *testing.T) {
	s, _ := newMockStore(t)
	if err := s.UpdateRFPAnalysis(context.Background(), "any", "not-json"); err == nil {
		t.Errorf("expected error for invalid JSON")
	}
}

func TestUpdateQuoteStatus_RejectsInvalidStatus(t *testing.T) {
	s, _ := newMockStore(t)
	if err := s.UpdateQuoteStatus(context.Background(), "any-quote", "fictional", "", ""); !errors.Is(err, ErrInvalidQuoteStatus) {
		t.Errorf("want ErrInvalidQuoteStatus, got %v", err)
	}
}

func TestUpdateQuoteStatus_QuoteNotFound(t *testing.T) {
	s, mock := newMockStore(t)
	quoteID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_quotes WHERE id::text = $1`).
		WithArgs(quoteID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	if err := s.UpdateQuoteStatus(context.Background(), quoteID, QuoteStatusSent, "", ""); !errors.Is(err, ErrQuoteNotFound) {
		t.Errorf("want ErrQuoteNotFound, got %v", err)
	}
}

func TestUpdateQuoteStatus_ApprovedTransition(t *testing.T) {
	s, mock := newMockStore(t)
	quoteID := "11111111-1111-1111-1111-111111111111"
	approver := "22222222-2222-2222-2222-222222222222"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_quotes WHERE id::text = $1`).
		WithArgs(quoteID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("draft"))
	// Approved path uses a special UPDATE with approved_at/approved_by_uid.
	mock.ExpectExec(`
		UPDATE cotizador_quotes
		SET status = $1, approved_at = NOW(), approved_by_uid = NULLIF($2,'')::uuid, updated_at = NOW()
		WHERE id::text = $3
	`).
		WithArgs(QuoteStatusApproved, approver, quoteID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`
		INSERT INTO cotizador_quote_history (quote_id, action, from_status, to_status, by_uid, notes)
		VALUES ($1::uuid, 'status_change', $2, $3, NULLIF($4,'')::uuid, $5)
	`).
		WithArgs(quoteID, "draft", QuoteStatusApproved, approver, "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := s.UpdateQuoteStatus(context.Background(), quoteID, QuoteStatusApproved, approver, ""); err != nil {
		t.Errorf("UpdateQuoteStatus approved: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestUpdateQuoteStatus_NoOpWhenSameStatus(t *testing.T) {
	s, mock := newMockStore(t)
	quoteID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_quotes WHERE id::text = $1`).
		WithArgs(quoteID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(QuoteStatusSent))
	mock.ExpectCommit()

	if err := s.UpdateQuoteStatus(context.Background(), quoteID, QuoteStatusSent, "", ""); err != nil {
		t.Errorf("UpdateQuoteStatus same-status: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestUpdateQuoteStatus_RegularTransition(t *testing.T) {
	s, mock := newMockStore(t)
	quoteID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status FROM cotizador_quotes WHERE id::text = $1`).
		WithArgs(quoteID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(QuoteStatusDraft))
	// Non-approved path uses simpler UPDATE.
	mock.ExpectExec(`
		UPDATE cotizador_quotes SET status = $1, updated_at = NOW() WHERE id::text = $2
	`).
		WithArgs(QuoteStatusSent, quoteID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`
		INSERT INTO cotizador_quote_history (quote_id, action, from_status, to_status, by_uid, notes)
		VALUES ($1::uuid, 'status_change', $2, $3, NULLIF($4,'')::uuid, $5)
	`).
		WithArgs(quoteID, QuoteStatusDraft, QuoteStatusSent, "", "approved by sales").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := s.UpdateQuoteStatus(context.Background(), quoteID, QuoteStatusSent, "", "approved by sales"); err != nil {
		t.Errorf("UpdateQuoteStatus regular: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

func TestGetQuote_NotFound(t *testing.T) {
	s, mock := newMockStore(t)
	// Match the exact SQL emitted by GetQuote — anchors on column tail so
	// schema additions (commit-7 header fields) don't silently slip through.
	mock.ExpectQuery(`SELECT id::text, lead_id::text, rfp_id::text, version, status, currency, subtotal::float8, taxes::float8, total::float8, valid_until, terms, justification, approved_at, approved_by_uid::text, created_by_uid::text, created_by_role, created_at, updated_at, folio, proposal_type, product_name, product_subtitle, tags, prepared_for_company, prepared_for_area, prepared_for_contact_name, prepared_for_contact_email, issue_date, prepared_by_name, prepared_by_email, prepared_by_role FROM cotizador_quotes WHERE id::text = $1`).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	if _, err := s.GetQuote(context.Background(), "missing"); !errors.Is(err, ErrQuoteNotFound) {
		t.Errorf("want ErrQuoteNotFound, got %v", err)
	}
}
