package dashboard

import (
	"net/http"
	"strings"
)

// Principal represents the authenticated dashboard user.
type Principal struct {
	displayName string
	roles       []string
}

// DisplayName returns the display name for this principal.
func (p Principal) DisplayName() string {
	if strings.TrimSpace(p.displayName) == "" {
		return "OPERATOR"
	}
	return p.displayName
}

// IsAdmin returns whether this principal has admin role.
func (p Principal) IsAdmin() bool { return p.HasRole("admin") }

// Roles returns the assigned roles slice.
func (p Principal) Roles() []string { return p.roles }

// HasRole checks if principal has a specific role.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole checks if principal has at least one of the given roles.
func (p Principal) HasAnyRole(roles ...string) bool {
	for _, want := range roles {
		if p.HasRole(want) {
			return true
		}
	}
	return false
}

// principalFromRequest derives a Principal from the current request using the
// MountConfig closures.
func (h *handlers) principalFromRequest(r *http.Request) Principal {
	name := ""
	if h.cfg.GetDisplayName != nil {
		name = strings.TrimSpace(h.cfg.GetDisplayName(r))
	}
	var roles []string
	if h.cfg.GetRoles != nil {
		roles = h.cfg.GetRoles(r)
	} else if h.cfg.IsAdmin != nil && h.cfg.IsAdmin(r) {
		// Backward-compat: si solo IsAdmin, asumir role admin.
		roles = []string{"admin"}
	}
	return Principal{displayName: name, roles: roles}
}
