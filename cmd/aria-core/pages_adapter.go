package main

import (
	"context"
	"log"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/comments"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/databases"
)

// pageDatabaseAdapter expone *databases.Store al cloudserver. Match con la
// interface PageDatabaseService: simple delegation.
type pageDatabaseAdapter struct {
	*databases.Store
}

func newPageDatabaseAdapter(cs *cloudstore.CloudStore) *pageDatabaseAdapter {
	return &pageDatabaseAdapter{Store: databases.New(cs.DB())}
}

// pageCommentsAdapter expone *comments.Store + email notifier al cloudserver.
// Implementa cloudserver.PageCommentsService.
type pageCommentsAdapter struct {
	*comments.Store
	emailSvc *email.Service
	users    comments.UserResolver
}

func newPageCommentsAdapter(cs *cloudstore.CloudStore, emailSvc *email.Service, users *dashboardUserAdapter) *pageCommentsAdapter {
	var resolver comments.UserResolver
	if users != nil {
		resolver = newUserResolverAdapter(users)
	}
	return &pageCommentsAdapter{
		Store:    comments.New(cs.DB()),
		emailSvc: emailSvc,
		users:    resolver,
	}
}

// NotifyMentioned satisface PageCommentsService.NotifyMentioned. Adapta el
// email service interno (que es generic) a la interfaz EmailNotifier que
// espera comments.NotifyMentioned.
func (a *pageCommentsAdapter) NotifyMentioned(ctx context.Context, commentID, pageTitle, pageURL, mentionedBy string) error {
	if a == nil || a.Store == nil {
		return nil
	}
	notifier := &mentionEmailNotifier{svc: a.emailSvc}
	return a.Store.NotifyMentioned(ctx, commentID, notifier, a.users, pageTitle, pageURL, mentionedBy)
}

// mentionEmailNotifier adapta *email.Service a comments.EmailNotifier.
// Reusa la plantilla genérica con un subject custom.
type mentionEmailNotifier struct {
	svc *email.Service
}

func (n *mentionEmailNotifier) IsConfigured() bool {
	return n != nil && n.svc != nil && n.svc.IsConfigured()
}

func (n *mentionEmailNotifier) PublicURL() string {
	if n == nil || n.svc == nil {
		return ""
	}
	return n.svc.PublicURL()
}

// SendMentionNotification envía email con subject custom. Si el template
// específico no existe en internal/cloud/email/templates, esta función simplemente
// loguea (degraded behavior). En la wave actual no introducimos un nuevo template
// para evitar tocar internal/cloud/email/.
func (n *mentionEmailNotifier) SendMentionNotification(ctx context.Context, mc comments.MentionEmailContext) error {
	if !n.IsConfigured() {
		log.Printf("mentionEmailNotifier: SKIP (email not configured) to=%s page=%s", mc.ToEmail, mc.PageTitle)
		return nil
	}
	// Best-effort: usar SendInvite como genérico no aplica acá. Como NO debemos
	// tocar internal/cloud/email/, sólo loggeamos. Cuando el equipo de email
	// agregue un template "mention", el notifier puede extenderse aquí.
	log.Printf("mentionEmailNotifier: would send to=%s subject=mention on %q snippet=%q",
		mc.ToEmail, mc.PageTitle, truncateString(mc.CommentSnippet, 60))
	return nil
}

// userResolverAdapter adapta *dashboardUserAdapter a comments.UserResolver.
// Usado al notificar mentions.
type userResolverAdapter struct {
	users *dashboardUserAdapter
}

func newUserResolverAdapter(u *dashboardUserAdapter) *userResolverAdapter {
	return &userResolverAdapter{users: u}
}

func (r *userResolverAdapter) GetByUID(ctx context.Context, uid string) (string, string, error) {
	if r == nil || r.users == nil {
		return "", "", nil
	}
	u, err := r.users.GetByUID(ctx, uid)
	if err != nil || u == nil {
		return "", "", err
	}
	return u.Email, u.Name, nil
}
