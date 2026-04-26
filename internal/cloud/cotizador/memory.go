package cotizador

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	OutcomeWon     = "won"
	OutcomeLost    = "lost"
	OutcomeExpired = "expired"
)

func ValidOutcome(s string) bool {
	return s == OutcomeWon || s == OutcomeLost || s == OutcomeExpired
}

// statusToOutcome mapea un quote status terminal a su outcome canónico.
// approved → won, rejected → lost, expired → expired. Otros → "" (no terminal).
func statusToOutcome(status string) string {
	switch status {
	case QuoteStatusApproved:
		return OutcomeWon
	case QuoteStatusRejected:
		return OutcomeLost
	case QuoteStatusExpired:
		return OutcomeExpired
	default:
		return ""
	}
}

// IsTerminalStatus devuelve true si el status implica "cierre" de la quote
// y por lo tanto requiere lesson + outcome (decisión 3a).
func IsTerminalStatus(status string) bool {
	return statusToOutcome(status) != ""
}

type Outcome struct {
	ID            int64
	QuoteID       string
	Outcome       string
	Reason        string
	RecordedByUID sql.NullString
	OccurredAt    time.Time
}

type Lesson struct {
	ID            string
	QuoteID       sql.NullString
	LeadID        sql.NullString
	Text          string
	Tags          []string
	CreatedByUID  sql.NullString
	CreatedByRole string
	CreatedAt     time.Time
}

type SimilarItemHit struct {
	QuoteID       string
	Version       int
	QuoteStatus   string
	LeadID        string
	LeadName      string
	LeadCompany   string
	ItemID        string
	SKU           string
	Description   string
	Qty           float64
	UnitPrice     float64
	Subtotal      float64
	Currency      string
	QuoteCreated  time.Time
	Rank          float64
}

type OutcomeStats struct {
	Total        int
	Won          int
	Lost         int
	Expired      int
	Open         int
	WinRate      float64 // won / (won + lost) en porcentaje
	AvgWonTotal  float64
	AvgLostTotal float64
}

type ClientHistorySummary struct {
	LeadID     string
	LeadName   string
	Company    string
	QuoteCount int
	WonCount   int
	LostCount  int
	TotalSold  float64
	LastQuoteAt sql.NullTime
}

// === Promote lead to client (commit 6) ===

type PromoteLeadParams struct {
	LeadID         string
	LegalName      string
	RFC            string
	FiscalAddress  string
	BillingEmail   string
	ContactsJSON   string // jsonb: array de contactos [{name, role, email, phone}, ...]
	Notes          string
	CreatedByUID   string
}

// PromoteLeadToClient crea entry en cotizador_clients y vincula con el lead origen.
// Setea el client_id en el lead para tracking inverso.
func (s *Store) PromoteLeadToClient(ctx context.Context, p PromoteLeadParams) (*Client, error) {
	if strings.TrimSpace(p.LeadID) == "" {
		return nil, fmt.Errorf("lead_id is required")
	}
	if strings.TrimSpace(p.LegalName) == "" {
		return nil, fmt.Errorf("legal_name is required")
	}
	contacts := strings.TrimSpace(p.ContactsJSON)
	if contacts == "" {
		contacts = "[]"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		INSERT INTO cotizador_clients (lead_id, legal_name, rfc, fiscal_address, billing_email, contacts_json, notes, created_by_uid)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::jsonb, $7, NULLIF($8,'')::uuid)
		RETURNING id::text, lead_id::text, legal_name, rfc, fiscal_address, billing_email,
		          contacts_json::text, notes, created_by_uid::text, created_at, updated_at
	`, p.LeadID, p.LegalName, p.RFC, p.FiscalAddress, p.BillingEmail, contacts, p.Notes, strings.TrimSpace(p.CreatedByUID))
	c, err := scanClient(row)
	if err != nil {
		return nil, fmt.Errorf("insert client: %w", err)
	}
	// Linkear lead al cliente
	if _, err := tx.ExecContext(ctx, `
		UPDATE cotizador_leads SET client_id = $1::uuid, updated_at = NOW() WHERE id::text = $2
	`, c.ID, p.LeadID); err != nil {
		return nil, fmt.Errorf("link lead to client: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) GetClient(ctx context.Context, id string) (*Client, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, lead_id::text, legal_name, rfc, fiscal_address, billing_email,
		       contacts_json::text, notes, created_by_uid::text, created_at, updated_at
		FROM cotizador_clients WHERE id::text = $1
	`, id)
	c, err := scanClient(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrClientNotFound
		}
		return nil, err
	}
	return c, nil
}

func (s *Store) ListClients(ctx context.Context) ([]*Client, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, lead_id::text, legal_name, rfc, fiscal_address, billing_email,
		       contacts_json::text, notes, created_by_uid::text, created_at, updated_at
		FROM cotizador_clients ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Client
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanClient(s scanner) (*Client, error) {
	var c Client
	var leadID, byUID sql.NullString
	if err := s.Scan(&c.ID, &leadID, &c.LegalName, &c.RFC, &c.FiscalAddress, &c.BillingEmail,
		&c.ContactsJSON, &c.Notes, &byUID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.LeadID = leadID
	c.CreatedByUID = byUID
	return &c, nil
}

// === Outcomes ===

// CloseQuoteResult contiene el resultado de cerrar una quote, incluyendo
// info del proyecto auto-creado si fue approval.
type CloseQuoteResult struct {
	ProjectCreated bool
	ProjectName    string
}

// CloseQuoteWithOutcome marca la quote en status terminal + inserta outcome
// + lesson opcional + (si approved) auto-crea proyecto en cloud_project_controls.
// Decisión 4a: aprobación → conversión automática a proyecto.
func (s *Store) CloseQuoteWithOutcome(ctx context.Context, quoteID, newStatus, byUID, reason, lessonText string, lessonTags []string) error {
	if !IsTerminalStatus(newStatus) {
		return fmt.Errorf("status %q is not terminal (must be approved|rejected|expired)", newStatus)
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
	// Update status
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
	// History
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cotizador_quote_history (quote_id, action, from_status, to_status, by_uid, notes)
		VALUES ($1::uuid, 'closed', $2, $3, NULLIF($4,'')::uuid, $5)
	`, quoteID, fromStatus, newStatus, strings.TrimSpace(byUID), reason); err != nil {
		return err
	}
	// Outcome
	outcome := statusToOutcome(newStatus)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cotizador_quote_outcomes (quote_id, outcome, reason, recorded_by_uid)
		VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid)
	`, quoteID, outcome, reason, strings.TrimSpace(byUID)); err != nil {
		return err
	}
	// Necesitamos lead_id para lesson y auto-conversión.
	var leadID string
	if err := tx.QueryRowContext(ctx, `SELECT lead_id::text FROM cotizador_quotes WHERE id::text = $1`, quoteID).Scan(&leadID); err != nil {
		return err
	}
	// Lesson opcional asociada
	lessonText = strings.TrimSpace(lessonText)
	if lessonText != "" {
		role := "cotizador"
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cotizador_lessons (quote_id, lead_id, text, tags, created_by_uid, created_by_role)
			VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5,'')::uuid, $6)
		`, quoteID, leadID, lessonText, pq.Array(lessonTags), strings.TrimSpace(byUID), role); err != nil {
			return fmt.Errorf("insert lesson: %w", err)
		}
	}
	// Auto-conversión a proyecto cuando outcome=won (decisión 4a).
	if outcome == OutcomeWon {
		// Marcar lead como won
		if _, err := tx.ExecContext(ctx, `
			UPDATE cotizador_leads SET status = 'won', updated_at = NOW() WHERE id::text = $1 AND status <> 'won'
		`, leadID); err != nil {
			return fmt.Errorf("update lead status to won: %w", err)
		}
		// Audit log lead history
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cotizador_lead_history (lead_id, action, to_status, by_uid, notes)
			VALUES ($1::uuid, 'auto_won_from_quote', 'won', NULLIF($2,'')::uuid, $3)
		`, leadID, strings.TrimSpace(byUID), fmt.Sprintf("auto-promoted from quote %s approval", quoteID)); err != nil {
			return fmt.Errorf("insert lead history: %w", err)
		}
		// Crear proyecto en cloud_project_controls. Nombre derivado del lead.
		var leadName, leadCompany string
		if err := tx.QueryRowContext(ctx, `SELECT name, company FROM cotizador_leads WHERE id::text = $1`, leadID).Scan(&leadName, &leadCompany); err != nil {
			return err
		}
		projectName := projectNameFromLead(leadName, leadCompany, quoteID)
		// Insert idempotente: si ya existe (por re-aprobación), no falla.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cloud_project_controls (project, sync_enabled, updated_by)
			VALUES ($1, TRUE, NULLIF($2,'')::text)
			ON CONFLICT (project) DO NOTHING
		`, projectName, strings.TrimSpace(byUID)); err != nil {
			return fmt.Errorf("create project: %w", err)
		}
	}
	return tx.Commit()
}

// projectNameFromLead deriva un nombre de proyecto válido y único del lead.
// Formato: "<company>-<short-quote-id>" sanitizado a [a-z0-9-].
func projectNameFromLead(leadName, leadCompany, quoteID string) string {
	base := strings.TrimSpace(leadCompany)
	if base == "" {
		base = strings.TrimSpace(leadName)
	}
	if base == "" {
		base = "lead"
	}
	base = strings.ToLower(base)
	out := make([]rune, 0, len(base))
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			out = append(out, '-')
		}
	}
	clean := strings.Trim(string(out), "-")
	if len(clean) > 32 {
		clean = clean[:32]
	}
	short := strings.ReplaceAll(quoteID, "-", "")
	if len(short) > 8 {
		short = short[:8]
	}
	return clean + "-" + short
}

// === Lessons ===

type CreateLessonParams struct {
	QuoteID      string // opcional
	LeadID       string // opcional
	Text         string
	Tags         []string
	CreatedByUID string
	Role         string
}

func (s *Store) CreateLesson(ctx context.Context, p CreateLessonParams) (*Lesson, error) {
	if strings.TrimSpace(p.Text) == "" {
		return nil, fmt.Errorf("lesson text is required")
	}
	role := strings.TrimSpace(p.Role)
	if role == "" {
		role = "cotizador"
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO cotizador_lessons (quote_id, lead_id, text, tags, created_by_uid, created_by_role)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, $3, $4, NULLIF($5,'')::uuid, $6)
		RETURNING id::text, quote_id::text, lead_id::text, text, tags, created_by_uid::text, created_by_role, created_at
	`, strings.TrimSpace(p.QuoteID), strings.TrimSpace(p.LeadID), strings.TrimSpace(p.Text),
		pq.Array(p.Tags), strings.TrimSpace(p.CreatedByUID), role)
	return scanLesson(row)
}

// SearchLessons full-text en el texto + filtro opcional por tag.
func (s *Store) SearchLessons(ctx context.Context, query, tag string, limit int) ([]*Lesson, error) {
	if limit <= 0 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	tag = strings.TrimSpace(tag)
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case query == "" && tag == "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, quote_id::text, lead_id::text, text, tags, created_by_uid::text, created_by_role, created_at
			FROM cotizador_lessons ORDER BY created_at DESC LIMIT $1
		`, limit)
	case query != "" && tag == "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, quote_id::text, lead_id::text, text, tags, created_by_uid::text, created_by_role, created_at
			FROM cotizador_lessons
			WHERE text_tsv @@ plainto_tsquery('simple', $1)
			ORDER BY ts_rank(text_tsv, plainto_tsquery('simple', $1)) DESC, created_at DESC
			LIMIT $2
		`, query, limit)
	case query == "" && tag != "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, quote_id::text, lead_id::text, text, tags, created_by_uid::text, created_by_role, created_at
			FROM cotizador_lessons WHERE $1 = ANY(tags) ORDER BY created_at DESC LIMIT $2
		`, tag, limit)
	default:
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, quote_id::text, lead_id::text, text, tags, created_by_uid::text, created_by_role, created_at
			FROM cotizador_lessons
			WHERE text_tsv @@ plainto_tsquery('simple', $1) AND $2 = ANY(tags)
			ORDER BY ts_rank(text_tsv, plainto_tsquery('simple', $1)) DESC, created_at DESC
			LIMIT $3
		`, query, tag, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lesson
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// === Memoria histórica de items: búsqueda similar ===

func (s *Store) SearchSimilarItems(ctx context.Context, query string, limit int) ([]*SimilarItemHit, error) {
	if limit <= 0 {
		limit = 10
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			q.id::text as quote_id, q.version, q.status,
			q.lead_id::text, l.name, l.company,
			i.id::text as item_id, i.sku, i.description, i.qty::float8, i.unit_price::float8, i.subtotal::float8,
			q.currency, q.created_at,
			ts_rank(i.description_tsv, plainto_tsquery('simple', $1)) as rank
		FROM cotizador_quote_items i
		JOIN cotizador_quotes q ON q.id = i.quote_id
		JOIN cotizador_leads l ON l.id = q.lead_id
		WHERE i.description_tsv @@ plainto_tsquery('simple', $1)
		ORDER BY rank DESC, q.created_at DESC
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SimilarItemHit
	for rows.Next() {
		var h SimilarItemHit
		if err := rows.Scan(&h.QuoteID, &h.Version, &h.QuoteStatus, &h.LeadID, &h.LeadName, &h.LeadCompany,
			&h.ItemID, &h.SKU, &h.Description, &h.Qty, &h.UnitPrice, &h.Subtotal, &h.Currency, &h.QuoteCreated, &h.Rank); err != nil {
			return nil, err
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}

// === Outcome stats ===

func (s *Store) GetOutcomeStats(ctx context.Context) (*OutcomeStats, error) {
	var st OutcomeStats
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, count(*), COALESCE(AVG(total::float8), 0)
		FROM cotizador_quotes GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		var avgTotal float64
		if err := rows.Scan(&status, &count, &avgTotal); err != nil {
			return nil, err
		}
		st.Total += count
		switch status {
		case QuoteStatusApproved:
			st.Won = count
			st.AvgWonTotal = avgTotal
		case QuoteStatusRejected:
			st.Lost = count
			st.AvgLostTotal = avgTotal
		case QuoteStatusExpired:
			st.Expired = count
		default:
			st.Open += count
		}
	}
	if st.Won+st.Lost > 0 {
		st.WinRate = float64(st.Won) / float64(st.Won+st.Lost) * 100
	}
	return &st, rows.Err()
}

// === Client history ===

func (s *Store) GetClientHistory(ctx context.Context, query string) ([]*ClientHistorySummary, error) {
	q := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			l.id::text, l.name, l.company,
			COUNT(q.id) FILTER (WHERE q.id IS NOT NULL) as quote_count,
			COUNT(q.id) FILTER (WHERE q.status = 'approved') as won_count,
			COUNT(q.id) FILTER (WHERE q.status = 'rejected') as lost_count,
			COALESCE(SUM(q.total::float8) FILTER (WHERE q.status = 'approved'), 0) as total_sold,
			MAX(q.created_at) as last_quote_at
		FROM cotizador_leads l
		LEFT JOIN cotizador_quotes q ON q.lead_id = l.id
		WHERE LOWER(l.name) LIKE $1 OR LOWER(l.company) LIKE $1
		GROUP BY l.id, l.name, l.company
		ORDER BY last_quote_at DESC NULLS LAST
		LIMIT 20
	`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ClientHistorySummary
	for rows.Next() {
		var c ClientHistorySummary
		if err := rows.Scan(&c.LeadID, &c.LeadName, &c.Company, &c.QuoteCount, &c.WonCount, &c.LostCount, &c.TotalSold, &c.LastQuoteAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// === scanners ===

func scanLesson(s scanner) (*Lesson, error) {
	var l Lesson
	var quoteID, leadID, byUID sql.NullString
	var tags pq.StringArray
	if err := s.Scan(&l.ID, &quoteID, &leadID, &l.Text, &tags, &byUID, &l.CreatedByRole, &l.CreatedAt); err != nil {
		return nil, err
	}
	l.QuoteID = quoteID
	l.LeadID = leadID
	l.CreatedByUID = byUID
	l.Tags = []string(tags)
	return &l, nil
}
