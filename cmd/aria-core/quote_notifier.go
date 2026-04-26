package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
)

// quoteEmailNotifier implements quoteNotifier — fires emails async after a
// quote status transition or close. Lookups go directly against cotizador.Store
// (for quote details) and cloudusers.Store (for creator email lookup).
type quoteEmailNotifier struct {
	store     *cotizador.Store
	users     *cloudusers.Store
	email     *email.Service
	publicURL string
}

func newQuoteEmailNotifier(store *cotizador.Store, users *cloudusers.Store, email *email.Service, publicURL string) *quoteEmailNotifier {
	return &quoteEmailNotifier{store: store, users: users, email: email, publicURL: strings.TrimSpace(publicURL)}
}

// NotifyQuoteStatusChange fires off an email goroutine for the given transition.
// Fire-and-forget: errors are logged but not propagated.
func (n *quoteEmailNotifier) NotifyQuoteStatusChange(_ context.Context, quoteID, fromStatus, newStatus, byUID, notes string) {
	if n == nil || n.email == nil {
		return
	}
	if fromStatus == newStatus {
		return
	}
	go n.dispatchQuoteEmail(quoteID, newStatus, byUID, notes)
}

// NotifyQuoteClose treats a close as a status transition for email purposes.
func (n *quoteEmailNotifier) NotifyQuoteClose(_ context.Context, quoteID, fromStatus, newStatus, byUID, reason string) {
	if n == nil || n.email == nil {
		return
	}
	if fromStatus == newStatus {
		return
	}
	go n.dispatchQuoteEmail(quoteID, newStatus, byUID, reason)
}

func (n *quoteEmailNotifier) dispatchQuoteEmail(quoteID, newStatus, byUID, notes string) {
	// Build a fresh context with a generous timeout so the goroutine doesn't
	// inherit a request context that may already be cancelled by the time
	// the HTTP handler returns.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	q, err := n.store.GetQuote(ctx, quoteID)
	if err != nil {
		log.Printf("email: notifier: get quote %s: %v", quoteID, err)
		return
	}
	qc := buildEmailQuoteContext(q, n.publicURL, notes)

	switch newStatus {
	case cotizador.QuoteStatusSent:
		if err := n.email.SendQuoteSent(ctx, qc); err != nil {
			log.Printf("email: SendQuoteSent quote=%s: %v", quoteID, err)
		}
	case cotizador.QuoteStatusApproved:
		bcc := n.lookupCreatorEmail(ctx, q.CreatedByUID.String)
		if err := n.email.SendQuoteApproved(ctx, qc, bcc); err != nil {
			log.Printf("email: SendQuoteApproved quote=%s: %v", quoteID, err)
		}
	case cotizador.QuoteStatusRejected:
		creator := n.lookupCreatorEmail(ctx, q.CreatedByUID.String)
		if creator == "" {
			log.Printf("email: SendQuoteRejected quote=%s: no creator email; skipping", quoteID)
			return
		}
		if err := n.email.SendQuoteRejected(ctx, qc, creator); err != nil {
			log.Printf("email: SendQuoteRejected quote=%s: %v", quoteID, err)
		}
	default:
		// Other transitions (draft, in_review, expired) don't trigger emails.
	}
	_ = byUID // reserved for future audit emails.
}

// lookupCreatorEmail resolves a UID to its email; returns empty string if not found.
func (n *quoteEmailNotifier) lookupCreatorEmail(ctx context.Context, uid string) string {
	uid = strings.TrimSpace(uid)
	if uid == "" || n.users == nil {
		return ""
	}
	u, err := n.users.GetByUID(ctx, uid)
	if err != nil {
		if !errors.Is(err, cloudusers.ErrNotFound) {
			log.Printf("email: lookup creator uid=%s: %v", uid, err)
		}
		return ""
	}
	return u.Email
}

// buildEmailQuoteContext converts a *cotizador.Quote into an email.QuoteContext.
func buildEmailQuoteContext(q *cotizador.Quote, publicURL, notes string) email.QuoteContext {
	folio := ""
	if q.Folio.Valid {
		folio = q.Folio.String
	}
	validUntil := ""
	if q.ValidUntil.Valid {
		validUntil = q.ValidUntil.Time.Format("2006-01-02")
	}
	return email.QuoteContext{
		QuoteID:                 q.ID,
		Folio:                   folio,
		ProductName:             q.ProductName,
		PreparedForCompany:      q.PreparedForCompany,
		PreparedForContactName:  q.PreparedForContactName,
		PreparedForContactEmail: q.PreparedForContactEmail,
		Total:                   q.Total,
		Currency:                q.Currency,
		Status:                  q.Status,
		ValidUntil:              validUntil,
		PreparedByName:          q.PreparedByName,
		PreparedByEmail:         q.PreparedByEmail,
		Notes:                   notes,
		PublicURL:               publicURL,
	}
}

// runQuoteExpiringSweep scans cotizador_quotes for entries whose status is
// 'sent' or 'in_review' and whose valid_until is within the next 7 days, and
// fires the expiring-warning email to the creator. Designed to be called from
// a daily cron/timer.
func runQuoteExpiringSweep(ctx context.Context, store *cotizador.Store, users *cloudusers.Store, emailSvc *email.Service, publicURL string) error {
	if store == nil || emailSvc == nil {
		return nil
	}
	db := store.DB()
	if db == nil {
		return errors.New("quote sweep: cotizador store db is nil")
	}
	const q = `
		SELECT id::text, created_by_uid::text
		FROM cotizador_quotes
		WHERE status IN ('sent','in_review')
		  AND valid_until IS NOT NULL
		  AND valid_until <= (CURRENT_DATE + INTERVAL '7 days')
		  AND valid_until >= CURRENT_DATE
	`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	notifier := newQuoteEmailNotifier(store, users, emailSvc, publicURL)
	count := 0
	for rows.Next() {
		var quoteID, createdBy string
		if err := rows.Scan(&quoteID, &createdBy); err != nil {
			log.Printf("email: sweep scan: %v", err)
			continue
		}
		quote, err := store.GetQuote(ctx, quoteID)
		if err != nil {
			log.Printf("email: sweep get quote %s: %v", quoteID, err)
			continue
		}
		creator := notifier.lookupCreatorEmail(ctx, createdBy)
		if creator == "" {
			continue
		}
		qc := buildEmailQuoteContext(quote, publicURL, "")
		if err := emailSvc.SendQuoteExpiring(ctx, qc, creator); err != nil {
			log.Printf("email: SendQuoteExpiring quote=%s: %v", quoteID, err)
			continue
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	log.Printf("email: quote-expiring sweep dispatched %d notification(s)", count)
	return nil
}
