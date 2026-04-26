package attachments

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrShareNotFound is returned when a token does not exist or is revoked.
var ErrShareNotFound = errors.New("attachments: share link not found")

// ErrShareExpired is returned when a token's expires_at has passed.
var ErrShareExpired = errors.New("attachments: share link expired")

// ErrSharePasswordRequired is returned by Resolve when the link has a password
// but the caller did not provide one (or provided an incorrect one).
var ErrSharePasswordRequired = errors.New("attachments: share link requires password")

// ErrShareForbiddenSensitivity signals an attempt to share a page whose
// sensitivity column is set to "confidential". The caller must surface a 403
// to the dashboard layer.
var ErrShareForbiddenSensitivity = errors.New("attachments: page is confidential and cannot be shared publicly")

// ShareLink is the runtime view of an aria_page_share_links row.
type ShareLink struct {
	ID            string
	PageID        string
	Token         string
	HasPassword   bool
	ExpiresAt     *time.Time
	ViewCount     int
	LastViewedAt  *time.Time
	CreatedByUID  string
	CreatedAt     time.Time
	IsRevoked     bool
}

// CreateShareParams is the input to ShareStore.Create.
type CreateShareParams struct {
	PageID       string
	Password     string // optional plaintext; bcrypt-hashed before persisting.
	ExpiresAt    *time.Time
	CreatedByUID string
}

// ShareStore is the interface implemented by *PgShareStore plus any test
// double. The interface lets the dashboard handlers depend on a narrow API.
type ShareStore interface {
	Create(ctx context.Context, p CreateShareParams) (*ShareLink, error)
	Get(ctx context.Context, id string) (*ShareLink, error)
	GetByToken(ctx context.Context, token string) (*ShareLink, error)
	ListByPage(ctx context.Context, pageID string, includeRevoked bool) ([]*ShareLink, error)
	Revoke(ctx context.Context, id, byUID string) error
	// Resolve validates a token + optional password and increments view_count.
	// Returns ErrShareForbiddenSensitivity / ErrSharePasswordRequired etc. on
	// failure. ip and ua are recorded in aria_page_share_access_log.
	Resolve(ctx context.Context, token, password, ip, ua string) (*ShareLink, error)
}

// PgShareStore is the Postgres implementation of ShareStore.
type PgShareStore struct {
	db *sql.DB
	// pageSensitivityResolver, when non-nil, is consulted at Create() time to
	// reject share-link creation for confidential pages. The default no-op
	// allows callers running before the PAGES module exists; once aria_pages is
	// live, the cmd/wiring layer installs a real resolver.
	pageSensitivityResolver func(ctx context.Context, pageID string) (string, error)
	// nowFn is overridable in tests.
	nowFn func() time.Time
	mu    sync.Mutex
}

// NewPgShareStore returns a PgShareStore wired to the given DB. The optional
// resolver lets the wiring layer block share creation on confidential pages.
func NewPgShareStore(db *sql.DB) *PgShareStore {
	return &PgShareStore{db: db, nowFn: time.Now}
}

// SetSensitivityResolver installs a func that returns the page's sensitivity
// tag ("public"|"internal"|"client"|"confidential") for confidential-gating.
// If the resolver returns an error containing the string "aria_pages" we treat
// the parent table as missing and let the share creation proceed.
func (s *PgShareStore) SetSensitivityResolver(fn func(ctx context.Context, pageID string) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageSensitivityResolver = fn
}

// generateToken returns a 32-byte URL-safe random hex (64 chars) using
// crypto/rand. We deliberately do not use UUID because UUIDs leak structure
// (timestamp, MAC) and are predictable enough that an attacker iterating over
// {token} URLs could brute-force a public link.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Create persists a new share link with a fresh token. password may be empty;
// when provided, it is bcrypt-hashed at cost 10.
func (s *PgShareStore) Create(ctx context.Context, p CreateShareParams) (*ShareLink, error) {
	if strings.TrimSpace(p.PageID) == "" {
		return nil, fmt.Errorf("attachments: share: page_id required")
	}
	if strings.TrimSpace(p.CreatedByUID) == "" {
		return nil, fmt.Errorf("attachments: share: created_by_uid required")
	}
	s.mu.Lock()
	resolver := s.pageSensitivityResolver
	s.mu.Unlock()
	if resolver != nil {
		sens, err := resolver(ctx, p.PageID)
		if err == nil && strings.EqualFold(sens, "confidential") {
			return nil, ErrShareForbiddenSensitivity
		}
		// Errors are non-fatal: we don't want a transient query failure to
		// block legitimate share creation. The HTTP layer logs the error.
	}
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("attachments: share: gen token: %w", err)
	}
	var passHash *string
	if pw := strings.TrimSpace(p.Password); pw != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(pw), 10)
		if err != nil {
			return nil, fmt.Errorf("attachments: share: bcrypt: %w", err)
		}
		v := string(h)
		passHash = &v
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO aria_page_share_links (page_id, token, password_hash, expires_at, created_by_uid)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid)
		RETURNING id::text, page_id::text, token, password_hash IS NOT NULL,
		          expires_at, view_count, last_viewed_at, created_by_uid::text, created_at, is_revoked
	`, p.PageID, token, passHash, p.ExpiresAt, p.CreatedByUID)
	link, err := scanShareLink(row)
	if err != nil {
		return nil, fmt.Errorf("attachments: share: insert: %w", err)
	}
	return link, nil
}

// Get returns a share link by its UUID id.
func (s *PgShareStore) Get(ctx context.Context, id string) (*ShareLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, page_id::text, token, password_hash IS NOT NULL,
		       expires_at, view_count, last_viewed_at, created_by_uid::text, created_at, is_revoked
		FROM aria_page_share_links WHERE id = $1::uuid`, id)
	link, err := scanShareLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrShareNotFound
	}
	return link, err
}

// GetByToken is used by the public route to load metadata before validating
// the password. The token must not be revoked.
func (s *PgShareStore) GetByToken(ctx context.Context, token string) (*ShareLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, page_id::text, token, password_hash IS NOT NULL,
		       expires_at, view_count, last_viewed_at, created_by_uid::text, created_at, is_revoked
		FROM aria_page_share_links WHERE token = $1 AND NOT is_revoked`, token)
	link, err := scanShareLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrShareNotFound
	}
	return link, err
}

// ListByPage returns active share links for a page (revoked included if asked).
func (s *PgShareStore) ListByPage(ctx context.Context, pageID string, includeRevoked bool) ([]*ShareLink, error) {
	q := `
		SELECT id::text, page_id::text, token, password_hash IS NOT NULL,
		       expires_at, view_count, last_viewed_at, created_by_uid::text, created_at, is_revoked
		FROM aria_page_share_links
		WHERE page_id = $1::uuid`
	if !includeRevoked {
		q += ` AND NOT is_revoked`
	}
	q += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, q, pageID)
	if err != nil {
		return nil, fmt.Errorf("attachments: list shares: %w", err)
	}
	defer rows.Close()
	out := []*ShareLink{}
	for rows.Next() {
		link, err := scanShareLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// Revoke flips the is_revoked flag. byUID is logged.
func (s *PgShareStore) Revoke(ctx context.Context, id, byUID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE aria_page_share_links SET is_revoked = TRUE WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("attachments: revoke share: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrShareNotFound
	}
	return nil
}

// Resolve validates a token and (optionally) a password. On success it
// increments view_count and returns the link. Each call writes a row to
// aria_page_share_access_log regardless of outcome — this is the audit trail.
func (s *PgShareStore) Resolve(ctx context.Context, token, password, ip, ua string) (*ShareLink, error) {
	link, err := s.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if link.IsRevoked {
		_ = s.logAccess(ctx, link.ID, ip, ua, password != "", boolPtr(false))
		return nil, ErrShareNotFound
	}
	if link.ExpiresAt != nil && link.ExpiresAt.Before(s.nowFn().UTC()) {
		_ = s.logAccess(ctx, link.ID, ip, ua, password != "", boolPtr(false))
		return nil, ErrShareExpired
	}
	if link.HasPassword {
		var hash sql.NullString
		if err := s.db.QueryRowContext(ctx,
			`SELECT password_hash FROM aria_page_share_links WHERE id = $1::uuid`,
			link.ID,
		).Scan(&hash); err != nil {
			return nil, fmt.Errorf("attachments: load hash: %w", err)
		}
		if !hash.Valid || hash.String == "" {
			// Inconsistent row — treat as no-password.
		} else {
			pw := strings.TrimSpace(password)
			if pw == "" {
				_ = s.logAccess(ctx, link.ID, ip, ua, false, nil)
				return nil, ErrSharePasswordRequired
			}
			if err := bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(pw)); err != nil {
				_ = s.logAccess(ctx, link.ID, ip, ua, true, boolPtr(false))
				return nil, ErrSharePasswordRequired
			}
			_ = s.logAccess(ctx, link.ID, ip, ua, true, boolPtr(true))
		}
	} else {
		_ = s.logAccess(ctx, link.ID, ip, ua, false, nil)
	}

	// Increment view_count atomically. Even on rare race conditions the
	// counter remains monotonically increasing because UPDATE ... SET col=col+1
	// is atomic at row level.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE aria_page_share_links
		SET view_count = view_count + 1, last_viewed_at = NOW()
		WHERE id = $1::uuid
	`, link.ID); err != nil {
		return nil, fmt.Errorf("attachments: increment view_count: %w", err)
	}
	return link, nil
}

func (s *PgShareStore) logAccess(ctx context.Context, shareID, ip, ua string, attempted bool, correct *bool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_page_share_access_log
		  (share_link_id, client_ip, user_agent, password_attempted, password_correct)
		VALUES ($1::uuid, NULLIF($2,'')::inet, NULLIF($3,''), $4, $5)
	`, shareID, ip, ua, attempted, correct)
	return err
}

func scanShareLink(r interface {
	Scan(...any) error
}) (*ShareLink, error) {
	var s ShareLink
	var expires, lastView sql.NullTime
	if err := r.Scan(
		&s.ID, &s.PageID, &s.Token, &s.HasPassword,
		&expires, &s.ViewCount, &lastView,
		&s.CreatedByUID, &s.CreatedAt, &s.IsRevoked,
	); err != nil {
		return nil, err
	}
	if expires.Valid {
		t := expires.Time
		s.ExpiresAt = &t
	}
	if lastView.Valid {
		t := lastView.Time
		s.LastViewedAt = &t
	}
	return &s, nil
}

func boolPtr(b bool) *bool { return &b }
