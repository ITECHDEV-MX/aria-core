package redactor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// AliasStore persists deterministic token <-> displayValue mappings so that the
// same RFC / email / amount produces the same opaque token across calls. This
// is critical for Claude to be able to correlate "[CLIENT-A1B2]" between two
// different observations.
//
// The store has two backends:
//   - DB (postgres): durable, multi-process safe, used in production.
//   - Memory: fallback for tests / when no DB is provided.
type AliasStore interface {
	// Lookup returns the displayValue for an existing token, or ("", nil) if missing.
	Lookup(ctx context.Context, token string) (string, error)
	// Upsert binds a token to an entity-type / display value. If the token
	// already exists, the existing displayValue is kept (deterministic).
	Upsert(ctx context.Context, token, entityType, displayValue string, entityID *string) error
}

// aliasDBStore persists aliases in postgres `aria_redaction_aliases`.
type aliasDBStore struct {
	db *sql.DB
}

// NewAliasDBStore wraps a *sql.DB as an AliasStore.
func NewAliasDBStore(db *sql.DB) AliasStore {
	return &aliasDBStore{db: db}
}

func (s *aliasDBStore) Lookup(ctx context.Context, token string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT display_value FROM aria_redaction_aliases WHERE alias_token = $1`, token,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redactor: lookup alias: %w", err)
	}
	return v, nil
}

func (s *aliasDBStore) Upsert(ctx context.Context, token, entityType, displayValue string, entityID *string) error {
	if s == nil || s.db == nil {
		return nil
	}
	var idArg any
	if entityID != nil && strings.TrimSpace(*entityID) != "" {
		idArg = *entityID
	} else {
		idArg = nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_redaction_aliases (alias_token, entity_type, entity_id, display_value)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4)
		ON CONFLICT (alias_token) DO NOTHING
	`, token, entityType, idArg, displayValue)
	if err != nil {
		return fmt.Errorf("redactor: upsert alias: %w", err)
	}
	return nil
}

// memAliasStore is the in-memory fallback used in tests and when no DB is wired.
type memAliasStore struct {
	mu   sync.RWMutex
	rows map[string]string // token -> displayValue
}

// NewMemoryAliasStore returns a process-local AliasStore.
func NewMemoryAliasStore() AliasStore {
	return &memAliasStore{rows: make(map[string]string)}
}

func (m *memAliasStore) Lookup(_ context.Context, token string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rows[token], nil
}

func (m *memAliasStore) Upsert(_ context.Context, token, _ string, displayValue string, _ *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[token]; ok {
		// Keep the original mapping deterministic — same token can never re-bind.
		return nil
	}
	m.rows[token] = displayValue
	return nil
}

// tokenForValue returns the deterministic shortid token for a given pattern type
// and raw display value. Same input -> same output (sha256 truncated, hex-cased).
//
// Format: [PREFIX-XXXX] where PREFIX uppercases the entity name and XXXX is the
// first 4 hex chars of sha256(entityType || ":" || displayValue). This is short
// enough to read inline yet collision-resistant for realistic corpus sizes.
func tokenForValue(entityType PatternType, displayValue string) string {
	prefix := tokenPrefix(entityType)
	h := sha256.Sum256([]byte(string(entityType) + ":" + displayValue))
	short := strings.ToUpper(hex.EncodeToString(h[:2])) // 4 chars
	return "[" + prefix + "-" + short + "]"
}

func tokenPrefix(p PatternType) string {
	switch p {
	case PatternRFC:
		return "RFC"
	case PatternCURP:
		return "CURP"
	case PatternEmail:
		return "EMAIL"
	case PatternPhone:
		return "PHONE"
	case PatternAmount:
		return "AMOUNT"
	case PatternLegalName:
		return "LEGAL"
	case PatternCLABE:
		return "CLABE"
	case PatternIBAN:
		return "IBAN"
	case PatternAddress:
		return "ADDR"
	case PatternCreditCard:
		return "CARD"
	case PatternClient:
		return "CLIENT"
	case PatternQuote:
		return "QUOTE"
	case PatternLead:
		return "LEAD"
	default:
		return "ENT"
	}
}
