// Package comments implementa threading de comments inline en bloques de
// aria_pages, con soporte de @mentions extraidas y notificación opcional vía
// email service.
package comments

import (
	"fmt"
	"github.com/ITECHDEV-MX/aria-core/internal/obs"
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Errores públicos.
var (
	ErrNotFound     = errors.New("comments: not found")
	ErrForbidden    = errors.New("comments: forbidden")
	ErrInvalidInput = errors.New("comments: invalid input")
)

// mentionRegex matchea @uuid o @palabra. La canonical es @<UUID>.
// Si el input es @username, el resolver del cloudserver lo mapea a UID antes
// de persistir; la regex acepta ambas formas.
var mentionRegex = regexp.MustCompile(`@([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[A-Za-z0-9_.+-]+)`)

// Mention es una mention extraída del content_md.
type Mention struct {
	Raw   string // "@username" o "@uuid"
	Token string // sin "@"
	IsUID bool   // true si Token parsea como UUID
}

// ParseMentions extrae todas las @mentions de un texto. No deduplica.
func ParseMentions(content string) []Mention {
	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	out := make([]Mention, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := m[0]
		token := m[1]
		_, err := uuid.Parse(token)
		out = append(out, Mention{Raw: raw, Token: token, IsUID: err == nil})
	}
	return out
}

// UniqueUIDs filtra mentions a sólo UIDs válidos y deduplica.
func UniqueUIDs(mentions []Mention) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range mentions {
		if !m.IsUID {
			continue
		}
		k := strings.ToLower(m.Token)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, m.Token)
	}
	return out
}

// EmailNotifier es el contrato mínimo que necesitamos del email service.
// Nota: el cloudserver inyecta un adapter sobre internal/cloud/email.Service.
type EmailNotifier interface {
	IsConfigured() bool
	PublicURL() string
	SendMentionNotification(ctx context.Context, mc MentionEmailContext) error
}

// MentionEmailContext son los datos para renderear la notification email.
type MentionEmailContext struct {
	ToEmail        string
	ToName         string
	PageTitle      string
	PageURL        string
	CommentID      string
	CommentSnippet string
	MentionedBy    string
}

// UserResolver resuelve uid → email para envío de notificaciones.
type UserResolver interface {
	GetByUID(ctx context.Context, uid string) (email, name string, err error)
}

// NotifyMentioned envía emails a todas las mentions ya persistidas.
// Si emailSvc es nil o no está configurado, sólo loguea.
// Es safe-to-call con nils — en degraded mode sólo registra.
func (s *Store) NotifyMentioned(ctx context.Context, commentID string, emailSvc EmailNotifier, users UserResolver, pageTitle, pageURL, mentionedBy string) error {
	if s == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mentioned_uid FROM aria_page_mentions
		WHERE comment_id = $1 AND notified_at IS NULL`, commentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pendingMention struct{ id, uid string }
	pending := []pendingMention{}
	for rows.Next() {
		var pm pendingMention
		if err := rows.Scan(&pm.id, &pm.uid); err != nil {
			return err
		}
		pending = append(pending, pm)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Snippet del comment (≤140 chars).
	var snippet string
	_ = s.db.QueryRowContext(ctx, `SELECT content_md FROM aria_page_comments WHERE id = $1`, commentID).Scan(&snippet)
	if len(snippet) > 140 {
		snippet = snippet[:140] + "..."
	}

	for _, pm := range pending {
		toEmail, toName := "", ""
		if users != nil {
			if e, n, err := users.GetByUID(ctx, pm.uid); err == nil {
				toEmail, toName = e, n
			}
		}
		// Si no hay email configurado o no resolvimos to_email, sólo marcamos
		// notified_at para no reintentar infinitamente y logueamos.
		if emailSvc == nil || !emailSvc.IsConfigured() || toEmail == "" {
			obs.L().Info(fmt.Sprintf("comments.NotifyMentioned: SKIP send (degraded) comment=%s uid=%s", commentID, pm.uid))
		} else {
			if err := emailSvc.SendMentionNotification(ctx, MentionEmailContext{
				ToEmail:        toEmail,
				ToName:         toName,
				PageTitle:      pageTitle,
				PageURL:        pageURL,
				CommentID:      commentID,
				CommentSnippet: snippet,
				MentionedBy:    mentionedBy,
			}); err != nil {
				obs.L().Info(fmt.Sprintf("comments.NotifyMentioned: send failed comment=%s uid=%s err=%v", commentID, pm.uid, err))
				continue
			}
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE aria_page_mentions SET notified_at = NOW() WHERE id = $1`, pm.id); err != nil {
			return err
		}
	}
	return nil
}
