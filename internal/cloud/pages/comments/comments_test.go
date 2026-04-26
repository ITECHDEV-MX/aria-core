package comments

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ─── Pure-unit tests ────────────────────────────────────────────────────────

func TestParseMentions_UID(t *testing.T) {
	uid := uuid.NewString()
	content := "hola @" + uid + " y @bob"
	mentions := ParseMentions(content)
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(mentions))
	}
	uidM := mentions[0]
	if !uidM.IsUID || uidM.Token != uid {
		t.Errorf("first should be UID; got %+v", uidM)
	}
	bobM := mentions[1]
	if bobM.IsUID || bobM.Token != "bob" {
		t.Errorf("second should be username; got %+v", bobM)
	}
}

func TestParseMentions_NoMentions(t *testing.T) {
	if got := ParseMentions("just text without ats"); len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestUniqueUIDs_DedupAndFilter(t *testing.T) {
	a := uuid.NewString()
	b := uuid.NewString()
	mentions := []Mention{
		{Token: a, IsUID: true},
		{Token: a, IsUID: true},
		{Token: "user1", IsUID: false},
		{Token: b, IsUID: true},
	}
	out := UniqueUIDs(mentions)
	if len(out) != 2 {
		t.Errorf("expected 2 unique UIDs, got %d: %v", len(out), out)
	}
}

func TestParseMentions_EmailLikeIsTreatedAsUsername(t *testing.T) {
	mentions := ParseMentions("hi @alice@example.com")
	// La regex no captura el dominio post-@, así que mention.Token = "alice"
	if len(mentions) == 0 {
		t.Fatal("expected at least one mention")
	}
	if mentions[0].Token != "alice" {
		t.Errorf("expected alice, got %q", mentions[0].Token)
	}
}

// ─── Integration tests (require ARIA_CORE_TEST_DSN) ─────────────────────────

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_TEST_DSN"))
	if dsn == "" {
		t.Skip("ARIA_CORE_TEST_DSN not set; skipping integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("ping db failed: %v", err)
	}
	ddls := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,
		`CREATE TABLE IF NOT EXISTS aria_page_comments (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			page_id UUID NOT NULL,
			block_anchor TEXT,
			parent_comment_id UUID REFERENCES aria_page_comments(id) ON DELETE CASCADE,
			content_md TEXT NOT NULL,
			author_uid UUID NOT NULL,
			is_resolved BOOLEAN NOT NULL DEFAULT FALSE,
			resolved_by_uid UUID,
			resolved_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS aria_page_mentions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			comment_id UUID NOT NULL REFERENCES aria_page_comments(id) ON DELETE CASCADE,
			mentioned_uid UUID NOT NULL,
			notified_at TIMESTAMPTZ,
			read_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, q := range ddls {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return db
}

func setupStore(t *testing.T) (*Store, func()) {
	t.Helper()
	db := openTestDB(t)
	s := New(db)
	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM aria_page_mentions`)
		_, _ = db.Exec(`DELETE FROM aria_page_comments`)
		_ = db.Close()
	}
	return s, cleanup
}

func TestIntegration_CreateAndGet(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	authorUID := uuid.NewString()
	mentionedUID := uuid.NewString()

	c, err := s.Create(context.Background(), CreateParams{
		PageID:    pageID,
		ContentMD: "Buen punto @" + mentionedUID,
		AuthorUID: authorUID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == "" {
		t.Fatal("missing id")
	}
	if len(c.Mentions) != 1 || c.Mentions[0] != mentionedUID {
		t.Errorf("expected 1 mention=%s, got %v", mentionedUID, c.Mentions)
	}

	got, err := s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ContentMD != c.ContentMD {
		t.Errorf("content mismatch")
	}
}

func TestIntegration_Threading(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()

	parent, err := s.Create(context.Background(), CreateParams{PageID: pageID, ContentMD: "top", AuthorUID: a})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Create(context.Background(), CreateParams{
			PageID:          pageID,
			ParentCommentID: parent.ID,
			ContentMD:       "reply",
			AuthorUID:       a,
		}); err != nil {
			t.Fatalf("reply: %v", err)
		}
	}
	tops, err := s.ListPage(context.Background(), pageID, ListPageOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tops) != 1 {
		t.Errorf("expected 1 top-level, got %d", len(tops))
	}
	if tops[0].ReplyCount != 3 {
		t.Errorf("expected reply count=3, got %d", tops[0].ReplyCount)
	}
	replies, err := s.ListReplies(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("replies: %v", err)
	}
	if len(replies) != 3 {
		t.Errorf("expected 3 replies, got %d", len(replies))
	}
}

func TestIntegration_ResolveFlow(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	c, _ := s.Create(context.Background(), CreateParams{PageID: pageID, ContentMD: "issue", AuthorUID: a})

	if err := s.Resolve(context.Background(), c.ID, a); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, _ := s.Get(context.Background(), c.ID)
	if !got.IsResolved {
		t.Error("expected resolved")
	}
	if got.ResolvedByUID != a {
		t.Errorf("expected by=%s, got %s", a, got.ResolvedByUID)
	}

	resolvedList, _ := s.ListPage(context.Background(), pageID, ListPageOpts{OnlyResolved: true})
	if len(resolvedList) != 1 {
		t.Errorf("expected 1 resolved, got %d", len(resolvedList))
	}
	unresolvedList, _ := s.ListPage(context.Background(), pageID, ListPageOpts{OnlyUnresolved: true})
	if len(unresolvedList) != 0 {
		t.Errorf("expected 0 unresolved, got %d", len(unresolvedList))
	}

	if err := s.Unresolve(context.Background(), c.ID); err != nil {
		t.Fatalf("unresolve: %v", err)
	}
	got2, _ := s.Get(context.Background(), c.ID)
	if got2.IsResolved {
		t.Error("expected unresolved")
	}
}

func TestIntegration_UpdateContent_OnlyAuthor(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	other := uuid.NewString()
	c, _ := s.Create(context.Background(), CreateParams{PageID: pageID, ContentMD: "v1", AuthorUID: a})

	if _, err := s.UpdateContent(context.Background(), c.ID, other, "v2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("expected forbidden, got %v", err)
	}
	if _, err := s.UpdateContent(context.Background(), c.ID, a, "v2"); err != nil {
		t.Fatalf("author update: %v", err)
	}
	got, _ := s.Get(context.Background(), c.ID)
	if got.ContentMD != "v2" {
		t.Errorf("expected v2, got %q", got.ContentMD)
	}
}

func TestIntegration_DeleteCascadesMentions(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	mentioned := uuid.NewString()
	c, _ := s.Create(context.Background(), CreateParams{
		PageID: pageID, ContentMD: "@" + mentioned + " heads up", AuthorUID: a,
	})
	if err := s.Delete(context.Background(), c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// La mention debería haber cascadeado.
	mentions, _ := s.ListMentions(context.Background(), mentioned, false, 10)
	if len(mentions) != 0 {
		t.Errorf("expected mentions cascaded, got %d", len(mentions))
	}
}

func TestIntegration_MentionsResolverFromUsername(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	target := uuid.NewString()
	c, err := s.Create(context.Background(), CreateParams{
		PageID:    pageID,
		AuthorUID: a,
		ContentMD: "@bob check this",
		MentionResolver: func(name string) string {
			if name == "bob" {
				return target
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(c.Mentions) != 1 || c.Mentions[0] != target {
		t.Errorf("expected resolved mention=%s, got %v", target, c.Mentions)
	}
}

func TestIntegration_CountsAndMarkRead(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	target := uuid.NewString()
	if _, err := s.Create(context.Background(), CreateParams{
		PageID: pageID, AuthorUID: a, ContentMD: "@" + target + " a",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Create(context.Background(), CreateParams{
		PageID: pageID, AuthorUID: a, ContentMD: "@" + target + " b",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	n, err := s.CountUnreadMentions(context.Background(), target)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 unread, got %d", n)
	}
	if err := s.MarkMentionsRead(context.Background(), target, pageID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	n2, _ := s.CountUnreadMentions(context.Background(), target)
	if n2 != 0 {
		t.Errorf("expected 0 unread after mark, got %d", n2)
	}
}
