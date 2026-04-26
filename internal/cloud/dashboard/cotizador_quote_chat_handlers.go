package dashboard

import (
	"fmt"
	"net/http"
	"strings"
)

// handleQuoteChatCreate creates a new chat session and redirects to the page.
func (h *handlers) handleQuoteChatCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	uid := h.principalUID(r)
	in := CreateChatSessionInput{
		LeadID:      strings.TrimSpace(r.PostForm.Get("lead_id")),
		RFPID:       strings.TrimSpace(r.PostForm.Get("rfp_id")),
		TemplateKey: strings.TrimSpace(r.PostForm.Get("template_key")),
		Title:       strings.TrimSpace(r.PostForm.Get("title")),
		InitiatedBy: uid,
	}
	sess, err := h.cfg.QuoteChat.CreateSession(r.Context(), in)
	if err != nil {
		http.Error(w, fmt.Sprintf("create chat session: %v", err), http.StatusBadRequest)
		return
	}
	target := "/dashboard/cotizador/quote-chat/" + sess.ID
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleQuoteChatPage renders the full chat page.
func (h *handlers) handleQuoteChatPage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	sess, msgs, sections, err := h.cfg.QuoteChat.GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("get session: %v", err), http.StatusNotFound)
		return
	}
	p := h.principalFromRequest(r)
	component := CotizadorQuoteChatPage(sess, msgs, sections, "")
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Quote-Chat", p.DisplayName(), "cotizador", p.Roles(), component))
}

// handleQuoteChatSend processes a new user message + dispatches to the LLM.
func (h *handlers) handleQuoteChatSend(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(r.PostForm.Get("message"))
	if msg == "" {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}
	uid := h.principalUID(r)
	in := SendChatMessageInput{
		SessionID:   id,
		UserUID:     uid,
		Message:     msg,
		Sensitivity: strings.TrimSpace(r.PostForm.Get("sensitivity")),
	}
	if in.Sensitivity == "" {
		in.Sensitivity = "client"
	}
	if _, _, err := h.cfg.QuoteChat.SendUserMessage(r.Context(), in); err != nil {
		http.Error(w, fmt.Sprintf("send: %v", err), http.StatusBadGateway)
		return
	}
	msgs, _ := h.cfg.QuoteChat.ListMessages(r.Context(), id)
	renderComponent(w, r, CotizadorChatMessagesPartial(msgs))
}

// handleQuoteChatPreview returns the live-preview pane HTML.
func (h *handlers) handleQuoteChatPreview(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	sess, _, sections, err := h.cfg.QuoteChat.GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("get session: %v", err), http.StatusNotFound)
		return
	}
	renderComponent(w, r, CotizadorChatPreviewPartial(sess, sections))
}

// handleQuoteChatMessages returns the message list partial (used by polling).
func (h *handlers) handleQuoteChatMessages(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	msgs, err := h.cfg.QuoteChat.ListMessages(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("list messages: %v", err), http.StatusNotFound)
		return
	}
	renderComponent(w, r, CotizadorChatMessagesPartial(msgs))
}

// handleQuoteChatSectionEdit returns the inline editor for one section.
func (h *handlers) handleQuoteChatSectionEdit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	key := r.PathValue("key")
	sess, _, _, err := h.cfg.QuoteChat.GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("get session: %v", err), http.StatusNotFound)
		return
	}
	title, content, err := h.cfg.QuoteChat.GetSection(r.Context(), id, key)
	if err != nil {
		http.Error(w, fmt.Sprintf("get section: %v", err), http.StatusNotFound)
		return
	}
	renderComponent(w, r, CotizadorChatSectionEditPartial(sess, key, title, content))
}

// handleQuoteChatSectionSave persists the inline editor changes.
func (h *handlers) handleQuoteChatSectionSave(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	key := r.PathValue("key")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	content := r.PostForm.Get("content_md")
	if err := h.cfg.QuoteChat.UpsertSection(r.Context(), id, key, title, content); err != nil {
		http.Error(w, fmt.Sprintf("save section: %v", err), http.StatusBadRequest)
		return
	}
	sess, _, sections, err := h.cfg.QuoteChat.GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("reload: %v", err), http.StatusInternalServerError)
		return
	}
	// Replace the whole preview pane so all sections are re-rendered with the new content.
	renderComponent(w, r, CotizadorChatPreviewPartial(sess, sections))
}

// handleQuoteChatFinalize marks the session finalized + creates the quote.
func (h *handlers) handleQuoteChatFinalize(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	uid := h.principalUID(r)
	if _, err := h.cfg.QuoteChat.FinalizeSession(r.Context(), id, uid); err != nil {
		http.Error(w, fmt.Sprintf("finalize: %v", err), http.StatusBadRequest)
		return
	}
	// Re-render the page (full root replacement).
	sess, msgs, sections, err := h.cfg.QuoteChat.GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("reload: %v", err), http.StatusInternalServerError)
		return
	}
	renderComponent(w, r, CotizadorQuoteChatPage(sess, msgs, sections, ""))
}

// handleQuoteChatEmailPreview returns the email preview modal.
// CRITICAL: this is preview only — no email is sent until /email-send is called.
func (h *handlers) handleQuoteChatEmailPreview(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	view, err := h.cfg.QuoteChat.BuildEmailPreview(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("build email preview: %v", err), http.StatusBadRequest)
		return
	}
	renderComponent(w, r, CotizadorChatEmailModal(view))
}

// handleQuoteChatEmailSend dispatches the actual email after manual confirm.
func (h *handlers) handleQuoteChatEmailSend(w http.ResponseWriter, r *http.Request) {
	if h.cfg.QuoteChat == nil {
		http.Error(w, "quote-chat module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	quoteID := strings.TrimSpace(r.PostForm.Get("quote_id"))
	to := strings.TrimSpace(r.PostForm.Get("to"))
	cc := strings.TrimSpace(r.PostForm.Get("cc"))
	subject := strings.TrimSpace(r.PostForm.Get("subject"))
	body := r.PostForm.Get("body")

	if to == "" || subject == "" || strings.TrimSpace(body) == "" {
		renderComponent(w, r, CotizadorChatEmailErrorToast("Faltan campos obligatorios"))
		return
	}
	if err := h.cfg.QuoteChat.SendEmail(r.Context(), id, quoteID, to, cc, subject, body); err != nil {
		renderComponent(w, r, CotizadorChatEmailErrorToast(err.Error()))
		return
	}
	renderComponent(w, r, CotizadorChatEmailSentToast(to))
}

// principalUID is shared with vault_handlers.go.
