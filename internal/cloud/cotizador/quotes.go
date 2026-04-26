package cotizador

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	QuoteStatusDraft    = "draft"
	QuoteStatusSent     = "sent"
	QuoteStatusInReview = "in_review"
	QuoteStatusApproved = "approved"
	QuoteStatusRejected = "rejected"
	QuoteStatusExpired  = "expired"
)

var quoteStatuses = []string{QuoteStatusDraft, QuoteStatusSent, QuoteStatusInReview, QuoteStatusApproved, QuoteStatusRejected, QuoteStatusExpired}

func ValidQuoteStatus(s string) bool {
	for _, v := range quoteStatuses {
		if v == s {
			return true
		}
	}
	return false
}

func AllQuoteStatuses() []string {
	out := make([]string, len(quoteStatuses))
	copy(out, quoteStatuses)
	return out
}

func QuoteStatusLabel(s string) string {
	switch s {
	case QuoteStatusDraft:
		return "Borrador"
	case QuoteStatusSent:
		return "Enviada"
	case QuoteStatusInReview:
		return "En revisión"
	case QuoteStatusApproved:
		return "Aprobada"
	case QuoteStatusRejected:
		return "Rechazada"
	case QuoteStatusExpired:
		return "Expirada"
	default:
		return s
	}
}

var (
	ErrRFPNotFound        = errors.New("rfp not found")
	ErrQuoteNotFound      = errors.New("quote not found")
	ErrInvalidQuoteStatus = errors.New("invalid quote status")
)

type RFP struct {
	ID            string
	LeadID        string
	SourceType    string
	SourceContent string
	AnalysisJSON  string
	CreatedByUID  sql.NullString
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Quote struct {
	ID            string
	LeadID        string
	RFPID         sql.NullString
	Version       int
	Status        string
	Currency      string
	Subtotal      float64
	Taxes         float64
	Total         float64
	ValidUntil    sql.NullTime
	Terms         string
	Justification string
	ApprovedAt    sql.NullTime
	ApprovedByUID sql.NullString
	CreatedByUID  sql.NullString
	CreatedByRole string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// Header propuesta (commit 7)
	Folio                   sql.NullString
	ProposalType            string
	ProductName             string
	ProductSubtitle         string
	Tags                    []string
	PreparedForCompany      string
	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string
	IssueDate               sql.NullTime
	PreparedByName          string
	PreparedByEmail         string
	PreparedByRole          string
}

type QuoteSection struct {
	ID         string
	QuoteID    string
	Key        string
	Title      string
	ContentMD  string
	SortOrder  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type QuoteItem struct {
	ID          string
	QuoteID     string
	SKU         string
	Description string
	Qty         float64
	UnitPrice   float64
	Subtotal    float64
	SortOrder   int
}

type QuoteHistoryEntry struct {
	ID           int64
	QuoteID      string
	Action       string
	FromStatus   sql.NullString
	ToStatus     sql.NullString
	ByUID        sql.NullString
	SnapshotJSON sql.NullString
	Notes        string
	OccurredAt   time.Time
}

// === RFPs ===

type CreateRFPParams struct {
	LeadID        string
	SourceType    string // 'text' | 'pdf' | 'url'
	SourceContent string
	AnalysisJSON  string // JSON string del análisis (puede venir vacío al crear y se actualiza después)
	CreatedByUID  string
}

func (s *Store) CreateRFP(ctx context.Context, p CreateRFPParams) (*RFP, error) {
	if strings.TrimSpace(p.LeadID) == "" {
		return nil, fmt.Errorf("lead_id is required")
	}
	if strings.TrimSpace(p.SourceType) == "" {
		p.SourceType = "text"
	}
	analysis := strings.TrimSpace(p.AnalysisJSON)
	if analysis == "" {
		analysis = "{}"
	}
	if !json.Valid([]byte(analysis)) {
		return nil, fmt.Errorf("analysis_json is not valid JSON")
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO cotizador_rfps (lead_id, source_type, source_content, analysis_json, created_by_uid)
		VALUES ($1::uuid, $2, $3, $4::jsonb, NULLIF($5,'')::uuid)
		RETURNING id::text, lead_id::text, source_type, source_content, analysis_json::text,
		          created_by_uid::text, created_at, updated_at
	`, p.LeadID, p.SourceType, p.SourceContent, analysis, strings.TrimSpace(p.CreatedByUID))
	return scanRFP(row)
}

func (s *Store) GetRFP(ctx context.Context, id string) (*RFP, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, lead_id::text, source_type, source_content, analysis_json::text,
		       created_by_uid::text, created_at, updated_at
		FROM cotizador_rfps WHERE id::text = $1
	`, id)
	rfp, err := scanRFP(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRFPNotFound
		}
		return nil, err
	}
	return rfp, nil
}

func (s *Store) ListRFPsByLead(ctx context.Context, leadID string) ([]*RFP, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, lead_id::text, source_type, source_content, analysis_json::text,
		       created_by_uid::text, created_at, updated_at
		FROM cotizador_rfps WHERE lead_id::text = $1 ORDER BY created_at DESC
	`, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RFP
	for rows.Next() {
		rfp, err := scanRFP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rfp)
	}
	return out, rows.Err()
}

// UpdateRFPAnalysis actualiza solo el analysis_json (pensado para que un agente
// MCP llame con el JSON estructurado después de leer el RFP).
func (s *Store) UpdateRFPAnalysis(ctx context.Context, id, analysisJSON string) error {
	if !json.Valid([]byte(analysisJSON)) {
		return fmt.Errorf("analysis_json is not valid JSON")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cotizador_rfps SET analysis_json = $1::jsonb, updated_at = NOW() WHERE id::text = $2
	`, analysisJSON, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRFPNotFound
	}
	return nil
}

// === Quotes ===

type CreateQuoteParams struct {
	LeadID        string
	RFPID         string // opcional
	Currency      string
	ValidUntil    *time.Time
	Terms         string
	Justification string
	CreatedByUID  string
	Role          string // para created_by_role
	Items         []CreateQuoteItemParams
}

type CreateQuoteItemParams struct {
	SKU         string
	Description string
	Qty         float64
	UnitPrice   float64
}

// CreateQuote inserta quote + items en transacción, calcula totales, asigna version.
func (s *Store) CreateQuote(ctx context.Context, p CreateQuoteParams) (*Quote, error) {
	if strings.TrimSpace(p.LeadID) == "" {
		return nil, fmt.Errorf("lead_id is required")
	}
	currency := strings.TrimSpace(p.Currency)
	if currency == "" {
		currency = "MXN"
	}
	role := strings.TrimSpace(p.Role)
	if role == "" {
		role = "cotizador"
	}
	if len(p.Items) == 0 {
		return nil, fmt.Errorf("at least one item is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Calcular siguiente version para este lead.
	var nextVersion int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0)+1 FROM cotizador_quotes WHERE lead_id::text = $1`, p.LeadID,
	).Scan(&nextVersion); err != nil {
		return nil, fmt.Errorf("compute version: %w", err)
	}

	// Calcular subtotales/total. Sin impuestos por ahora (taxes=0).
	subtotal := 0.0
	for i := range p.Items {
		p.Items[i].Qty = roundTo(p.Items[i].Qty, 3)
		p.Items[i].UnitPrice = roundTo(p.Items[i].UnitPrice, 2)
		line := roundTo(p.Items[i].Qty*p.Items[i].UnitPrice, 2)
		subtotal += line
	}
	subtotal = roundTo(subtotal, 2)
	total := subtotal // taxes=0 por ahora

	var validUntil any
	if p.ValidUntil != nil {
		validUntil = p.ValidUntil.UTC()
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO cotizador_quotes (lead_id, rfp_id, version, currency, subtotal, taxes, total,
		                              valid_until, terms, justification, created_by_uid, created_by_role)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, $4, $5, 0, $6,
		        $7::date, $8, $9, NULLIF($10,'')::uuid, $11)
		RETURNING id::text, lead_id::text, rfp_id::text, version, status, currency,
		          subtotal::float8, taxes::float8, total::float8,
		          valid_until, terms, justification, approved_at, approved_by_uid::text,
		          created_by_uid::text, created_by_role, created_at, updated_at,
		          folio, proposal_type, product_name, product_subtitle, tags,
		          prepared_for_company, prepared_for_area, prepared_for_contact_name, prepared_for_contact_email,
		          issue_date, prepared_by_name, prepared_by_email, prepared_by_role
	`, p.LeadID, strings.TrimSpace(p.RFPID), nextVersion, currency, subtotal, total,
		validUntil, p.Terms, p.Justification, strings.TrimSpace(p.CreatedByUID), role)
	q, err := scanQuote(row)
	if err != nil {
		return nil, fmt.Errorf("insert quote: %w", err)
	}
	for i, it := range p.Items {
		line := roundTo(it.Qty*it.UnitPrice, 2)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cotizador_quote_items (quote_id, sku, description, qty, unit_price, subtotal, sort_order)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		`, q.ID, it.SKU, it.Description, it.Qty, it.UnitPrice, line, i+1); err != nil {
			return nil, fmt.Errorf("insert item %d: %w", i, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cotizador_quote_history (quote_id, action, to_status, by_uid)
		VALUES ($1::uuid, 'created', 'draft', NULLIF($2,'')::uuid)
	`, q.ID, strings.TrimSpace(p.CreatedByUID)); err != nil {
		return nil, fmt.Errorf("insert history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return q, nil
}

func (s *Store) GetQuote(ctx context.Context, id string) (*Quote, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, lead_id::text, rfp_id::text, version, status, currency,
		       subtotal::float8, taxes::float8, total::float8,
		       valid_until, terms, justification, approved_at, approved_by_uid::text,
		       created_by_uid::text, created_by_role, created_at, updated_at,
		       folio, proposal_type, product_name, product_subtitle, tags,
		       prepared_for_company, prepared_for_area, prepared_for_contact_name, prepared_for_contact_email,
		       issue_date, prepared_by_name, prepared_by_email, prepared_by_role
		FROM cotizador_quotes WHERE id::text = $1
	`, id)
	q, err := scanQuote(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrQuoteNotFound
		}
		return nil, err
	}
	return q, nil
}

func (s *Store) ListQuotesByLead(ctx context.Context, leadID string) ([]*Quote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, lead_id::text, rfp_id::text, version, status, currency,
		       subtotal::float8, taxes::float8, total::float8,
		       valid_until, terms, justification, approved_at, approved_by_uid::text,
		       created_by_uid::text, created_by_role, created_at, updated_at,
		       folio, proposal_type, product_name, product_subtitle, tags,
		       prepared_for_company, prepared_for_area, prepared_for_contact_name, prepared_for_contact_email,
		       issue_date, prepared_by_name, prepared_by_email, prepared_by_role
		FROM cotizador_quotes WHERE lead_id::text = $1 ORDER BY version DESC
	`, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Quote
	for rows.Next() {
		q, err := scanQuote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) ListQuoteItems(ctx context.Context, quoteID string) ([]*QuoteItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, quote_id::text, sku, description, qty::float8, unit_price::float8, subtotal::float8, sort_order
		FROM cotizador_quote_items WHERE quote_id::text = $1 ORDER BY sort_order, created_at
	`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*QuoteItem
	for rows.Next() {
		var it QuoteItem
		if err := rows.Scan(&it.ID, &it.QuoteID, &it.SKU, &it.Description, &it.Qty, &it.UnitPrice, &it.Subtotal, &it.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

func (s *Store) UpdateQuoteStatus(ctx context.Context, quoteID, newStatus, byUID, notes string) error {
	if !ValidQuoteStatus(newStatus) {
		return ErrInvalidQuoteStatus
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var fromStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM cotizador_quotes WHERE id::text = $1`, quoteID).Scan(&fromStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrQuoteNotFound
		}
		return err
	}
	if fromStatus == newStatus {
		return tx.Commit()
	}
	if newStatus == QuoteStatusApproved {
		if _, err := tx.ExecContext(ctx, `
			UPDATE cotizador_quotes
			SET status = $1, approved_at = NOW(), approved_by_uid = NULLIF($2,'')::uuid, updated_at = NOW()
			WHERE id::text = $3
		`, newStatus, strings.TrimSpace(byUID), quoteID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE cotizador_quotes SET status = $1, updated_at = NOW() WHERE id::text = $2
		`, newStatus, quoteID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cotizador_quote_history (quote_id, action, from_status, to_status, by_uid, notes)
		VALUES ($1::uuid, 'status_change', $2, $3, NULLIF($4,'')::uuid, $5)
	`, quoteID, fromStatus, newStatus, strings.TrimSpace(byUID), strings.TrimSpace(notes)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) QuoteHistory(ctx context.Context, quoteID string, limit int) ([]*QuoteHistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, quote_id::text, action, from_status, to_status, by_uid::text, snapshot_json::text, notes, occurred_at
		FROM cotizador_quote_history WHERE quote_id::text = $1 ORDER BY occurred_at DESC LIMIT $2
	`, quoteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*QuoteHistoryEntry
	for rows.Next() {
		var e QuoteHistoryEntry
		if err := rows.Scan(&e.ID, &e.QuoteID, &e.Action, &e.FromStatus, &e.ToStatus, &e.ByUID, &e.SnapshotJSON, &e.Notes, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// === Sections (commit 7) ===

// ListSections retorna las secciones de una quote ordenadas.
func (s *Store) ListSections(ctx context.Context, quoteID string) ([]*QuoteSection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, quote_id::text, section_key, title, content_md, sort_order, created_at, updated_at
		FROM cotizador_quote_sections WHERE quote_id::text = $1 ORDER BY sort_order, created_at
	`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*QuoteSection
	for rows.Next() {
		var sec QuoteSection
		if err := rows.Scan(&sec.ID, &sec.QuoteID, &sec.Key, &sec.Title, &sec.ContentMD, &sec.SortOrder, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &sec)
	}
	return out, rows.Err()
}

// UpsertSection inserta o actualiza una sección por (quote_id, section_key).
func (s *Store) UpsertSection(ctx context.Context, quoteID, key, title, contentMD string, sortOrder int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cotizador_quote_sections (quote_id, section_key, title, content_md, sort_order)
		VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (quote_id, section_key) DO UPDATE
		SET title = EXCLUDED.title, content_md = EXCLUDED.content_md,
		    sort_order = EXCLUDED.sort_order, updated_at = NOW()
	`, quoteID, key, title, contentMD, sortOrder)
	return err
}

func (s *Store) DeleteSection(ctx context.Context, quoteID, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cotizador_quote_sections WHERE quote_id::text = $1 AND section_key = $2`, quoteID, key)
	return err
}

// === Header propuesta ===

type UpdateProposalHeaderParams struct {
	Folio                   string
	ProposalType            string
	ProductName             string
	ProductSubtitle         string
	Tags                    []string
	PreparedForCompany      string
	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string
	IssueDate               *time.Time
	PreparedByName          string
	PreparedByEmail         string
	PreparedByRole          string
}

// UpdateProposalHeader actualiza los campos de cabecera de la propuesta.
// Folio se valida UNIQUE; los strings vacíos quedan como ''.
func (s *Store) UpdateProposalHeader(ctx context.Context, quoteID string, p UpdateProposalHeaderParams) error {
	var folio any
	if strings.TrimSpace(p.Folio) != "" {
		folio = p.Folio
	}
	var issueDate any
	if p.IssueDate != nil {
		issueDate = p.IssueDate.UTC()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cotizador_quotes
		SET folio = $1, proposal_type = $2, product_name = $3, product_subtitle = $4, tags = $5,
		    prepared_for_company = $6, prepared_for_area = $7,
		    prepared_for_contact_name = $8, prepared_for_contact_email = $9,
		    issue_date = $10::date, prepared_by_name = $11, prepared_by_email = $12, prepared_by_role = $13,
		    updated_at = NOW()
		WHERE id::text = $14
	`, folio, strings.TrimSpace(p.ProposalType), p.ProductName, p.ProductSubtitle, pq.Array(p.Tags),
		p.PreparedForCompany, p.PreparedForArea, p.PreparedForContactName, p.PreparedForContactEmail,
		issueDate, p.PreparedByName, p.PreparedByEmail, p.PreparedByRole, quoteID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrQuoteNotFound
	}
	return nil
}

// === scanners ===

func scanRFP(s scanner) (*RFP, error) {
	var r RFP
	if err := s.Scan(&r.ID, &r.LeadID, &r.SourceType, &r.SourceContent, &r.AnalysisJSON,
		&r.CreatedByUID, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanQuote(s scanner) (*Quote, error) {
	var q Quote
	var rfpID sql.NullString
	var tags pq.StringArray
	if err := s.Scan(&q.ID, &q.LeadID, &rfpID, &q.Version, &q.Status, &q.Currency,
		&q.Subtotal, &q.Taxes, &q.Total,
		&q.ValidUntil, &q.Terms, &q.Justification, &q.ApprovedAt, &q.ApprovedByUID,
		&q.CreatedByUID, &q.CreatedByRole, &q.CreatedAt, &q.UpdatedAt,
		&q.Folio, &q.ProposalType, &q.ProductName, &q.ProductSubtitle, &tags,
		&q.PreparedForCompany, &q.PreparedForArea, &q.PreparedForContactName, &q.PreparedForContactEmail,
		&q.IssueDate, &q.PreparedByName, &q.PreparedByEmail, &q.PreparedByRole); err != nil {
		return nil, err
	}
	q.RFPID = rfpID
	q.Tags = []string(tags)
	return &q, nil
}

func roundTo(v float64, decimals int) float64 {
	mul := 1.0
	for i := 0; i < decimals; i++ {
		mul *= 10
	}
	if v >= 0 {
		return float64(int64(v*mul+0.5)) / mul
	}
	return float64(int64(v*mul-0.5)) / mul
}
