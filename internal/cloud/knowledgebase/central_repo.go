package knowledgebase

import (
	"context"
	"fmt"
	"strings"
)

// EnsureCentralRepo verifica que el repo central existe; si no, lo crea.
// También bootstrapea README.md raíz, .gitignore y plantillas/ si están
// ausentes. Es idempotente: corre seguro en cada arranque.
func (s *service) EnsureCentralRepo(ctx context.Context) error {
	if s.gh == nil {
		return ErrGitHubNotConfigured
	}

	// 1. Repo: get-or-create.
	repo, err := s.gh.GetRepo(ctx, s.org, s.repo)
	if err != nil {
		return fmt.Errorf("knowledgebase: get repo %s/%s: %w", s.org, s.repo, err)
	}
	if repo == nil {
		desc := "Knowledge base central de iTechDev — PRDs, historias, cotizaciones (auto-mantenido por ARIA Core)."
		repo, err = s.gh.CreateRepo(ctx, s.org, s.repo, desc, true /*private*/)
		if err != nil {
			return fmt.Errorf("knowledgebase: create repo %s/%s: %w", s.org, s.repo, err)
		}
	}
	s.defaultBranch = strings.TrimSpace(repo.DefaultBranch)
	if s.defaultBranch == "" {
		s.defaultBranch = "main"
	}

	// 2. Bootstrap README.md raíz si no existe.
	if existing, err := s.gh.GetFile(ctx, s.org, s.repo, s.defaultBranch, "README.md"); err == nil && existing == nil {
		// No existe → crear con índice vacío.
		body := RenderRootIndex(nil)
		_, err := s.putFile(ctx, "README.md", []byte(body), "", commitMessage("docs", "bootstrap root README"))
		if err != nil {
			return fmt.Errorf("knowledgebase: bootstrap README.md: %w", err)
		}
	}

	// 3. Bootstrap plantillas/.
	templates := []struct {
		path    string
		content string
		message string
	}{
		{"plantillas/prd-template.md", PRDTemplateMarkdown, commitMessage("docs", "bootstrap PRD template")},
		{"plantillas/historia-template.md", HistoriaTemplateMarkdown, commitMessage("docs", "bootstrap historia template")},
		{"plantillas/cotizacion-template.md", CotizacionTemplateMarkdown, commitMessage("docs", "bootstrap cotización template")},
	}
	for _, t := range templates {
		existing, err := s.gh.GetFile(ctx, s.org, s.repo, s.defaultBranch, t.path)
		if err != nil {
			return fmt.Errorf("knowledgebase: get %s: %w", t.path, err)
		}
		if existing != nil {
			continue
		}
		if _, err := s.putFile(ctx, t.path, []byte(t.content), "", t.message); err != nil {
			return fmt.Errorf("knowledgebase: bootstrap %s: %w", t.path, err)
		}
	}

	// 4. Bootstrap .gitignore (filtrar archivos comunes que no deben commitearse).
	if existing, err := s.gh.GetFile(ctx, s.org, s.repo, s.defaultBranch, ".gitignore"); err == nil && existing == nil {
		gitignore := strings.TrimSpace(`
# ARIA Core knowledge-base sync
.DS_Store
Thumbs.db
*.tmp
*.bak
.vscode/
.idea/
node_modules/
`) + "\n"
		_, _ = s.putFile(ctx, ".gitignore", []byte(gitignore), "", commitMessage("chore", "bootstrap .gitignore"))
	}

	return nil
}

// commitMessage construye un mensaje de commit estándar con el sufijo
// `[skip-aria-sync]` que el listener del repo central usa para evitar
// loops si en el futuro pulled changes triggerean re-sync.
func commitMessage(kind, summary string) string {
	return fmt.Sprintf("%s: %s [skip-aria-sync]", kind, summary)
}
