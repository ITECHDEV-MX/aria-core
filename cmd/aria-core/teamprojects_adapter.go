// teamprojects_adapter wires internal/cloud/teamprojects + internal/cloud/github
// into cloudserver via the TeamProjectsService + TeamProjectsCreateAdapter
// contracts. Also supplies vault-by-name resolution for the GH PAT.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/github"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/teamprojects"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/vault"
)

// vaultByNameResolver implementa github.VaultLike resolviendo un secret por nombre.
// Usa la principal "system" (creator-by-default) que para fines de bootstrap es admin.
type vaultByNameResolver struct {
	store      *vault.PgStore
	systemUID  string
	rawDB      *sql.DB
}

func newVaultByNameResolver(v *vault.PgStore, systemUID string) *vaultByNameResolver {
	return &vaultByNameResolver{store: v, systemUID: systemUID, rawDB: v.DBRaw()}
}

func (r *vaultByNameResolver) RevealByName(ctx context.Context, name string) (string, error) {
	if r == nil || r.store == nil || r.rawDB == nil {
		return "", fmt.Errorf("vault: not configured")
	}
	if !r.store.Available() {
		return "", fmt.Errorf("vault: degraded mode (set ARIA_CORE_VAULT_MASTER_KEY)")
	}
	// Locate active secret id by name.
	var id string
	err := r.rawDB.QueryRowContext(ctx, `SELECT id::text FROM aria_secrets WHERE name=$1 AND is_active=TRUE LIMIT 1`, name).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("vault: secret %q no encontrado (configurar GITHUB_API_TOKEN en vault)", name)
		}
		return "", err
	}
	// Reveal as system principal (admin role).
	pt, err := r.store.Reveal(ctx, id, vault.Principal{UID: r.systemUID, Roles: []string{"admin"}}, "github_integration")
	if err != nil {
		return "", err
	}
	return pt, nil
}

// teamProjectsAdapter encapsula store + GH client + adapter de creación.
type teamProjectsAdapter struct {
	store    *teamprojects.PgStore
	github   *github.Client
	systemUID string
	org      string
}

func newTeamProjectsAdapter(cs *cloudstore.CloudStore, vaultAdpt *vaultAdapter, systemUID string) *teamProjectsAdapter {
	store := teamprojects.NewPgStore(cs.DB())
	org := strings.TrimSpace(os.Getenv("ARIA_CORE_GITHUB_ORG"))
	if org == "" {
		org = "ITECHDEV-MX"
	}
	a := &teamProjectsAdapter{
		store:     store,
		systemUID: systemUID,
		org:       org,
	}
	if vaultAdpt != nil {
		ctx, cancel := contextWithTimeout(15)
		defer cancel()
		resolver := newVaultByNameResolver(vaultAdpt.store, systemUID)
		gh, err := github.NewFromVault(ctx, resolver, org)
		if err != nil {
			log.Printf("[aria-core-cloud] github integration DISABLED: %v", err)
		} else {
			a.github = gh
			log.Printf("[aria-core-cloud] github integration ready (org=%s)", org)
		}
	}
	return a
}

// GitHubClient retorna el *github.Client si está configurado (vault con
// GITHUB_API_TOKEN). Si vault está degraded o el token no se pudo cargar,
// retorna nil. Wave 8 (knowledgebase) lo usa para construir su adapter.
func (a *teamProjectsAdapter) GitHubClient() *github.Client {
	if a == nil {
		return nil
	}
	return a.github
}

// ─── teamprojects.ProjectStore (passthrough) ─────────────────────────────

// Implements cloudserver.TeamProjectsService.
func (a *teamProjectsAdapter) CreateProject(ctx context.Context, p teamprojects.CreateProjectParams) (*teamprojects.Project, error) {
	return a.store.CreateProject(ctx, p)
}
func (a *teamProjectsAdapter) GetProject(ctx context.Context, id string) (*teamprojects.Project, error) {
	return a.store.GetProject(ctx, id)
}
func (a *teamProjectsAdapter) GetProjectBySlug(ctx context.Context, slug string) (*teamprojects.Project, error) {
	return a.store.GetProjectBySlug(ctx, slug)
}
func (a *teamProjectsAdapter) ListProjects(ctx context.Context, f teamprojects.ListFilter) ([]*teamprojects.Project, error) {
	return a.store.ListProjects(ctx, f)
}
func (a *teamProjectsAdapter) UpdateProject(ctx context.Context, id string, u teamprojects.UpdateProjectParams) error {
	return a.store.UpdateProject(ctx, id, u)
}
func (a *teamProjectsAdapter) ArchiveProject(ctx context.Context, id string) error {
	return a.store.ArchiveProject(ctx, id)
}
func (a *teamProjectsAdapter) AddMember(ctx context.Context, projectID, userUID, role, byUID string) error {
	return a.store.AddMember(ctx, projectID, userUID, role, byUID)
}
func (a *teamProjectsAdapter) RemoveMember(ctx context.Context, projectID, userUID string) error {
	return a.store.RemoveMember(ctx, projectID, userUID)
}
func (a *teamProjectsAdapter) ListMembers(ctx context.Context, projectID string) ([]teamprojects.ProjectMember, error) {
	return a.store.ListMembers(ctx, projectID)
}
func (a *teamProjectsAdapter) IsMember(ctx context.Context, projectID, userUID string) (bool, error) {
	return a.store.IsMember(ctx, projectID, userUID)
}
func (a *teamProjectsAdapter) CreateTask(ctx context.Context, p teamprojects.CreateTaskParams) (*teamprojects.Task, error) {
	return a.store.CreateTask(ctx, p)
}
func (a *teamProjectsAdapter) GetTask(ctx context.Context, taskID string) (*teamprojects.Task, error) {
	return a.store.GetTask(ctx, taskID)
}
func (a *teamProjectsAdapter) ListTasksByProject(ctx context.Context, projectID string, f teamprojects.TaskFilter) ([]*teamprojects.Task, error) {
	return a.store.ListTasksByProject(ctx, projectID, f)
}
func (a *teamProjectsAdapter) ListTasksAssignedTo(ctx context.Context, userUID string, f teamprojects.TaskFilter) ([]*teamprojects.Task, error) {
	return a.store.ListTasksAssignedTo(ctx, userUID, f)
}
func (a *teamProjectsAdapter) UpdateTaskStatus(ctx context.Context, taskID, status, byUID string) error {
	return a.store.UpdateTaskStatus(ctx, taskID, status, byUID)
}
func (a *teamProjectsAdapter) UpdateTaskPosition(ctx context.Context, taskID string, newPosition int) error {
	return a.store.UpdateTaskPosition(ctx, taskID, newPosition)
}
func (a *teamProjectsAdapter) AssignTask(ctx context.Context, taskID, userUID, byUID string) error {
	return a.store.AssignTask(ctx, taskID, userUID, byUID)
}
func (a *teamProjectsAdapter) UnassignTask(ctx context.Context, taskID, userUID string) error {
	return a.store.UnassignTask(ctx, taskID, userUID)
}
func (a *teamProjectsAdapter) CloseTask(ctx context.Context, taskID, byUID string) error {
	return a.store.CloseTask(ctx, taskID, byUID)
}
func (a *teamProjectsAdapter) ListTaskAssignees(ctx context.Context, taskID string) ([]string, error) {
	return a.store.ListTaskAssignees(ctx, taskID)
}
func (a *teamProjectsAdapter) LinkObservation(ctx context.Context, taskID, observationID, linkType, byUID string) error {
	return a.store.LinkObservation(ctx, taskID, observationID, linkType, byUID)
}
func (a *teamProjectsAdapter) UnlinkObservation(ctx context.Context, taskID, observationID, linkType string) error {
	return a.store.UnlinkObservation(ctx, taskID, observationID, linkType)
}
func (a *teamProjectsAdapter) ListLinkedObservations(ctx context.Context, taskID string) ([]teamprojects.TaskObservationLink, error) {
	return a.store.ListLinkedObservations(ctx, taskID)
}
func (a *teamProjectsAdapter) LinkSession(ctx context.Context, taskID, sessionID string) error {
	return a.store.LinkSession(ctx, taskID, sessionID)
}
func (a *teamProjectsAdapter) ListLinkedSessions(ctx context.Context, taskID string) ([]teamprojects.TaskSessionLink, error) {
	return a.store.ListLinkedSessions(ctx, taskID)
}
func (a *teamProjectsAdapter) AddComment(ctx context.Context, taskID, authorUID, content string) (*teamprojects.TaskComment, error) {
	return a.store.AddComment(ctx, taskID, authorUID, content)
}
func (a *teamProjectsAdapter) ListComments(ctx context.Context, taskID string) ([]teamprojects.TaskComment, error) {
	return a.store.ListComments(ctx, taskID)
}

// CaptureKnowledge inyecta el GH fetcher si está disponible.
func (a *teamProjectsAdapter) CaptureKnowledge(ctx context.Context, taskID string, opts teamprojects.CaptureKnowledgeOptions) (*teamprojects.KnowledgeCapture, error) {
	if a.github != nil && opts.GHFetcher == nil {
		opts.GHFetcher = ghFetcherAdapter{c: a.github}
	}
	return a.store.CaptureKnowledge(ctx, taskID, opts)
}

// ghFetcherAdapter convierte github.Commit a teamprojects.GHCommit.
type ghFetcherAdapter struct{ c *github.Client }

func (g ghFetcherAdapter) ListCommits(ctx context.Context, owner, repo string, since, until time.Time, author string) ([]teamprojects.GHCommit, error) {
	commits, err := g.c.ListCommits(ctx, owner, repo, since, until, author)
	if err != nil {
		return nil, err
	}
	out := make([]teamprojects.GHCommit, 0, len(commits))
	for _, c := range commits {
		out = append(out, teamprojects.GHCommit{
			SHA:     c.SHA,
			Message: c.CommitObj.Message,
			URL:     c.HTMLURL,
			Author:  c.Author.Login,
			Date:    c.CommitObj.Author.Date,
		})
	}
	return out, nil
}

// CreateProjectWithRepo: crea proyecto + intenta crear repo GH si está habilitado.
func (a *teamProjectsAdapter) CreateProjectWithRepo(ctx context.Context, in cloudserver.CreateTeamProjectInput) (*teamprojects.Project, string, error) {
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = teamprojects.SlugFromName(in.Name)
	}
	params := teamprojects.CreateProjectParams{
		Slug:                slug,
		Name:                in.Name,
		Description:         in.Description,
		ClientID:             in.ClientID,
		GitHubRepoPrivate:   true,
		GitHubDefaultBranch: "main",
		CreatedByUID:        in.CreatedByUID,
	}
	warning := ""
	// Intentar crear/linkear repo si GH disponible y NoGitHub=false.
	if !in.NoGitHub {
		if a.github == nil {
			warning = "Repo GitHub NO creado: GITHUB_API_TOKEN no configurado en vault. Agregalo en /dashboard/vault con name=GITHUB_API_TOKEN, category=api_token, scope=personal."
		} else {
			// 1) Si el repo ya existe en la org → linkear en lugar de crear.
			existing, getErr := a.github.GetRepo(ctx, a.org, slug)
			if getErr == nil && existing != nil {
				params.GitHubRepoURL = existing.HTMLURL
				params.GitHubRepoOwner = existing.Owner.Login
				params.GitHubRepoName = existing.Name
				if existing.DefaultBranch != "" {
					params.GitHubDefaultBranch = existing.DefaultBranch
				}
				warning = fmt.Sprintf("Repo %s/%s ya existía en GitHub — linkeado al proyecto en vez de crear uno nuevo.", a.org, slug)
			} else {
				// 2) No existe → crear nuevo.
				repo, err := a.github.CreateRepo(ctx, github.CreateRepoParams{
					Name:        slug,
					Description: in.Description,
					Private:     true,
					AutoInit:    true,
					GitIgnore:   "Go",
					License:     "mit",
				})
				if err != nil {
					warning = fmt.Sprintf("Repo GitHub falló al crearse (%v); proyecto creado sin repo. Verificá permisos del PAT o creá el repo manualmente y editá el proyecto para linkearlo.", err)
				} else {
					params.GitHubRepoURL = repo.HTMLURL
					params.GitHubRepoOwner = repo.Owner.Login
					params.GitHubRepoName = repo.Name
					if repo.DefaultBranch != "" {
						params.GitHubDefaultBranch = repo.DefaultBranch
					}
				}
			}
		}
	}
	pr, err := a.store.CreateProject(ctx, params)
	if err != nil {
		return nil, "", err
	}
	return pr, warning, nil
}

// contextWithTimeout es un wrapper alrededor de context.WithTimeout para
// uso en el adapter. Devuelve un context con timeout en segundos.
func contextWithTimeout(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}
