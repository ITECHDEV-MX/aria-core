package dashboard

import (
	"fmt"
	"net/http"
	"strings"
)

func (h *handlers) handleAdminSkillsList(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	skills, err := h.cfg.AriaMem.ListAllSkills(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("list skills: %v", err), http.StatusInternalServerError)
		return
	}
	stacks, _ := h.cfg.AriaMem.ListUniqueStacks(r.Context())
	p := h.principalFromRequest(r)
	component := AdminSkillsListPage(skills, stacks)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Skills", p.DisplayName(), "admin-skills", p.Roles(), component))
}

// handleAdminSkillsListPartial — endpoint HTMX para refrescar la tabla de skills
// con search + filtros (stack, active, source). Devuelve solo la partial.
func (h *handlers) handleAdminSkillsListPartial(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	stack := strings.TrimSpace(r.URL.Query().Get("stack"))
	activeOnly := r.URL.Query().Get("active") == "on" || r.URL.Query().Get("active") == "true" || r.URL.Query().Get("active") == "1"
	source := strings.TrimSpace(r.URL.Query().Get("source"))

	skills, err := h.cfg.AriaMem.SearchSkills(r.Context(), q, stack, activeOnly)
	if err != nil {
		http.Error(w, fmt.Sprintf("search skills: %v", err), http.StatusInternalServerError)
		return
	}
	// Filter por source en memoria (raro que haya muchos sources distintos).
	if source != "" {
		filtered := make([]AriaSkillView, 0, len(skills))
		for _, s := range skills {
			if s.Source == source {
				filtered = append(filtered, s)
			}
		}
		skills = filtered
	}
	renderComponent(w, r, AdminSkillsListPartial(skills))
}

func (h *handlers) handleAdminSkillNew(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	component := AdminSkillEditor(nil)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Nuevo skill", p.DisplayName(), "admin-skills", p.Roles(), component))
}

func (h *handlers) handleAdminSkillEdit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	sk, err := h.cfg.AriaMem.GetSkillByID(r.Context(), id)
	if err != nil {
		http.Error(w, "skill no encontrado", http.StatusNotFound)
		return
	}
	p := h.principalFromRequest(r)
	component := AdminSkillEditor(sk)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Editar skill — "+sk.Name, p.DisplayName(), "admin-skills", p.Roles(), component))
}

func (h *handlers) handleAdminSkillUpsert(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.PostForm.Get("id"))
	if id == "" {
		http.Error(w, "id es requerido (slug del skill)", http.StatusBadRequest)
		return
	}
	stack := []string{}
	if v := strings.TrimSpace(r.PostForm.Get("stack")); v != "" {
		for _, t := range strings.Split(v, ",") {
			if tt := strings.TrimSpace(t); tt != "" {
				stack = append(stack, tt)
			}
		}
	}
	in := UpsertAriaSkillInput{
		ID:          id,
		Name:        strings.TrimSpace(r.PostForm.Get("name")),
		Description: strings.TrimSpace(r.PostForm.Get("description")),
		Stack:       stack,
		Content:     r.PostForm.Get("content"),
		Source:      strings.TrimSpace(r.PostForm.Get("source")),
		Active:      r.PostForm.Get("active") == "on" || r.PostForm.Get("active") == "true",
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if err := h.cfg.AriaMem.UpsertSkill(r.Context(), in); err != nil {
		http.Error(w, fmt.Sprintf("upsert skill: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/admin/skills", http.StatusSeeOther)
}

func (h *handlers) handleAdminSkillToggle(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	sk, err := h.cfg.AriaMem.GetSkillByID(r.Context(), id)
	if err != nil {
		http.Error(w, "skill no encontrado", http.StatusNotFound)
		return
	}
	if err := h.cfg.AriaMem.SetSkillActive(r.Context(), id, !sk.Active); err != nil {
		http.Error(w, fmt.Sprintf("toggle: %v", err), http.StatusBadRequest)
		return
	}
	skills, _ := h.cfg.AriaMem.ListAllSkills(r.Context())
	renderComponent(w, r, AdminSkillsListPartial(skills))
}

func (h *handlers) handleAdminSkillDelete(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := h.cfg.AriaMem.DeleteSkill(r.Context(), id); err != nil {
		http.Error(w, fmt.Sprintf("delete: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/admin/skills", http.StatusSeeOther)
}

func (h *handlers) handleAdminMCPView(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	component := AdminMCPViewPage()
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("MCP & Tools", p.DisplayName(), "admin", p.Roles(), component))
}
