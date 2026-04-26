package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClient_RequiresToken(t *testing.T) {
	if _, err := NewClient(Config{Token: ""}); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestNewClient_DefaultsOrg(t *testing.T) {
	c, err := NewClient(Config{Token: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Org() != "ITECHDEV-MX" {
		t.Errorf("default org = %q", c.Org())
	}
}

func TestCreateRepo_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/orgs/ITECHDEV-MX/repos" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testpat" {
			t.Errorf("auth header = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("accept header = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "my-repo" {
			t.Errorf("name = %v", body["name"])
		}
		if body["private"] != true {
			t.Errorf("private = %v", body["private"])
		}
		if body["auto_init"] != true {
			t.Errorf("auto_init = %v", body["auto_init"])
		}
		if body["gitignore_template"] != "Go" {
			t.Errorf("gitignore = %v", body["gitignore_template"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             123,
			"name":           "my-repo",
			"full_name":      "ITECHDEV-MX/my-repo",
			"html_url":       "https://github.com/ITECHDEV-MX/my-repo",
			"private":        true,
			"default_branch": "main",
			"owner":          map[string]any{"login": "ITECHDEV-MX"},
		})
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "testpat", BaseURL: srv.URL, Org: "ITECHDEV-MX"})
	repo, err := c.CreateRepo(context.Background(), CreateRepoParams{
		Name: "my-repo", Description: "test", Private: true, AutoInit: true, GitIgnore: "Go",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.HTMLURL != "https://github.com/ITECHDEV-MX/my-repo" {
		t.Errorf("html_url = %q", repo.HTMLURL)
	}
}

func TestInviteCollaborator_RequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		want := "/repos/foo/bar/collaborators/octocat"
		if r.URL.Path != want {
			t.Errorf("path = %s; want %s", r.URL.Path, want)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["permission"] != "admin" {
			t.Errorf("permission = %v", body["permission"])
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "x", BaseURL: srv.URL})
	if err := c.InviteCollaborator(context.Background(), "foo", "bar", "octocat", "admin"); err != nil {
		t.Errorf("InviteCollaborator: %v", err)
	}
}

func TestListCommits_FiltersAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("author") != "octocat" {
			t.Errorf("author param = %q", q.Get("author"))
		}
		if q.Get("since") == "" || q.Get("until") == "" {
			t.Error("since/until missing")
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"sha":      "abc123",
				"html_url": "https://github.com/foo/bar/commit/abc123",
				"commit": map[string]any{
					"message": "fix bug",
					"author": map[string]any{
						"name":  "Octo",
						"email": "octo@example.com",
						"date":  "2026-01-15T10:00:00Z",
					},
				},
				"author": map[string]any{"login": "octocat"},
			},
		})
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "x", BaseURL: srv.URL})
	commits, err := c.ListCommits(context.Background(), "foo", "bar",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		"octocat")
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) != 1 || commits[0].SHA != "abc123" || commits[0].Author.Login != "octocat" {
		t.Errorf("decode failure: %+v", commits)
	}
}

func TestUnauthorizedMapsToError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "bad", BaseURL: srv.URL})
	_, err := c.CreateRepo(context.Background(), CreateRepoParams{Name: "x"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestNotFoundMapsToError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "x", BaseURL: srv.URL})
	_, err := c.GetIssue(context.Background(), "foo", "bar", 42)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestNewFromVault_PassesThrough(t *testing.T) {
	v := stubVault{tok: "ghp_secret"}
	c, err := NewFromVault(context.Background(), v, "ACME")
	if err != nil {
		t.Fatalf("NewFromVault: %v", err)
	}
	if c.token != "ghp_secret" || c.Org() != "ACME" {
		t.Errorf("decode failure: token=%q org=%q", c.token, c.Org())
	}
}

func TestIssueNumberFromURL(t *testing.T) {
	cases := map[string]int{
		"https://github.com/foo/bar/issues/42": 42,
		"https://github.com/foo/bar/issues/0":  0,
		"https://github.com/foo/bar":           0,
		"":                                     0,
	}
	for in, want := range cases {
		if got := IssueNumberFromURL(in); got != want {
			t.Errorf("IssueNumberFromURL(%q) = %d; want %d", in, got, want)
		}
	}
}

type stubVault struct{ tok string }

func (s stubVault) RevealByName(ctx context.Context, name string) (string, error) {
	if !strings.EqualFold(name, "GITHUB_API_TOKEN") {
		return "", errors.New("not found")
	}
	return s.tok, nil
}
