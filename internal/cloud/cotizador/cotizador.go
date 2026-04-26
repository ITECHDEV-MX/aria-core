// Package cotizador implementa el módulo de cotizaciones de aria-core.
// Funnel: Lead (new → contacted → qualified → quoting → won/lost)
//          Client (post-won, datos fiscales completos)
//          Quote, RFP (commits siguientes)
package cotizador

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusNew        = "new"
	StatusContacted  = "contacted"
	StatusQualified  = "qualified"
	StatusQuoting    = "quoting"
	StatusWon        = "won"
	StatusLost       = "lost"
)

var leadStatuses = []string{StatusNew, StatusContacted, StatusQualified, StatusQuoting, StatusWon, StatusLost}

func ValidLeadStatus(s string) bool {
	for _, v := range leadStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// LeadStatusLabel retorna display name para UI.
func LeadStatusLabel(s string) string {
	switch s {
	case StatusNew:
		return "Nuevo"
	case StatusContacted:
		return "Contactado"
	case StatusQualified:
		return "Calificado"
	case StatusQuoting:
		return "Cotizando"
	case StatusWon:
		return "Ganado"
	case StatusLost:
		return "Perdido"
	default:
		return s
	}
}

// AllLeadStatuses retorna la lista canónica para selects de UI.
func AllLeadStatuses() []string {
	out := make([]string, len(leadStatuses))
	copy(out, leadStatuses)
	return out
}

var (
	ErrLeadNotFound      = errors.New("lead not found")
	ErrInvalidLeadStatus = errors.New("invalid lead status")
	ErrClientNotFound    = errors.New("client not found")
)

type Lead struct {
	ID            string
	Name          string
	Company       string
	Email         string
	Phone         string
	Source        string
	Status        string
	Notes         string
	OwnerUID      sql.NullString
	CreatedByUID  sql.NullString
	CreatedByRole string
	ClientID      sql.NullString
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type LeadHistoryEntry struct {
	ID         int64
	LeadID     string
	Action     string
	FromStatus sql.NullString
	ToStatus   sql.NullString
	ByUID      sql.NullString
	Notes      string
	OccurredAt time.Time
}

type Client struct {
	ID             string
	LeadID         sql.NullString
	LegalName      string
	RFC            string
	FiscalAddress  string
	BillingEmail   string
	ContactsJSON   string
	Notes          string
	CreatedByUID   sql.NullString
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateLeadParams agrupa los inputs de creación.
type CreateLeadParams struct {
	Name         string
	Company      string
	Email        string
	Phone        string
	Source       string
	Notes        string
	OwnerUID     string // si "", queda NULL
	CreatedByUID string // user que creó (de session JWT); si "", queda NULL
	Role         string // role activo del que creó (para created_by_role; si "", default 'cotizador')
}

func (s *Store) CreateLead(ctx context.Context, p CreateLeadParams) (*Lead, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("lead name is required")
	}
	role := strings.TrimSpace(p.Role)
	if role == "" {
		role = "cotizador"
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO cotizador_leads (name, company, email, phone, source, notes, owner_uid, created_by_uid, created_by_role)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,'')::uuid, NULLIF($8,'')::uuid, $9)
		RETURNING id::text, name, company, email, phone, source, status, notes,
		          owner_uid::text, created_by_uid::text, created_by_role,
		          client_id::text, created_at, updated_at
	`,
		name, strings.TrimSpace(p.Company), strings.TrimSpace(p.Email), strings.TrimSpace(p.Phone),
		strings.TrimSpace(p.Source), strings.TrimSpace(p.Notes),
		strings.TrimSpace(p.OwnerUID), strings.TrimSpace(p.CreatedByUID), role,
	)
	lead, err := scanLead(row)
	if err != nil {
		return nil, fmt.Errorf("insert lead: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO cotizador_lead_history (lead_id, action, to_status, by_uid)
		VALUES ($1::uuid, 'created', 'new', NULLIF($2,'')::uuid)
	`, lead.ID, strings.TrimSpace(p.CreatedByUID)); err != nil {
		return nil, fmt.Errorf("insert history: %w", err)
	}
	return lead, nil
}

func (s *Store) GetLead(ctx context.Context, id string) (*Lead, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, name, company, email, phone, source, status, notes,
		       owner_uid::text, created_by_uid::text, created_by_role,
		       client_id::text, created_at, updated_at
		FROM cotizador_leads WHERE id::text = $1 LIMIT 1
	`, id)
	lead, err := scanLead(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLeadNotFound
		}
		return nil, err
	}
	return lead, nil
}

// ListLeads filtra por status (vacío = todos), ordenado por created_at DESC.
func (s *Store) ListLeads(ctx context.Context, status string) ([]*Lead, error) {
	var (
		rows *sql.Rows
		err  error
	)
	status = strings.TrimSpace(status)
	if status == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, name, company, email, phone, source, status, notes,
			       owner_uid::text, created_by_uid::text, created_by_role,
			       client_id::text, created_at, updated_at
			FROM cotizador_leads ORDER BY created_at DESC
		`)
	} else {
		if !ValidLeadStatus(status) {
			return nil, ErrInvalidLeadStatus
		}
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, name, company, email, phone, source, status, notes,
			       owner_uid::text, created_by_uid::text, created_by_role,
			       client_id::text, created_at, updated_at
			FROM cotizador_leads WHERE status = $1 ORDER BY created_at DESC
		`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lead
	for rows.Next() {
		lead, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lead)
	}
	return out, rows.Err()
}

// UpdateLeadStatus cambia el estado y registra audit log.
func (s *Store) UpdateLeadStatus(ctx context.Context, leadID, newStatus, byUID, notes string) error {
	if !ValidLeadStatus(newStatus) {
		return ErrInvalidLeadStatus
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var fromStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM cotizador_leads WHERE id::text = $1`, leadID).Scan(&fromStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeadNotFound
		}
		return err
	}
	if fromStatus == newStatus {
		return tx.Commit() // no-op
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cotizador_leads SET status = $1, updated_at = NOW() WHERE id::text = $2`, newStatus, leadID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cotizador_lead_history (lead_id, action, from_status, to_status, by_uid, notes)
		VALUES ($1::uuid, 'status_change', $2, $3, NULLIF($4,'')::uuid, $5)
	`, leadID, fromStatus, newStatus, strings.TrimSpace(byUID), strings.TrimSpace(notes)); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateLead actualiza campos editables (no status — usar UpdateLeadStatus).
func (s *Store) UpdateLead(ctx context.Context, id, name, company, email, phone, source, notes, ownerUID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cotizador_leads
		SET name = $1, company = $2, email = $3, phone = $4, source = $5, notes = $6,
		    owner_uid = NULLIF($7,'')::uuid, updated_at = NOW()
		WHERE id::text = $8
	`,
		strings.TrimSpace(name), strings.TrimSpace(company), strings.TrimSpace(email),
		strings.TrimSpace(phone), strings.TrimSpace(source), strings.TrimSpace(notes),
		strings.TrimSpace(ownerUID), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLeadNotFound
	}
	return nil
}

// LeadHistory retorna las últimas N entradas del audit log de un lead.
func (s *Store) LeadHistory(ctx context.Context, leadID string, limit int) ([]*LeadHistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, lead_id::text, action, from_status, to_status, by_uid::text, notes, occurred_at
		FROM cotizador_lead_history
		WHERE lead_id::text = $1
		ORDER BY occurred_at DESC
		LIMIT $2
	`, leadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LeadHistoryEntry
	for rows.Next() {
		var e LeadHistoryEntry
		if err := rows.Scan(&e.ID, &e.LeadID, &e.Action, &e.FromStatus, &e.ToStatus, &e.ByUID, &e.Notes, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// CountByStatus retorna {status: count} de todos los leads. Útil para dashboards.
func (s *Store) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, count(*) FROM cotizador_leads GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLead(s scanner) (*Lead, error) {
	var l Lead
	var ownerUID, createdByUID, clientID sql.NullString
	if err := s.Scan(&l.ID, &l.Name, &l.Company, &l.Email, &l.Phone, &l.Source, &l.Status, &l.Notes,
		&ownerUID, &createdByUID, &l.CreatedByRole, &clientID, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	l.OwnerUID = ownerUID
	l.CreatedByUID = createdByUID
	l.ClientID = clientID
	return &l, nil
}
