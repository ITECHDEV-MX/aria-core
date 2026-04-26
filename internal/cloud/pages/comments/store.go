package comments

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Comment es la representación de un comment con metadata.
type Comment struct {
	ID              string
	PageID          string
	BlockAnchor     string
	ParentCommentID string
	ContentMD       string
	AuthorUID       string
	IsResolved      bool
	ResolvedByUID   string
	ResolvedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Mentions        []string // UIDs mencionados (agregado al cargar)
	ReplyCount      int      // count de hijos directos (agregado en List)
}

// Store implementa CRUD de comments + mentions sobre Postgres.
type Store struct {
	db *sql.DB
}

// New construye un Store sobre una conexión postgres ya abierta.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateParams parametriza la creación de un comment.
type CreateParams struct {
	PageID          string
	BlockAnchor     string
	ParentCommentID string
	ContentMD       string
	AuthorUID       string
	// MentionResolver es una función opcional que mapea username → uid.
	// Si nil o retorna "", la mention textual se ignora (sólo se persisten UIDs explícitos).
	MentionResolver func(username string) string
}

// Create inserta un comment + extrae mentions y persiste. Si content_md o
// author_uid están vacíos, retorna ErrInvalidInput.
func (s *Store) Create(ctx context.Context, p CreateParams) (*Comment, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("comments: store not initialized")
	}
	if strings.TrimSpace(p.ContentMD) == "" {
		return nil, fmt.Errorf("%w: content_md is required", ErrInvalidInput)
	}
	if _, err := uuid.Parse(strings.TrimSpace(p.AuthorUID)); err != nil {
		return nil, fmt.Errorf("%w: author_uid must be UUID", ErrInvalidInput)
	}
	if _, err := uuid.Parse(strings.TrimSpace(p.PageID)); err != nil {
		return nil, fmt.Errorf("%w: page_id must be UUID", ErrInvalidInput)
	}
	var parent any
	if v := strings.TrimSpace(p.ParentCommentID); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			return nil, fmt.Errorf("%w: parent_comment_id must be UUID", ErrInvalidInput)
		}
		parent = v
	}
	var anchor any
	if a := strings.TrimSpace(p.BlockAnchor); a != "" {
		anchor = a
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		INSERT INTO aria_page_comments (page_id, block_anchor, parent_comment_id, content_md, author_uid)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, page_id, COALESCE(block_anchor,''), COALESCE(parent_comment_id::text,''),
		         content_md, author_uid, is_resolved, COALESCE(resolved_by_uid::text,''),
		         resolved_at, created_at, updated_at`
	row := tx.QueryRowContext(ctx, q, p.PageID, anchor, parent, p.ContentMD, p.AuthorUID)
	c, err := scanComment(row)
	if err != nil {
		return nil, fmt.Errorf("comments: create: %w", err)
	}

	// Extraer mentions.
	mentions := ParseMentions(p.ContentMD)
	uids := UniqueUIDs(mentions)
	// Resolver usernames si hay resolver.
	if p.MentionResolver != nil {
		seen := map[string]struct{}{}
		for _, u := range uids {
			seen[strings.ToLower(u)] = struct{}{}
		}
		for _, m := range mentions {
			if m.IsUID {
				continue
			}
			resolved := strings.TrimSpace(p.MentionResolver(m.Token))
			if resolved == "" {
				continue
			}
			if _, dup := seen[strings.ToLower(resolved)]; dup {
				continue
			}
			seen[strings.ToLower(resolved)] = struct{}{}
			uids = append(uids, resolved)
		}
	}
	for _, uid := range uids {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO aria_page_mentions (comment_id, mentioned_uid) VALUES ($1, $2)`, c.ID, uid); err != nil {
			return nil, fmt.Errorf("comments: insert mention: %w", err)
		}
	}
	c.Mentions = uids

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}

// Get retorna un comment por id (incluye mentions).
func (s *Store) Get(ctx context.Context, id string) (*Comment, error) {
	const q = `
		SELECT id, page_id, COALESCE(block_anchor,''), COALESCE(parent_comment_id::text,''),
		       content_md, author_uid, is_resolved, COALESCE(resolved_by_uid::text,''),
		       resolved_at, created_at, updated_at
		FROM aria_page_comments WHERE id = $1`
	row := s.db.QueryRowContext(ctx, q, id)
	c, err := scanComment(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Mentions, _ = s.loadMentionUIDs(ctx, c.ID)
	return c, nil
}

// ListPageOpts parametriza ListPage.
type ListPageOpts struct {
	OnlyUnresolved bool
	OnlyResolved   bool
	BlockAnchor    string
	// IncludeReplies: si false, retorna sólo top-level (parent NULL).
	IncludeReplies bool
	Limit          int
}

// ListPage retorna comments de una page, top-level primero (orden cronológico),
// con replyCount agregado para los top-level. Si IncludeReplies=true incluye
// también los hijos en el flat array.
func (s *Store) ListPage(ctx context.Context, pageID string, opts ListPageOpts) ([]*Comment, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conds := []string{"page_id = $1"}
	args := []any{pageID}
	idx := 2
	if !opts.IncludeReplies {
		conds = append(conds, "parent_comment_id IS NULL")
	}
	if opts.OnlyUnresolved {
		conds = append(conds, "is_resolved = FALSE")
	}
	if opts.OnlyResolved {
		conds = append(conds, "is_resolved = TRUE")
	}
	if a := strings.TrimSpace(opts.BlockAnchor); a != "" {
		conds = append(conds, fmt.Sprintf("block_anchor = $%d", idx))
		args = append(args, a)
		idx++
	}

	q := `
		SELECT id, page_id, COALESCE(block_anchor,''), COALESCE(parent_comment_id::text,''),
		       content_md, author_uid, is_resolved, COALESCE(resolved_by_uid::text,''),
		       resolved_at, created_at, updated_at
		FROM aria_page_comments
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY created_at ASC
		LIMIT $` + fmt.Sprintf("%d", idx)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanCommentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cargar reply counts en batch para top-level.
	if !opts.IncludeReplies {
		for _, c := range out {
			c.ReplyCount, _ = s.countReplies(ctx, c.ID)
		}
	}
	return out, nil
}

// ListReplies retorna los replies directos de un comment.
func (s *Store) ListReplies(ctx context.Context, parentID string) ([]*Comment, error) {
	const q = `
		SELECT id, page_id, COALESCE(block_anchor,''), COALESCE(parent_comment_id::text,''),
		       content_md, author_uid, is_resolved, COALESCE(resolved_by_uid::text,''),
		       resolved_at, created_at, updated_at
		FROM aria_page_comments WHERE parent_comment_id = $1
		ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanCommentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateContent updatea content_md. Sólo el author puede hacerlo (caller debe
// chequear); el store no reaplica ese check pero retorna ErrForbidden si
// authorUID no matchea.
func (s *Store) UpdateContent(ctx context.Context, id, authorUID, newContent string) (*Comment, error) {
	if strings.TrimSpace(newContent) == "" {
		return nil, fmt.Errorf("%w: content_md is required", ErrInvalidInput)
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.AuthorUID != authorUID {
		return nil, ErrForbidden
	}
	const q = `
		UPDATE aria_page_comments SET content_md = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING id, page_id, COALESCE(block_anchor,''), COALESCE(parent_comment_id::text,''),
		         content_md, author_uid, is_resolved, COALESCE(resolved_by_uid::text,''),
		         resolved_at, created_at, updated_at`
	row := s.db.QueryRowContext(ctx, q, newContent, id)
	c, err := scanComment(row)
	if err != nil {
		return nil, err
	}
	c.Mentions, _ = s.loadMentionUIDs(ctx, c.ID)
	return c, nil
}

// Resolve marca un comment como resuelto.
func (s *Store) Resolve(ctx context.Context, id, byUID string) error {
	if _, err := uuid.Parse(byUID); err != nil {
		return fmt.Errorf("%w: by_uid must be UUID", ErrInvalidInput)
	}
	const q = `
		UPDATE aria_page_comments
		SET is_resolved = TRUE, resolved_by_uid = $1, resolved_at = NOW(), updated_at = NOW()
		WHERE id = $2`
	res, err := s.db.ExecContext(ctx, q, byUID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Unresolve revierte el resolve.
func (s *Store) Unresolve(ctx context.Context, id string) error {
	const q = `
		UPDATE aria_page_comments
		SET is_resolved = FALSE, resolved_by_uid = NULL, resolved_at = NULL, updated_at = NOW()
		WHERE id = $1`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete elimina un comment. Caller debe chequear permisos (autor o admin).
func (s *Store) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM aria_page_comments WHERE id = $1`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountUnresolved retorna el número de comments unresolved en una page.
func (s *Store) CountUnresolved(ctx context.Context, pageID string) (int, error) {
	const q = `SELECT COUNT(*) FROM aria_page_comments WHERE page_id = $1 AND is_resolved = FALSE`
	var n int
	err := s.db.QueryRowContext(ctx, q, pageID).Scan(&n)
	return n, err
}

// CountUnreadMentions retorna mentions no-read del usuario.
func (s *Store) CountUnreadMentions(ctx context.Context, uid string) (int, error) {
	const q = `SELECT COUNT(*) FROM aria_page_mentions WHERE mentioned_uid = $1 AND read_at IS NULL`
	var n int
	err := s.db.QueryRowContext(ctx, q, uid).Scan(&n)
	return n, err
}

// MarkMentionsRead marca todas las mentions de un usuario en una page como leídas.
func (s *Store) MarkMentionsRead(ctx context.Context, uid, pageID string) error {
	const q = `
		UPDATE aria_page_mentions m SET read_at = NOW()
		FROM aria_page_comments c
		WHERE m.comment_id = c.id AND m.mentioned_uid = $1 AND c.page_id = $2 AND m.read_at IS NULL`
	_, err := s.db.ExecContext(ctx, q, uid, pageID)
	return err
}

// ListMentions retorna las mentions del usuario, opcionalmente sólo unread.
func (s *Store) ListMentions(ctx context.Context, uid string, onlyUnread bool, limit int) ([]MentionRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
		SELECT m.id, m.comment_id, c.page_id, c.content_md, c.author_uid, c.created_at,
		       m.read_at, m.notified_at
		FROM aria_page_mentions m
		JOIN aria_page_comments c ON c.id = m.comment_id
		WHERE m.mentioned_uid = $1`
	if onlyUnread {
		q += " AND m.read_at IS NULL"
	}
	q += ` ORDER BY c.created_at DESC LIMIT $2`
	rows, err := s.db.QueryContext(ctx, q, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MentionRow
	for rows.Next() {
		var m MentionRow
		var readAt, notifiedAt sql.NullTime
		if err := rows.Scan(&m.ID, &m.CommentID, &m.PageID, &m.ContentMD, &m.AuthorUID, &m.CreatedAt, &readAt, &notifiedAt); err != nil {
			return nil, err
		}
		if readAt.Valid {
			t := readAt.Time
			m.ReadAt = &t
		}
		if notifiedAt.Valid {
			t := notifiedAt.Time
			m.NotifiedAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MentionRow es un row del listado de mentions.
type MentionRow struct {
	ID         string
	CommentID  string
	PageID     string
	ContentMD  string
	AuthorUID  string
	CreatedAt  time.Time
	ReadAt     *time.Time
	NotifiedAt *time.Time
}

// ─── helpers ────────────────────────────────────────────────────────────────

func (s *Store) loadMentionUIDs(ctx context.Context, commentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mentioned_uid FROM aria_page_mentions WHERE comment_id = $1`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) countReplies(ctx context.Context, parentID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM aria_page_comments WHERE parent_comment_id = $1`, parentID).Scan(&n)
	return n, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanComment(rs rowScanner) (*Comment, error) {
	var c Comment
	var resolvedAt sql.NullTime
	if err := rs.Scan(&c.ID, &c.PageID, &c.BlockAnchor, &c.ParentCommentID,
		&c.ContentMD, &c.AuthorUID, &c.IsResolved, &c.ResolvedByUID,
		&resolvedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		c.ResolvedAt = &t
	}
	return &c, nil
}

func scanCommentRows(rs *sql.Rows) (*Comment, error) {
	return scanComment(rs)
}
