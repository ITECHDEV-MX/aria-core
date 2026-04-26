package dashboard

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// principalUID extracts the UID from session claims via MountConfig.GetUID; "" si no hay sesión.
func (h *handlers) principalUID(r *http.Request) string {
	if h.cfg.GetUID != nil {
		return h.cfg.GetUID(r)
	}
	return ""
}

func (h *handlers) handleVaultPage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	p := h.principalFromRequest(r)
	component := VaultPage(!h.cfg.Vault.Available())
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Vault", p.DisplayName(), "vault", p.Roles(), component))
}

func (h *handlers) handleVaultList(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	uid := h.principalUID(r)
	onlyOwned := r.URL.Query().Get("owned") != "false" // default true (mis secrets)
	rs, err := h.cfg.Vault.List(r.Context(), uid, onlyOwned)
	if err != nil {
		http.Error(w, fmt.Sprintf("list: %v", err), http.StatusInternalServerError)
		return
	}
	renderComponent(w, r, VaultListPartial(rs))
}

func (h *handlers) handleVaultCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	uid := h.principalUID(r)
	if uid == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	category := strings.TrimSpace(r.PostForm.Get("category"))
	scope := strings.TrimSpace(r.PostForm.Get("scope"))
	project := strings.TrimSpace(r.PostForm.Get("project"))
	clientID := strings.TrimSpace(r.PostForm.Get("client_id"))
	description := strings.TrimSpace(r.PostForm.Get("description"))
	value := r.PostForm.Get("value")
	if name == "" || category == "" || scope == "" || value == "" {
		http.Error(w, "name, category, scope, value son requeridos", http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Vault.Create(r.Context(), name, category, scope, project, clientID, description, value, uid); err != nil {
		http.Error(w, fmt.Sprintf("create: %v", err), http.StatusBadRequest)
		return
	}
	rs, _ := h.cfg.Vault.List(r.Context(), uid, true)
	renderComponent(w, r, VaultListPartial(rs))
}

func (h *handlers) handleVaultReveal(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	uid := h.principalUID(r)
	reason := strings.TrimSpace(r.PostForm.Get("reason"))
	if reason == "" {
		renderComponent(w, r, VaultRevealError("razón requerida"))
		return
	}
	value, err := h.cfg.Vault.Reveal(r.Context(), id, uid, reason)
	if err != nil {
		renderComponent(w, r, VaultRevealError(err.Error()))
		return
	}
	renderComponent(w, r, VaultRevealResult(id, value))
}

func (h *handlers) handleVaultRotate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	uid := h.principalUID(r)
	value := r.PostForm.Get("value")
	if value == "" {
		http.Error(w, "value requerido", http.StatusBadRequest)
		return
	}
	if err := h.cfg.Vault.Rotate(r.Context(), id, value, uid); err != nil {
		http.Error(w, fmt.Sprintf("rotate: %v", err), http.StatusBadRequest)
		return
	}
	rs, _ := h.cfg.Vault.List(r.Context(), uid, true)
	renderComponent(w, r, VaultListPartial(rs))
}

func (h *handlers) handleVaultDelete(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	uid := h.principalUID(r)
	if err := h.cfg.Vault.Delete(r.Context(), id, uid); err != nil {
		http.Error(w, fmt.Sprintf("delete: %v", err), http.StatusBadRequest)
		return
	}
	rs, _ := h.cfg.Vault.List(r.Context(), uid, true)
	renderComponent(w, r, VaultListPartial(rs))
}

func (h *handlers) handleVaultAuditDetail(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	limit := 100
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	entries, err := h.cfg.Vault.AccessLog(r.Context(), id, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("audit: %v", err), http.StatusInternalServerError)
		return
	}
	p := h.principalFromRequest(r)
	component := VaultAuditPage(entries, "Audit log: "+id)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Vault Audit", p.DisplayName(), "vault", p.Roles(), component))
}

func (h *handlers) handleVaultAuditGlobal(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Vault == nil {
		http.Error(w, "vault no configurado", http.StatusServiceUnavailable)
		return
	}
	limit := 100
	offset := 0
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	entries, err := h.cfg.Vault.GlobalAuditLog(r.Context(), limit, offset)
	if err != nil {
		http.Error(w, fmt.Sprintf("audit: %v", err), http.StatusInternalServerError)
		return
	}
	p := h.principalFromRequest(r)
	component := VaultAuditPage(entries, "Audit log global")
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Vault Audit", p.DisplayName(), "vault", p.Roles(), component))
}
