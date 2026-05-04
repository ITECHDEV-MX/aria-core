// Package skillmaintainers provides CRUD for the aria_skill_maintainers
// Postgres table introduced in v0.14.0 (PR2 backlog item #3).
//
// A skill maintainer is the person designated to review and approve
// SKILL.md changes for a given expertise domain. PR review is enforced
// at the GitHub level via .github/CODEOWNERS; this table records the
// canonical authority and lets the cloud server cross-reference reviews
// against an audit log in the future.
package skillmaintainers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Maintainer mirrors one aria_skill_maintainers row.
type Maintainer struct {
	ID               string
	Email            string
	GitHubUsername   string
	ExpertiseDomains []string
	AddedAt          time.Time
	AddedByUID       string
	RevokedAt        *time.Time // nil when active
}

// IsActive reports whether the maintainer has not been revoked.
func (m Maintainer) IsActive() bool { return m.RevokedAt == nil }

// Store wraps the *sql.DB with the queries this package needs.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ErrNotFound is returned when a maintainer email is missing.
var ErrNotFound = errors.New("skillmaintainers: not found")

// AddParams is the input for Add.
type AddParams struct {
	Email            string
	GitHubUsername   string
	ExpertiseDomains []string
	AddedByUID       string
}

// Add inserts a new maintainer or, if the email already exists, refreshes
// expertise_domains/github_username and unsets revoked_at. Idempotent
// admin command.
func (s *Store) Add(ctx context.Context, p AddParams) (*Maintainer, error) {
	email := strings.TrimSpace(strings.ToLower(p.Email))
	if email == "" {
		return nil, errors.New("email required")
	}
	if !strings.Contains(email, "@") {
		return nil, fmt.Errorf("not a valid email: %q", email)
	}
	if len(p.ExpertiseDomains) == 0 {
		p.ExpertiseDomains = []string{}
	}

	q := `
INSERT INTO aria_skill_maintainers (email, github_username, expertise_domains, added_by_uid)
VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, '')::uuid)
ON CONFLICT (email) DO UPDATE SET
    github_username   = EXCLUDED.github_username,
    expertise_domains = EXCLUDED.expertise_domains,
    revoked_at        = NULL
RETURNING id, email, COALESCE(github_username, ''),
          expertise_domains, added_at,
          COALESCE(added_by_uid::text, ''),
          revoked_at
`
	row := s.db.QueryRowContext(ctx, q,
		email, p.GitHubUsername, pq.Array(p.ExpertiseDomains), p.AddedByUID)

	var m Maintainer
	var domains pq.StringArray
	if err := row.Scan(
		&m.ID, &m.Email, &m.GitHubUsername,
		&domains, &m.AddedAt, &m.AddedByUID, &m.RevokedAt,
	); err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	m.ExpertiseDomains = []string(domains)
	return &m, nil
}

// List returns maintainers. activeOnly=true filters out revoked entries.
// Sorted alphabetically by email.
func (s *Store) List(ctx context.Context, activeOnly bool) ([]Maintainer, error) {
	q := `
SELECT id, email, COALESCE(github_username, ''),
       expertise_domains, added_at,
       COALESCE(added_by_uid::text, ''),
       revoked_at
FROM aria_skill_maintainers
`
	if activeOnly {
		q += "WHERE revoked_at IS NULL\n"
	}
	q += "ORDER BY email"

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []Maintainer
	for rows.Next() {
		var m Maintainer
		var domains pq.StringArray
		if err := rows.Scan(
			&m.ID, &m.Email, &m.GitHubUsername,
			&domains, &m.AddedAt, &m.AddedByUID, &m.RevokedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		m.ExpertiseDomains = []string(domains)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate: %w", err)
	}
	return out, nil
}

// Revoke sets revoked_at = NOW() for the given email. Idempotent —
// re-revoking is a no-op. Returns ErrNotFound if email never existed.
func (s *Store) Revoke(ctx context.Context, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return errors.New("email required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE aria_skill_maintainers
		 SET revoked_at = NOW()
		 WHERE email = $1 AND revoked_at IS NULL`,
		email)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		// Either email doesn't exist or already revoked. Distinguish.
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM aria_skill_maintainers WHERE email = $1)`,
			email).Scan(&exists); err != nil {
			return fmt.Errorf("existence check: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
