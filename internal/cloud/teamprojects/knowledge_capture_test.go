package teamprojects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestKnowledgeCapture_LinksObsAndSessions corre contra DB real:
// inserta una observación + una session del rango y verifica que
// CaptureKnowledge las linkee.
func TestKnowledgeCapture_LinksObsAndSessions(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-kn", Name: "KN", CreatedByUID: uid})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "Knowledge", CreatedByUID: uid})
	_ = s.AssignTask(context.Background(), task.ID, uid, uid)

	// Insertar una sesión y una observación del proyecto/dev/rango.
	sessID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO aria_sessions (id, developer_uid, project, started_at) VALUES ($1, $2::uuid, $3, NOW())`, sessID, uid, pr.Slug); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	defer db.Exec(`DELETE FROM aria_sessions WHERE id=$1`, sessID)

	obsID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO aria_observations (id, session_id, developer_uid, project, title, created_at) VALUES ($1, $2, $3::uuid, $4, $5, NOW())`,
		obsID, sessID, uid, pr.Slug, "obs sample"); err != nil {
		t.Fatalf("seed obs: %v", err)
	}
	defer db.Exec(`DELETE FROM aria_observations WHERE id=$1`, obsID)

	cap, err := s.CaptureKnowledge(context.Background(), task.ID, CaptureKnowledgeOptions{LinkedByUID: uid})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if cap.SessionsLinked != 1 {
		t.Errorf("expected 1 session linked, got %d", cap.SessionsLinked)
	}
	if cap.ObservationsLinked != 1 {
		t.Errorf("expected 1 observation linked, got %d", cap.ObservationsLinked)
	}
}

// TestKnowledgeCapture_NoCommitsWithoutGitHub verifica que sin GHFetcher,
// commits=0.
func TestKnowledgeCapture_NoCommitsWithoutGitHub(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{
		Slug:            "test-kn-2",
		Name:            "KN2",
		GitHubRepoOwner: "foo",
		GitHubRepoName:  "bar",
		CreatedByUID:    uid,
	})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "T", CreatedByUID: uid})

	cap, err := s.CaptureKnowledge(context.Background(), task.ID, CaptureKnowledgeOptions{LinkedByUID: uid})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if cap.CommitsLinked != 0 {
		t.Errorf("expected 0 commits without GHFetcher, got %d", cap.CommitsLinked)
	}
}

// TestKnowledgeCapture_WithGitHubMock usa un GHFetcher mock + ObsSaver mock
// para verificar que commits se capturen correctamente.
func TestKnowledgeCapture_WithGitHubMock(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{
		Slug:            "test-kn-3",
		Name:            "KN3",
		GitHubRepoOwner: "foo",
		GitHubRepoName:  "bar",
		CreatedByUID:    uid,
	})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "T", CreatedByUID: uid})
	_ = s.AssignTask(context.Background(), task.ID, uid, uid)

	mockFetcher := &mockGHFetcher{
		commits: []GHCommit{
			{SHA: "deadbeef", Message: "fix", URL: "https://github.com/foo/bar/commit/deadbeef", Author: "octocat", Date: time.Now()},
			{SHA: "cafef00d", Message: "feat", URL: "https://github.com/foo/bar/commit/cafef00d", Author: "octocat", Date: time.Now()},
		},
	}
	mockSaver := &mockObsSaver{}
	mockResolver := stubResolver{m: map[string]string{uid: "octocat"}}
	cap, err := s.CaptureKnowledge(context.Background(), task.ID, CaptureKnowledgeOptions{
		GHFetcher: mockFetcher, ObsSaver: mockSaver, GHResolver: mockResolver, LinkedByUID: uid,
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if cap.CommitsLinked != 2 {
		t.Errorf("expected 2 commits linked, got %d", cap.CommitsLinked)
	}
}

// ─── mocks ──────────────────────────────────────────────────────────────

type mockGHFetcher struct{ commits []GHCommit }

func (m *mockGHFetcher) ListCommits(ctx context.Context, owner, repo string, since, until time.Time, author string) ([]GHCommit, error) {
	return m.commits, nil
}

type mockObsSaver struct{ count int }

func (m *mockObsSaver) SaveCommitAsObservation(ctx context.Context, in CommitObservationInput) (string, error) {
	m.count++
	return uuid.NewString(), nil
}

type stubResolver struct{ m map[string]string }

func (s stubResolver) GitHubUsername(ctx context.Context, uid string) (string, error) {
	if v, ok := s.m[uid]; ok {
		return v, nil
	}
	return "", nil
}
