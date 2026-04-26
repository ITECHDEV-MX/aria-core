package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/channels"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	emailpkg "github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/redactor"
)

// quoteChatAdapter implements dashboard.QuoteChatService by orchestrating
// cotizador.ChatOrchestrator + the channels router + the redactor + the
// email service.
type quoteChatAdapter struct {
	store        *cotizador.Store
	orch         *cotizador.ChatOrchestrator
	router       channels.Router
	redactorSvc  redactor.Service
	emailSvc     *emailpkg.Service
	publicURL    string
	db           *sql.DB
	// finalizeHook (opcional) — wave 8 lo usa para auto-sync al repo central
	// cuando una session pasa a 'finalized'. Nil-safe.
	finalizeHook quoteChatFinalizeHook
}

// quoteChatFinalizeHook captura el callback que knowledgebase.Service invoca
// al cerrar una sesión con quote_id ya seteado.
type quoteChatFinalizeHook interface {
	OnQuoteFinalized(ctx context.Context, sessionID, quoteID string) error
}

// setFinalizeHook permite inyectar el callback de wave 8 sin acoplar el
// adapter al paquete knowledgebase directamente.
func (a *quoteChatAdapter) setFinalizeHook(h quoteChatFinalizeHook) {
	if a == nil {
		return
	}
	a.finalizeHook = h
}

// newQuoteChatAdapter wires the adapter. db, store, router, redactorSvc are
// required; emailSvc may be nil (the email send action will return an error).
func newQuoteChatAdapter(
	store *cotizador.Store,
	router channels.Router,
	redactorSvc redactor.Service,
	emailSvc *emailpkg.Service,
	publicURL string,
	db *sql.DB,
) *quoteChatAdapter {
	a := &quoteChatAdapter{
		store:       store,
		router:      router,
		redactorSvc: redactorSvc,
		emailSvc:    emailSvc,
		publicURL:   strings.TrimSpace(publicURL),
		db:          db,
	}
	a.orch = cotizador.NewChatOrchestrator(store, channelRouterAdapter{r: router}, scrubberAdapter{svc: redactorSvc}, channelLoggerAdapter{db: db})
	return a
}

// channelRouterAdapter bridges channels.Router to cotizador.ChatChannelRouter.
type channelRouterAdapter struct{ r channels.Router }

func (a channelRouterAdapter) Query(ctx context.Context, req cotizador.ChatRouterRequest) (*cotizador.ChatRouterResponse, error) {
	if a.r == nil {
		return nil, errors.New("channel router not wired")
	}
	msgs := make([]channels.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, channels.Message{Role: m.Role, Content: m.Content})
	}
	resp, err := a.r.Query(ctx, channels.Request{
		SystemPrompt: req.SystemPrompt,
		Messages:     msgs,
		Sensitivity:  req.Sensitivity,
		TimeoutSec:   req.TimeoutSec,
		Model:        req.Model,
	})
	if err != nil {
		return nil, err
	}
	return &cotizador.ChatRouterResponse{
		Content:    resp.Content,
		Model:      resp.Model,
		Channel:    resp.Channel,
		DurationMs: resp.DurationMs,
		TokensIn:   resp.TokensIn,
		TokensOut:  resp.TokensOut,
		Error:      resp.Error,
	}, nil
}

// scrubberAdapter bridges redactor.Service into cotizador.ChatScrubber.
type scrubberAdapter struct{ svc redactor.Service }

func (a scrubberAdapter) ScrubString(ctx context.Context, text string) (string, string) {
	type stringScrub interface {
		ScrubString(ctx context.Context, text string) (string, string)
	}
	if ss, ok := a.svc.(stringScrub); ok {
		return ss.ScrubString(ctx, text)
	}
	return text, "[]"
}

func (a scrubberAdapter) Expand(ctx context.Context, text string) (string, error) {
	if a.svc == nil {
		return text, nil
	}
	return a.svc.Expand(ctx, text)
}

func (a scrubberAdapter) LogEgress(ctx context.Context, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string, payloadSize int, scrubbed bool, redactionsJSON string) error {
	type stringLogger interface {
		LogEgressString(ctx context.Context, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string, payloadSize int, scrubbed bool, redactionsJSON string) error
	}
	if sl, ok := a.svc.(stringLogger); ok {
		return sl.LogEgressString(ctx, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash, payloadSize, scrubbed, redactionsJSON)
	}
	return nil
}

// channelLoggerAdapter persists channel call telemetry into aria_channel_calls.
type channelLoggerAdapter struct{ db *sql.DB }

func (a channelLoggerAdapter) LogCall(ctx context.Context, rec cotizador.ChannelCallRecord) (string, error) {
	return channels.LogCall(ctx, a.db, channels.CallRecord{
		Channel:              rec.Channel,
		Model:                rec.Model,
		PromptSize:           rec.PromptSize,
		ResponseSize:         rec.ResponseSize,
		TokensIn:             rec.TokensIn,
		TokensOut:            rec.TokensOut,
		DurationMs:           rec.DurationMs,
		Sensitivity:          rec.Sensitivity,
		InitiatedByUID:       rec.InitiatedByUID,
		RelatedChatSessionID: rec.RelatedChatSessionID,
		CostEstimateUSD:      rec.CostEstimateUSD,
		Error:                rec.Error,
	})
}

// ─── dashboard.QuoteChatService implementation ───────────────────────────────

func (a *quoteChatAdapter) CreateSession(ctx context.Context, in dashboard.CreateChatSessionInput) (*dashboard.ChatSessionView, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("quote-chat adapter: not initialized")
	}
	sess, err := a.store.CreateChatSession(ctx, cotizador.CreateChatSessionParams{
		LeadID:         in.LeadID,
		RFPID:          in.RFPID,
		TemplateKey:    in.TemplateKey,
		Title:          in.Title,
		InitiatedByUID: in.InitiatedBy,
	})
	if err != nil {
		return nil, err
	}
	return a.sessionViewWithLead(ctx, sess), nil
}

func (a *quoteChatAdapter) GetSession(ctx context.Context, id string) (*dashboard.ChatSessionView, []dashboard.ChatMessageView, []dashboard.ChatPreviewSectionView, error) {
	if a == nil || a.store == nil {
		return nil, nil, nil, errors.New("quote-chat adapter: not initialized")
	}
	sess, err := a.store.GetChatSession(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	view := a.sessionViewWithLead(ctx, sess)
	msgs, err := a.ListMessages(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	sections, err := a.ListSections(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return view, msgs, sections, nil
}

func (a *quoteChatAdapter) ListMessages(ctx context.Context, sessionID string) ([]dashboard.ChatMessageView, error) {
	rows, err := a.store.ListChatMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.ChatMessageView, 0, len(rows))
	for _, r := range rows {
		out = append(out, chatMessageToView(r))
	}
	return out, nil
}

func (a *quoteChatAdapter) ListSections(ctx context.Context, sessionID string) ([]dashboard.ChatPreviewSectionView, error) {
	sess, err := a.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !sess.QuoteID.Valid || strings.TrimSpace(sess.QuoteID.String) == "" {
		// No quote yet → derive sections from the latest assistant message.
		msgs, _ := a.store.ListChatMessages(ctx, sessionID)
		var assistant *cotizador.ChatMessage
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "assistant" {
				assistant = msgs[i]
				break
			}
		}
		if assistant == nil {
			return nil, nil
		}
		parsed := cotizador.ParseSectionBlocks(assistant.ContentMD)
		out := make([]dashboard.ChatPreviewSectionView, 0, len(parsed))
		for i, p := range parsed {
			out = append(out, dashboard.ChatPreviewSectionView{
				Key: simpleSlug(p.Title), Title: p.Title, ContentMD: p.Content, SortOrder: i + 1,
			})
		}
		return out, nil
	}
	secs, err := a.store.ListSections(ctx, sess.QuoteID.String)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.ChatPreviewSectionView, 0, len(secs))
	for _, s := range secs {
		out = append(out, dashboard.ChatPreviewSectionView{
			Key: s.Key, Title: s.Title, ContentMD: s.ContentMD, SortOrder: s.SortOrder,
		})
	}
	return out, nil
}

func (a *quoteChatAdapter) SendUserMessage(ctx context.Context, in dashboard.SendChatMessageInput) (*dashboard.ChatMessageView, *dashboard.ChatMessageView, error) {
	atts := make([]cotizador.ChatAttachment, 0, len(in.Attachments))
	for _, at := range in.Attachments {
		atts = append(atts, cotizador.ChatAttachment{
			AttachmentID: at.AttachmentID, Filename: at.Filename, MIME: at.MIME, Size: at.Size,
		})
	}
	turn, err := a.orch.SendUserMessage(ctx, cotizador.SendUserMessageParams{
		SessionID:   in.SessionID,
		UserUID:     in.UserUID,
		UserMessage: in.Message,
		Sensitivity: in.Sensitivity,
		Attachments: atts,
		TimeoutSec:  in.TimeoutSec,
	})
	if err != nil {
		return nil, nil, err
	}
	user := chatMessageToView(turn.UserMessage)
	assistant := chatMessageToView(turn.AssistantMessage)
	if assistant.Channel == "" {
		assistant.Channel = turn.Channel
	}
	if assistant.Model == "" {
		assistant.Model = turn.Model
	}
	return &user, &assistant, nil
}

func (a *quoteChatAdapter) UpsertSection(ctx context.Context, sessionID, key, title, contentMD string) error {
	sess, err := a.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !sess.QuoteID.Valid {
		return errors.New("session has no quote yet — finalize first")
	}
	return a.store.UpsertSection(ctx, sess.QuoteID.String, key, title, contentMD, 0)
}

func (a *quoteChatAdapter) GetSection(ctx context.Context, sessionID, key string) (string, string, error) {
	secs, err := a.ListSections(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	for _, s := range secs {
		if s.Key == key {
			return s.Title, s.ContentMD, nil
		}
	}
	return "", "", errors.New("section not found")
}

func (a *quoteChatAdapter) FinalizeSession(ctx context.Context, sessionID, byUID string) (string, error) {
	sess, err := a.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	// If the session has no quote yet, create a draft quote with one symbolic
	// item so the section upserts have something to attach to.
	if !sess.QuoteID.Valid || strings.TrimSpace(sess.QuoteID.String) == "" {
		if !sess.LeadID.Valid {
			return "", errors.New("cannot finalize: session has no lead")
		}
		q, qerr := a.store.CreateQuote(ctx, cotizador.CreateQuoteParams{
			LeadID:        sess.LeadID.String,
			Currency:      "MXN",
			Justification: "Generada vía quote-chat",
			Terms:         "Términos a definir.",
			CreatedByUID:  byUID,
			Role:          "cotizador",
			Items: []cotizador.CreateQuoteItemParams{
				{SKU: "", Description: "Cotización quote-chat (placeholder)", Qty: 1, UnitPrice: 0},
			},
		})
		if qerr != nil {
			return "", fmt.Errorf("create quote on finalize: %w", qerr)
		}
		// Now flush every parsed section into quote_sections.
		secs, _ := a.ListSections(ctx, sessionID)
		for i, s := range secs {
			_ = a.store.UpsertSection(ctx, q.ID, s.Key, s.Title, s.ContentMD, i+1)
		}
		if err := a.store.FinalizeChatSession(ctx, sessionID, q.ID); err != nil {
			return "", err
		}
		// Wave 8: trigger knowledge-base sync. Async + nil-safe.
		if a.finalizeHook != nil {
			_ = a.finalizeHook.OnQuoteFinalized(ctx, sessionID, q.ID)
		}
		return q.ID, nil
	}
	if err := a.store.FinalizeChatSession(ctx, sessionID, ""); err != nil {
		return "", err
	}
	if a.finalizeHook != nil {
		_ = a.finalizeHook.OnQuoteFinalized(ctx, sessionID, sess.QuoteID.String)
	}
	return sess.QuoteID.String, nil
}

func (a *quoteChatAdapter) BuildEmailPreview(ctx context.Context, sessionID string) (*dashboard.EmailPreviewView, error) {
	sess, err := a.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !sess.QuoteID.Valid {
		return nil, errors.New("no quote attached to session — finalize first")
	}
	q, err := a.store.GetQuote(ctx, sess.QuoteID.String)
	if err != nil {
		return nil, err
	}
	secs, _ := a.store.ListSections(ctx, sess.QuoteID.String)
	view := &dashboard.EmailPreviewView{
		QuoteID:   q.ID,
		SessionID: sess.ID,
	}

	// Subject default
	productName := strings.TrimSpace(q.ProductName)
	if productName == "" {
		productName = sess.Title
	}
	view.Subject = "Propuesta: " + productName + " · iTechDev"

	// Recipients
	to := strings.TrimSpace(q.PreparedForContactEmail)
	if to == "" && sess.LeadID.Valid {
		if lead, lerr := a.store.GetLead(ctx, sess.LeadID.String); lerr == nil {
			to = strings.TrimSpace(lead.Email)
		}
	}
	view.To = to
	view.CC = "jcguajardo@itechdev.com.mx"

	// Body — minimal HTML render of the sections.
	var body strings.Builder
	body.WriteString(`<div style="font-family:Arial,sans-serif;max-width:680px;margin:0 auto;color:#222;line-height:1.5">`)
	body.WriteString(`<h2>Propuesta iTechDev — ` + html.EscapeString(productName) + `</h2>`)
	if strings.TrimSpace(q.PreparedForContactName) != "" {
		body.WriteString(`<p>Hola ` + html.EscapeString(q.PreparedForContactName) + `,</p>`)
	} else {
		body.WriteString(`<p>Estimado/a,</p>`)
	}
	body.WriteString(`<p>Te compartimos la propuesta solicitada. Cualquier comentario, quedamos atentos.</p>`)
	for _, s := range secs {
		body.WriteString(`<h3 style="border-bottom:1px solid #ddd;padding-bottom:0.3rem">` + html.EscapeString(s.Title) + `</h3>`)
		body.WriteString(`<div style="white-space:pre-wrap">` + html.EscapeString(s.ContentMD) + `</div>`)
	}
	body.WriteString(`<p style="margin-top:2rem">Saludos,<br/><strong>JC Guajardo</strong><br/>iTechDev — Soluciones a medida</p>`)
	body.WriteString(`</div>`)
	view.BodyHTML = body.String()

	view.HasPDF = false // PDF rendering can be added by re-using cotizador_proposal.pdf endpoint
	return view, nil
}

func (a *quoteChatAdapter) SendEmail(ctx context.Context, sessionID, quoteID, to, cc, subject, bodyHTML string) error {
	if a == nil || a.emailSvc == nil {
		return errors.New("email service not configured")
	}
	if !a.emailSvc.IsConfigured() {
		return errors.New("email module disabled (M365 env vars missing)")
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return errors.New("recipient required")
	}
	bccList := splitCSV(cc)
	if err := a.emailSvc.SendRawHTML(ctx, to, bccList, subject, bodyHTML); err != nil {
		return err
	}
	if strings.TrimSpace(quoteID) != "" {
		_ = a.store.AttachQuoteEmailMeta(ctx, quoteID, to)
	}
	return nil
}

func (a *quoteChatAdapter) ListTemplates() []dashboard.CotizadorTemplateView {
	tps := cotizador.AvailableTemplates()
	out := make([]dashboard.CotizadorTemplateView, 0, len(tps))
	for _, t := range tps {
		out = append(out, dashboard.CotizadorTemplateView{
			Key:             t.Key,
			Name:            t.Name,
			Description:     t.Description,
			ProposalType:    t.ProposalType,
			DefaultProduct:  t.DefaultProduct,
			DefaultSubtitle: t.DefaultSubtitle,
			DefaultTags:     append([]string{}, t.DefaultTags...),
			SectionCount:    len(t.Sections),
		})
	}
	return out
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func chatSessionToView(s *cotizador.ChatSession, leadName, leadCompany string) *dashboard.ChatSessionView {
	if s == nil {
		return nil
	}
	v := &dashboard.ChatSessionView{
		ID:          s.ID,
		LeadID:     nullStringValue(s.LeadID),
		RFPID:      nullStringValue(s.RFPID),
		QuoteID:    nullStringValue(s.QuoteID),
		TemplateKey: s.TemplateKey,
		Title:      s.Title,
		Status:     s.Status,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
		LeadName:   leadName,
		LeadCompany: leadCompany,
	}
	if s.FinalizedAt.Valid {
		t := s.FinalizedAt.Time
		v.FinalizedAt = &t
	}
	return v
}

func chatMessageToView(m *cotizador.ChatMessage) dashboard.ChatMessageView {
	if m == nil {
		return dashboard.ChatMessageView{}
	}
	v := dashboard.ChatMessageView{
		ID:        m.ID,
		SessionID: m.SessionID,
		Role:      m.Role,
		ContentMD: m.ContentMD,
		CreatedAt: m.CreatedAt,
	}
	if m.ChannelCallID.Valid {
		v.ChannelCallID = m.ChannelCallID.String
	}
	return v
}

func nullStringValue(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}

func (a *quoteChatAdapter) sessionViewWithLead(ctx context.Context, s *cotizador.ChatSession) *dashboard.ChatSessionView {
	leadName, leadCompany := "", ""
	if s != nil && s.LeadID.Valid {
		if lead, err := a.store.GetLead(ctx, s.LeadID.String); err == nil && lead != nil {
			leadName = lead.Name
			leadCompany = lead.Company
		}
	}
	return chatSessionToView(s, leadName, leadCompany)
}

// chatLeadName is unused but kept for symmetry / future overrides.
var _ = time.Time{}

// splitCSV splits a comma/space-separated list of email addresses.
func splitCSV(in string) []string {
	parts := strings.FieldsFunc(in, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// simpleSlug is a tiny slugify good enough for unsaved sections (mirrors the
// internal cotizador package one).
func simpleSlug(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	var b strings.Builder
	prevDash := false
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "section"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
