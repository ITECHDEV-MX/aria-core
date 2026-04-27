package main

import (
	"context"
	"fmt"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/github"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/knowledgebase"
)

// kbGitHubAdapter implementa knowledgebase.GitHubLike envolviendo
// github.Client (wave 7) con los métodos de archivos (Get/Put/List)
// agregados en este merge.
type kbGitHubAdapter struct {
	c *github.Client
}

func newKBGitHubAdapter(c *github.Client) knowledgebase.GitHubLike {
	if c == nil {
		return nil
	}
	return &kbGitHubAdapter{c: c}
}

func (a *kbGitHubAdapter) GetRepo(ctx context.Context, owner, repo string) (*knowledgebase.GitHubRepoInfo, error) {
	r, err := a.c.GetRepo(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return repoToInfo(r), nil
}

func (a *kbGitHubAdapter) CreateRepo(ctx context.Context, owner, repo, description string, private bool) (*knowledgebase.GitHubRepoInfo, error) {
	if owner != "" && owner != a.c.Org() {
		return nil, fmt.Errorf("kb-github: owner %q != configured org %q (CreateRepo solo soporta el org del cliente)", owner, a.c.Org())
	}
	r, err := a.c.CreateRepo(ctx, github.CreateRepoParams{
		Name:        repo,
		Description: description,
		Private:     private,
		AutoInit:    true,
	})
	if err != nil {
		return nil, err
	}
	return repoToInfo(r), nil
}

func (a *kbGitHubAdapter) GetFile(ctx context.Context, owner, repo, branch, path string) (*knowledgebase.GitHubFile, error) {
	f, err := a.c.GetFile(ctx, owner, repo, branch, path)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	return &knowledgebase.GitHubFile{
		Path:    f.Path,
		Content: []byte(f.Content),
		SHA:     f.SHA,
		Size:    f.Size,
	}, nil
}

func (a *kbGitHubAdapter) PutFile(ctx context.Context, req knowledgebase.GitHubPutFileRequest) (*knowledgebase.GitHubPutFileResult, error) {
	prevSHA := req.PreviousSHA
	if prevSHA == "" {
		// Idempotencia: si el archivo ya existe sin que el caller nos diera
		// SHA, leemos para extraerlo. Si el contenido es idéntico, retornamos
		// el SHA actual sin commit (evita commits ruidosos en re-syncs).
		existing, err := a.c.GetFile(ctx, req.Owner, req.Repo, req.Branch, req.Path)
		if err != nil {
			return nil, fmt.Errorf("kb-github: pre-put GetFile %s: %w", req.Path, err)
		}
		if existing != nil {
			if existing.Content == string(req.Content) {
				return &knowledgebase.GitHubPutFileResult{
					CommitSHA: "",
					BlobSHA:   existing.SHA,
					HTMLURL:   existing.HTMLURL,
				}, nil
			}
			prevSHA = existing.SHA
		}
	}
	res, err := a.c.PutFile(ctx, req.Owner, req.Repo, req.Branch, req.Path, req.Content, req.CommitMessage, prevSHA)
	if err != nil {
		return nil, err
	}
	return &knowledgebase.GitHubPutFileResult{
		CommitSHA: res.Commit.SHA,
		BlobSHA:   res.Content.SHA,
		HTMLURL:   res.Content.HTMLURL,
	}, nil
}

func (a *kbGitHubAdapter) ListFiles(ctx context.Context, owner, repo, branch, path string) ([]knowledgebase.GitHubFileEntry, error) {
	entries, err := a.c.ListFiles(ctx, owner, repo, branch, path)
	if err != nil {
		return nil, err
	}
	out := make([]knowledgebase.GitHubFileEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, knowledgebase.GitHubFileEntry{
			Path: e.Path,
			Type: e.Type,
			SHA:  e.SHA,
			Size: e.Size,
		})
	}
	return out, nil
}

func repoToInfo(r *github.Repo) *knowledgebase.GitHubRepoInfo {
	if r == nil {
		return nil
	}
	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	return &knowledgebase.GitHubRepoInfo{
		Owner:         r.Owner.Login,
		Name:          r.Name,
		DefaultBranch: branch,
		Private:       r.Private,
		HTMLURL:       r.HTMLURL,
	}
}
