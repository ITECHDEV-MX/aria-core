package cotizador

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ChatSession statuses.
const (
	ChatStatusInProgress = "in_progress"
	ChatStatusFinalized  = "finalized"
	ChatStatusAbandoned  = "abandoned"
)

// Errors specific to the chat-quote flow.
var (
	ErrChatSessionNotFound = errors.New("chat session not found")
	ErrChatMessageInvalid  = errors.New("chat message invalid")
)

// ChatSession is one quote-chat thread. Lead/RFP/Quote IDs are nullable: the
// session can start lead-only and acquire a quote_id once finalized.
type ChatSession struct {
	ID             string
	LeadID         sql.NullString
	RFPID          sql.NullString
	QuoteID        sql.NullString
	TemplateKey    string
	Title          string
	Status         string
	InitiatedByUID string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	FinalizedAt    sql.NullTime
}

// ChatMessage stores both displayed content (with real client data) and the
// scrubbed payload that was actually sent to Claude. The split is the audit
// guarantee: content_md is ARIA-private, scrubbed_payload is what the LLM saw.
type ChatMessage struct {
	ID                 string
	SessionID          string
	Role               string
	ContentMD          string
	ScrubbedPayload    sql.NullString
	ChannelCallID      sql.NullString
	AttachmentsJSON    string
	ParsedQuoteUpdates string
	CreatedAt          time.Time
}

// CreateChatSessionParams is the input tuple for CreateChatSession.
type CreateChatSessionParams struct {
	LeadID         string
	RFPID          string
	QuoteID        string
	TemplateKey    string
	Title          string
	InitiatedByUID string
}

// CreateChatSession inserts a new in_progress session.
func (s *Store) CreateChatSession(ctx context.Context, p CreateChatSessionParams) (*ChatSession, error) {
	tk := strings.TrimSpace(p.TemplateKey)
	if tk == "" {
		tk = "itechdev_implementation_v1"
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = "Cotización sin título"
	}
	uid := strings.TrimSpace(p.InitiatedByUID)
	if uid == "" {
		return nil, fmt.Errorf("chat session: initiated_by_uid is required")
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO cotizador_chat_sessions (lead_id, rfp_id, quote_id, template_key, title, initiated_by_uid)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5, $6::uuid)
		RETURNING id::text, lead_id::text, rfp_id::text, quote_id::text, template_key, title,
		          status, initiated_by_uid::text, created_at, updated_at, finalized_at
	`,
		strings.TrimSpace(p.LeadID), strings.TrimSpace(p.RFPID), strings.TrimSpace(p.QuoteID),
		tk, title, uid,
	)
	return scanChatSession(row)
}

// GetChatSession returns one session by ID.
func (s *Store) GetChatSession(ctx context.Context, id string) (*ChatSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, lead_id::text, rfp_id::text, quote_id::text, template_key, title,
		       status, initiated_by_uid::text, created_at, updated_at, finalized_at
		FROM cotizador_chat_sessions WHERE id::text = $1
	`, id)
	sess, err := scanChatSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrChatSessionNotFound
	}
	return sess, err
}

// ListChatSessions returns sessions filtered by lead_id (empty = all).
// Most recent first.
func (s *Store) ListChatSessions(ctx context.Context, leadID string, limit int) ([]*ChatSession, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	leadID = strings.TrimSpace(leadID)
	if leadID == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, lead_id::text, rfp_id::text, quote_id::text, template_key, title,
			       status, initiated_by_uid::text, created_at, updated_at, finalized_at
			FROM cotizador_chat_sessions ORDER BY created_at DESC LIMIT $1
		`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text, lead_id::text, rfp_id::text, quote_id::text, template_key, title,
			       status, initiated_by_uid::text, created_at, updated_at, finalized_at
			FROM cotizador_chat_sessions WHERE lead_id::text = $1
			ORDER BY created_at DESC LIMIT $2
		`, leadID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ChatSession
	for rows.Next() {
		sess, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// FinalizeChatSession marks a session as finalized + (optional) attaches a quote_id.
func (s *Store) FinalizeChatSession(ctx context.Context, sessionID, quoteID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cotizador_chat_sessions
		SET status = 'finalized', quote_id = COALESCE(NULLIF($2,'')::uuid, quote_id),
		    finalized_at = NOW(), updated_at = NOW()
		WHERE id::text = $1
	`, sessionID, strings.TrimSpace(quoteID))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrChatSessionNotFound
	}
	return nil
}

// AbandonChatSession marks a session as abandoned (e.g. user closed the tab).
func (s *Store) AbandonChatSession(ctx context.Context, sessionID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cotizador_chat_sessions
		SET status = 'abandoned', updated_at = NOW()
		WHERE id::text = $1 AND status = 'in_progress'
	`, sessionID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrChatSessionNotFound
	}
	return nil
}

// AppendChatMessageParams is the input tuple for AppendChatMessage.
type AppendChatMessageParams struct {
	SessionID          string
	Role               string // user|assistant|system
	ContentMD          string
	ScrubbedPayload    string
	ChannelCallID      string
	AttachmentsJSON    string
	ParsedQuoteUpdates string
}

// AppendChatMessage inserts a new message into the session.
func (s *Store) AppendChatMessage(ctx context.Context, p AppendChatMessageParams) (*ChatMessage, error) {
	role := strings.ToLower(strings.TrimSpace(p.Role))
	if role != "user" && role != "assistant" && role != "system" {
		return nil, fmt.Errorf("%w: role must be user|assistant|system", ErrChatMessageInvalid)
	}
	if strings.TrimSpace(p.ContentMD) == "" {
		return nil, fmt.Errorf("%w: empty content", ErrChatMessageInvalid)
	}
	atts := strings.TrimSpace(p.AttachmentsJSON)
	if atts == "" {
		atts = "[]"
	}
	if !json.Valid([]byte(atts)) {
		return nil, fmt.Errorf("%w: attachments_json invalid", ErrChatMessageInvalid)
	}
	parsed := strings.TrimSpace(p.ParsedQuoteUpdates)
	if parsed == "" {
		parsed = "{}"
	}
	if !json.Valid([]byte(parsed)) {
		return nil, fmt.Errorf("%w: parsed_quote_updates invalid", ErrChatMessageInvalid)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO cotizador_chat_messages (
			session_id, role, content_md, scrubbed_payload, channel_call_id,
			attachments_json, parsed_quote_updates
		) VALUES (
			$1::uuid, $2, $3, NULLIF($4,''),
			NULLIF($5,'')::uuid, $6::jsonb, $7::jsonb
		)
		RETURNING id::text, session_id::text, role, content_md,
		          scrubbed_payload, channel_call_id::text,
		          attachments_json::text, parsed_quote_updates::text, created_at
	`,
		strings.TrimSpace(p.SessionID), role, p.ContentMD, p.ScrubbedPayload,
		strings.TrimSpace(p.ChannelCallID), atts, parsed,
	)
	msg, err := scanChatMessage(row)
	if err != nil {
		return nil, fmt.Errorf("append message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cotizador_chat_sessions SET updated_at = NOW() WHERE id::text = $1`, p.SessionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return msg, nil
}

// ListChatMessages returns messages of a session, oldest first.
func (s *Store) ListChatMessages(ctx context.Context, sessionID string) ([]*ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, session_id::text, role, content_md,
		       scrubbed_payload, channel_call_id::text,
		       attachments_json::text, parsed_quote_updates::text, created_at
		FROM cotizador_chat_messages WHERE session_id::text = $1
		ORDER BY created_at, id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ChatMessage
	for rows.Next() {
		m, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AttachQuoteEmailMeta marks the timestamp + recipient when an email is sent
// from the chat preview modal. Used by audit / status badge in the UI.
func (s *Store) AttachQuoteEmailMeta(ctx context.Context, quoteID, recipient string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cotizador_quotes
		SET email_sent_at = NOW(), email_sent_to = $2, updated_at = NOW()
		WHERE id::text = $1
	`, quoteID, strings.TrimSpace(recipient))
	return err
}

// QuoteEmailStatus returns email_sent_at + recipient (used to show badge in UI).
func (s *Store) QuoteEmailStatus(ctx context.Context, quoteID string) (sentAt sql.NullTime, recipient sql.NullString, err error) {
	row := s.db.QueryRowContext(ctx, `SELECT email_sent_at, email_sent_to FROM cotizador_quotes WHERE id::text = $1`, quoteID)
	err = row.Scan(&sentAt, &recipient)
	if errors.Is(err, sql.ErrNoRows) {
		return sentAt, recipient, ErrQuoteNotFound
	}
	return sentAt, recipient, err
}

// ─── scanners ───────────────────────────────────────────────────────────────

func scanChatSession(s scanner) (*ChatSession, error) {
	var sess ChatSession
	if err := s.Scan(&sess.ID, &sess.LeadID, &sess.RFPID, &sess.QuoteID,
		&sess.TemplateKey, &sess.Title, &sess.Status, &sess.InitiatedByUID,
		&sess.CreatedAt, &sess.UpdatedAt, &sess.FinalizedAt); err != nil {
		return nil, err
	}
	return &sess, nil
}

func scanChatMessage(s scanner) (*ChatMessage, error) {
	var m ChatMessage
	if err := s.Scan(&m.ID, &m.SessionID, &m.Role, &m.ContentMD,
		&m.ScrubbedPayload, &m.ChannelCallID,
		&m.AttachmentsJSON, &m.ParsedQuoteUpdates, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}
