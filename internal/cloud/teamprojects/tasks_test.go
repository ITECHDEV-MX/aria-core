package teamprojects

import (
	"context"
	"testing"
)

func TestCreateAndListTask(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-tk", Name: "TK", CreatedByUID: uid})
	task, err := s.CreateTask(context.Background(), CreateTaskParams{
		ProjectID: pr.ID, Title: "Implementar login", Priority: "high", CreatedByUID: uid,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != "todo" {
		t.Errorf("status = %q", task.Status)
	}
	tasks, _ := s.ListTasksByProject(context.Background(), pr.ID, TaskFilter{})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-st", Name: "ST", CreatedByUID: uid})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "T", CreatedByUID: uid})
	if err := s.UpdateTaskStatus(context.Background(), task.ID, "in_progress", uid); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _ := s.GetTask(context.Background(), task.ID)
	if got.Status != "in_progress" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestAssignAndListAssigned(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	uid2 := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-as", Name: "AS", CreatedByUID: uid})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "Assigned", CreatedByUID: uid})
	if err := s.AssignTask(context.Background(), task.ID, uid2, uid); err != nil {
		t.Fatalf("assign: %v", err)
	}
	mine, _ := s.ListTasksAssignedTo(context.Background(), uid2, TaskFilter{})
	if len(mine) != 1 {
		t.Errorf("expected 1 assigned task, got %d", len(mine))
	}
	// Idempotent assign.
	if err := s.AssignTask(context.Background(), task.ID, uid2, uid); err != nil {
		t.Errorf("idempotent assign should succeed, got %v", err)
	}
}

func TestCloseTaskCapturesNothingByDefault(t *testing.T) {
	// Sin GH integration y sin observaciones previas, CaptureKnowledge
	// retorna 0,0,0 sin error.
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-cl", Name: "CL", CreatedByUID: uid})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "Close me", CreatedByUID: uid})
	if err := s.AssignTask(context.Background(), task.ID, uid, uid); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseTask(context.Background(), task.ID, uid); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, _ := s.GetTask(context.Background(), task.ID)
	if got.Status != "done" || got.ClosedAt == nil {
		t.Errorf("expected closed, got status=%q closed_at=%v", got.Status, got.ClosedAt)
	}
	cap, err := s.CaptureKnowledge(context.Background(), task.ID, CaptureKnowledgeOptions{LinkedByUID: uid})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if cap.ObservationsLinked != 0 || cap.SessionsLinked != 0 || cap.CommitsLinked != 0 {
		t.Errorf("expected 0 captures sin obs/sessions/gh, got %+v", cap)
	}
}

func TestAddCommentAndList(t *testing.T) {
	s, db, cleanup := setupStore(t)
	defer cleanup()
	uid := seedUser(t, db)
	pr, _ := s.CreateProject(context.Background(), CreateProjectParams{Slug: "test-cmt", Name: "CMT", CreatedByUID: uid})
	task, _ := s.CreateTask(context.Background(), CreateTaskParams{ProjectID: pr.ID, Title: "T", CreatedByUID: uid})
	if _, err := s.AddComment(context.Background(), task.ID, uid, "hola"); err != nil {
		t.Fatalf("comment: %v", err)
	}
	cs, _ := s.ListComments(context.Background(), task.ID)
	if len(cs) != 1 || cs[0].ContentMD != "hola" {
		t.Errorf("expected 1 comment 'hola', got %+v", cs)
	}
}
