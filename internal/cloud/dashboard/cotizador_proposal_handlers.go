package dashboard

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark"
)

// handleCotizadorQuoteProposal — vista completa "propuesta" lista para presentar/imprimir.
func (h *handlers) handleCotizadorQuoteProposal(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	q, err := h.cfg.Cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, "quote not found", http.StatusNotFound)
		return
	}
	items, _ := h.cfg.Cotizador.ListQuoteItems(r.Context(), quoteID)
	sections, _ := h.cfg.Cotizador.ListSections(r.Context(), quoteID)
	// Renderizar markdown a HTML por sección
	rendered := make([]renderedSection, 0, len(sections))
	for _, s := range sections {
		rendered = append(rendered, renderedSection{
			Key:    s.Key,
			Title:  s.Title,
			HTML:   renderMarkdown(s.ContentMD),
			RawMD:  s.ContentMD,
		})
	}
	p := h.principalFromRequest(r)
	component := CotizadorProposalView(q, items, rendered)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	title := fmt.Sprintf("Propuesta %s", q.Folio)
	renderComponent(w, r, Layout(title, p.DisplayName(), "cotizador", p.Roles(), component))
}

func (h *handlers) handleCotizadorQuoteEditHeader(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	q, err := h.cfg.Cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, "quote not found", http.StatusNotFound)
		return
	}
	sections, _ := h.cfg.Cotizador.ListSections(r.Context(), quoteID)
	p := h.principalFromRequest(r)
	component := CotizadorProposalEditor(q, sections)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Editar propuesta", p.DisplayName(), "cotizador", p.Roles(), component))
}

func (h *handlers) handleCotizadorQuoteUpdateHeader(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	in := UpdateProposalHeaderInput{
		Folio:                   strings.TrimSpace(r.PostForm.Get("folio")),
		ProposalType:            strings.TrimSpace(r.PostForm.Get("proposal_type")),
		ProductName:             strings.TrimSpace(r.PostForm.Get("product_name")),
		ProductSubtitle:         strings.TrimSpace(r.PostForm.Get("product_subtitle")),
		PreparedForCompany:      strings.TrimSpace(r.PostForm.Get("prepared_for_company")),
		PreparedForArea:         strings.TrimSpace(r.PostForm.Get("prepared_for_area")),
		PreparedForContactName:  strings.TrimSpace(r.PostForm.Get("prepared_for_contact_name")),
		PreparedForContactEmail: strings.TrimSpace(r.PostForm.Get("prepared_for_contact_email")),
		PreparedByName:          strings.TrimSpace(r.PostForm.Get("prepared_by_name")),
		PreparedByEmail:         strings.TrimSpace(r.PostForm.Get("prepared_by_email")),
		PreparedByRole:          strings.TrimSpace(r.PostForm.Get("prepared_by_role")),
	}
	if v := strings.TrimSpace(r.PostForm.Get("issue_date")); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			in.IssueDate = &t
		}
	}
	if v := strings.TrimSpace(r.PostForm.Get("tags")); v != "" {
		for _, t := range strings.Split(v, ",") {
			if tt := strings.TrimSpace(t); tt != "" {
				in.Tags = append(in.Tags, tt)
			}
		}
	}
	// Defaults iTechDev si vacíos
	if in.PreparedByName == "" {
		in.PreparedByName = "Juan Carlos Guajardo"
	}
	if in.PreparedByEmail == "" {
		in.PreparedByEmail = "jcguajardo@itechdev.com.mx"
	}
	if in.PreparedByRole == "" {
		in.PreparedByRole = "CEO & Founder"
	}
	if in.ProposalType == "" {
		in.ProposalType = "commercial"
	}
	if err := h.cfg.Cotizador.UpdateProposalHeader(r.Context(), quoteID, in); err != nil {
		http.Error(w, fmt.Sprintf("update header: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/cotizador/quotes/"+quoteID+"/edit-header", http.StatusSeeOther)
}

func (h *handlers) handleCotizadorQuoteSectionUpsert(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.PostForm.Get("key"))
	title := strings.TrimSpace(r.PostForm.Get("title"))
	content := r.PostForm.Get("content_md")
	sortOrder, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("sort_order")))
	if key == "" || title == "" {
		http.Error(w, "key and title required", http.StatusBadRequest)
		return
	}
	if err := h.cfg.Cotizador.UpsertSection(r.Context(), quoteID, key, title, content, sortOrder); err != nil {
		http.Error(w, fmt.Sprintf("upsert section: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/cotizador/quotes/"+quoteID+"/edit-header", http.StatusSeeOther)
}

func (h *handlers) handleCotizadorStats(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	stats, err := h.cfg.Cotizador.GetDashboardStats(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("stats: %v", err), http.StatusInternalServerError)
		return
	}
	p := h.principalFromRequest(r)
	component := CotizadorStatsPage(stats)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Pipeline & Stats", p.DisplayName(), "cotizador", p.Roles(), component))
}

func (h *handlers) handleCotizadorQuoteApplyTemplate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	templateKey := strings.TrimSpace(r.PostForm.Get("template_key"))
	if templateKey == "" {
		http.Error(w, "template_key requerido", http.StatusBadRequest)
		return
	}
	if err := h.cfg.Cotizador.ApplyTemplate(r.Context(), quoteID, templateKey); err != nil {
		http.Error(w, fmt.Sprintf("aplicar plantilla: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/cotizador/quotes/"+quoteID+"/edit-header", http.StatusSeeOther)
}

func (h *handlers) handleCotizadorQuoteSectionDelete(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	key := r.PathValue("key")
	if err := h.cfg.Cotizador.DeleteSection(r.Context(), quoteID, key); err != nil {
		http.Error(w, fmt.Sprintf("delete section: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/cotizador/quotes/"+quoteID+"/edit-header", http.StatusSeeOther)
}

// renderedSection es lo que recibe el template — HTML pre-renderizado del markdown.
type renderedSection struct {
	Key   string
	Title string
	HTML  templ.Component
	RawMD string
}

// renderMarkdown convierte markdown a HTML usando goldmark con defaults seguros.
func renderMarkdown(md string) templ.Component {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(md), &buf); err != nil {
		return templ.Raw(`<p class="login-error">error rendering markdown</p>`)
	}
	return templ.Raw(buf.String())
}
