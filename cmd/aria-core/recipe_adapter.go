// recipe_adapter.go wires the recipe runner into the cloud runtime.
//
// The runner needs three bridges (vault, aria-memory, shell) plus the
// Postgres-backed store. We instantiate it here to keep cloud.go focused on
// composition, and to avoid forcing internal/cloud/cloudserver to import the
// concrete recipes package (it only declares the RecipeRunnerService interface).
package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/recipes"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/vault"
)

// vaultRecipeBridge adapts vault.PgStore to recipes.VaultBridge.
// The bridge is responsible for ACL checks (via store.Reveal) and for
// emitting audit log entries (handled inside vault.PgStore.Reveal).
type vaultRecipeBridge struct {
	v *vaultAdapter
}

// ResolveAndReveal looks up secrets by name and returns plaintext values.
// Each Reveal call hits aria_secret_access_log via vault.PgStore.
func (b *vaultRecipeBridge) ResolveAndReveal(ctx context.Context, names []string, byUID, reason string) (map[string]string, error) {
	if b.v == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(names))
	principal := vault.Principal{UID: byUID, Roles: []string{"dev"}}
	// List once, then map by name. This mirrors aria_vault_mcp.go::resolveSecretIDByName.
	rs, err := b.v.store.List(ctx, vault.ListFilter{Limit: 500}, principal)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(rs))
	for _, sec := range rs {
		byName[sec.Name] = sec.ID
	}
	for _, name := range names {
		id, ok := byName[name]
		if !ok {
			return nil, errSecretNotFound{name: name}
		}
		val, err := b.v.store.Reveal(ctx, id, principal, reason)
		if err != nil {
			return nil, err
		}
		out[name] = val
	}
	return out, nil
}

type errSecretNotFound struct{ name string }

func (e errSecretNotFound) Error() string { return "vault: secret not found: " + e.name }

// ariaMemRecipeBridge adapts the in-process ariamem service to recipes.AriaMemBridge.
type ariaMemRecipeBridge struct {
	mem *ariaMemAdapter
}

func (b *ariaMemRecipeBridge) Save(ctx context.Context, p recipes.AriaSaveBridgeInput) error {
	if b.mem == nil {
		return nil
	}
	_, err := b.mem.Save(ctx, cloudserver.AriaMemSaveInput{
		Project:         p.Project,
		Scope:           p.Scope,
		ObservationType: p.Type,
		Title:           p.Title,
		Narrative:       p.Content,
		TopicKey:        p.TopicKey,
		Source:          p.Source,
		DeveloperUID:    p.DeveloperUID,
		DeveloperRole:   "dev",
	})
	return err
}

// newRecipeRunner builds the cloud-side runner with all bridges wired in.
// Returns nil-safe adapter even if vault/mem are degraded — the runner just
// fails the corresponding step kinds with a "no bridge configured" message.
func newRecipeRunner(cs *cloudstore.CloudStore, v *vaultAdapter, mem *ariaMemAdapter) cloudserver.RecipeRunnerService {
	store := recipes.NewPgStore(cs.DB())
	opts := []recipes.Option{
		recipes.WithDefaultProject("aria-core"),
	}
	if v != nil {
		opts = append(opts, recipes.WithVault(&vaultRecipeBridge{v: v}))
	}
	if mem != nil {
		opts = append(opts, recipes.WithAriaMem(&ariaMemRecipeBridge{mem: mem}))
	}
	return recipes.NewRunner(store, opts...)
}
