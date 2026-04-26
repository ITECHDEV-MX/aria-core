package comments

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestMentionRegex_AcceptsUUIDFormat verifica que la regex matchea sólo UUIDs
// canonical. Necesario porque el store sólo persiste mentions para UIDs válidos.
func TestMentionRegex_AcceptsUUIDFormat(t *testing.T) {
	uid := uuid.NewString()
	mentions := ParseMentions("hi @" + uid + " hola")
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}
	if !mentions[0].IsUID {
		t.Error("expected IsUID=true for canonical UUID")
	}
}

func TestMentionRegex_AcceptsUsername(t *testing.T) {
	mentions := ParseMentions("@bob heads up")
	if len(mentions) != 1 || mentions[0].IsUID {
		t.Errorf("expected username mention, got %v", mentions)
	}
}

func TestMentionRegex_MultipleInOneText(t *testing.T) {
	a := uuid.NewString()
	b := uuid.NewString()
	text := "@" + a + " ping @" + b + " also @charlie"
	mentions := ParseMentions(text)
	if len(mentions) != 3 {
		t.Errorf("expected 3 mentions, got %d", len(mentions))
	}
	uids := UniqueUIDs(mentions)
	if len(uids) != 2 {
		t.Errorf("expected 2 unique UIDs, got %d", len(uids))
	}
}

// fakeNotifier captura llamadas SendMentionNotification para verificación.
type fakeNotifier struct {
	configured bool
	calls      []MentionEmailContext
}

func (f *fakeNotifier) IsConfigured() bool { return f.configured }
func (f *fakeNotifier) PublicURL() string  { return "https://test.local" }
func (f *fakeNotifier) SendMentionNotification(ctx context.Context, mc MentionEmailContext) error {
	f.calls = append(f.calls, mc)
	return nil
}

type fakeUserResolver struct{ email, name string }

func (r *fakeUserResolver) GetByUID(ctx context.Context, uid string) (string, string, error) {
	return r.email, r.name, nil
}

// TestNotifyMentioned_DispatchesAndMarks (integration) verifica el flujo
// end-to-end: crear comment con mention, llamar NotifyMentioned, ver que el
// notifier fue invocado con el contexto correcto y notified_at se setea.
func TestNotifyMentioned_DispatchesAndMarks(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	target := uuid.NewString()

	c, err := s.Create(context.Background(), CreateParams{
		PageID: pageID, AuthorUID: a, ContentMD: "Hi @" + target + " urgent",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	notifier := &fakeNotifier{configured: true}
	users := &fakeUserResolver{email: "x@y.com", name: "Target"}

	if err := s.NotifyMentioned(context.Background(), c.ID, notifier, users, "Page", "https://t/page", "Author"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(notifier.calls) != 1 {
		t.Errorf("expected 1 send, got %d", len(notifier.calls))
	}
	if notifier.calls[0].ToEmail != "x@y.com" {
		t.Errorf("wrong to email: %v", notifier.calls[0])
	}
	if notifier.calls[0].PageTitle != "Page" {
		t.Errorf("wrong page title")
	}

	// Llamar otra vez no debe re-enviar (notified_at != NULL).
	if err := s.NotifyMentioned(context.Background(), c.ID, notifier, users, "Page", "https://t/page", "Author"); err != nil {
		t.Fatalf("notify2: %v", err)
	}
	if len(notifier.calls) != 1 {
		t.Errorf("expected idempotent (1 call), got %d", len(notifier.calls))
	}
}

func TestNotifyMentioned_DegradedWhenEmailNotConfigured(t *testing.T) {
	s, cleanup := setupStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	a := uuid.NewString()
	target := uuid.NewString()
	c, err := s.Create(context.Background(), CreateParams{
		PageID: pageID, AuthorUID: a, ContentMD: "@" + target + " heads",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	notifier := &fakeNotifier{configured: false}
	if err := s.NotifyMentioned(context.Background(), c.ID, notifier, &fakeUserResolver{}, "P", "U", "by"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(notifier.calls) != 0 {
		t.Errorf("expected 0 sends in degraded mode, got %d", len(notifier.calls))
	}
}

// TestErrors confirma que los errores públicos están definidos.
func TestErrors(t *testing.T) {
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Error("ErrNotFound should be self-Is")
	}
	if !errors.Is(ErrForbidden, ErrForbidden) {
		t.Error("ErrForbidden should be self-Is")
	}
	if !errors.Is(ErrInvalidInput, ErrInvalidInput) {
		t.Error("ErrInvalidInput should be self-Is")
	}
}
