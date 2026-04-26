package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ─── stub PersonalCockpitService ────────────────────────────────────────────

type stubPersonalCockpitSvc struct {
	statsByUID         map[string]PersonalCockpitStats
	openByUID          map[string][]OpenSessionView
	heatmapByUID       map[string][]HeatmapDay
	timelineByUID      map[string][]TimelineEvent
	pendingByUID       map[string]PendingItems
	contributionsByUID map[string]ContributionsView
	// Session ownership: sessionID -> ownerUID
	sessionOwners      map[string]string
	resumedSessions    []string
	closedSessions     []string
	closeErr           error
}

func (s *stubPersonalCockpitSvc) Stats(_ context.Context, uid string, _ time.Time) (PersonalCockpitStats, error) {
	if v, ok := s.statsByUID[uid]; ok {
		return v, nil
	}
	return PersonalCockpitStats{}, nil
}

func (s *stubPersonalCockpitSvc) OpenSessions(_ context.Context, uid string) ([]OpenSessionView, error) {
	return s.openByUID[uid], nil
}

func (s *stubPersonalCockpitSvc) Heatmap(_ context.Context, uid string, _ time.Time) ([]HeatmapDay, error) {
	return s.heatmapByUID[uid], nil
}

func (s *stubPersonalCockpitSvc) Timeline(_ context.Context, uid string, _ int) ([]TimelineEvent, error) {
	return s.timelineByUID[uid], nil
}

func (s *stubPersonalCockpitSvc) Pending(_ context.Context, uid string, _ time.Time) (PendingItems, error) {
	return s.pendingByUID[uid], nil
}

func (s *stubPersonalCockpitSvc) Contributions(_ context.Context, uid string) (ContributionsView, error) {
	return s.contributionsByUID[uid], nil
}

func (s *stubPersonalCockpitSvc) IsSessionOwner(_ context.Context, sessionID, devUID string) (bool, error) {
	owner, ok := s.sessionOwners[sessionID]
	if !ok {
		return false, nil
	}
	return owner == devUID, nil
}

func (s *stubPersonalCockpitSvc) ResumeSession(_ context.Context, sessionID, _ string) (string, error) {
	s.resumedSessions = append(s.resumedSessions, sessionID)
	return "proj-cockpit", nil
}

func (s *stubPersonalCockpitSvc) CloseSession(_ context.Context, sessionID, _, _ string) error {
	if s.closeErr != nil {
		return s.closeErr
	}
	s.closedSessions = append(s.closedSessions, sessionID)
	return nil
}

// ─── tests ──────────────────────────────────────────────────────────────────

func TestPersonalCockpitVMBuildsWithMockData(t *testing.T) {
	now := time.Now()
	svc := &stubPersonalCockpitSvc{
		statsByUID: map[string]PersonalCockpitStats{
			"alan-uid": {
				SessionsThisWeek: 5,
				SessionsLastWeek: 2,
				ObservationsWeek: 7,
				ObservationsByType: map[string]int{
					"decision": 3, "learning": 2, "bugfix": 2,
				},
				TopSkills: []TopSkill{
					{ID: "s1", Name: "go-postgres", Count: 12},
					{ID: "s2", Name: "templ-htmx", Count: 8},
				},
				ActiveMinutes: 540,
			},
		},
		openByUID: map[string][]OpenSessionView{
			"alan-uid": {
				{ID: "sess-1", Project: "proj-x", Goal: "Fix dashboard bug",
					StartedAt: now.Add(-3 * time.Hour), LastActivityAt: now.Add(-30 * time.Minute)},
				{ID: "sess-2", Project: "proj-y", Goal: "Add feature",
					StartedAt: now.Add(-72 * time.Hour), LastActivityAt: now.Add(-50 * time.Hour),
					Zombi: true},
			},
		},
		heatmapByUID: map[string][]HeatmapDay{
			"alan-uid": {
				{Date: now.AddDate(0, 0, -3), Saves: 3, Sessions: 1},
				{Date: now.AddDate(0, 0, -1), Saves: 7, Sessions: 2},
			},
		},
		timelineByUID: map[string][]TimelineEvent{
			"alan-uid": {
				{Kind: "session", Action: "Sesión iniciada", Title: "proj-x",
					OccurredAt: now.Add(-1 * time.Hour), Icon: "▶", Variant: "muted"},
				{Kind: "observation", Action: "Observación guardada",
					Title: "ADR multi-tenant", Subtitle: "decision",
					OccurredAt: now.Add(-25 * time.Hour), Icon: "💡", Variant: "success"},
			},
		},
		pendingByUID: map[string]PendingItems{
			"alan-uid": {
				DraftQuotes: []PendingQuote{
					{ID: "q1", LeadName: "Acme", Currency: "MXN", Total: 50000},
				},
			},
		},
		contributionsByUID: map[string]ContributionsView{
			"alan-uid": {
				TopCanonObservations: []TopObservation{
					{ID: "o1", Title: "Patron de retry", RelevanceCount: 12},
				},
				TotalSaves:     220,
				RankPercentile: 25,
				RankLabel:      "top 25%",
			},
		},
	}

	h := &handlers{cfg: MountConfig{PersonalCockpit: svc}}
	p := Principal{uid: "alan-uid", displayName: "Alan", roles: []string{"dev"}}

	vm := h.buildPersonalCockpitVM(context.Background(), p)

	if !vm.HasService {
		t.Fatalf("expected HasService=true with svc configured")
	}
	if vm.Username != "Alan" {
		t.Errorf("Username: got %q want %q", vm.Username, "Alan")
	}
	if vm.Stats.SessionsThisWeek != 5 || vm.Stats.SessionsDelta() != 3 {
		t.Errorf("Stats sessions/delta: got %d/%d want 5/3",
			vm.Stats.SessionsThisWeek, vm.Stats.SessionsDelta())
	}
	if got := vm.Stats.ActiveHours(); got < 8.9 || got > 9.1 {
		t.Errorf("ActiveHours: got %.2f want ~9.0", got)
	}
	if len(vm.OpenSessions) != 2 {
		t.Errorf("OpenSessions len=%d want 2", len(vm.OpenSessions))
	} else if !vm.OpenSessions[1].Zombi {
		t.Errorf("expected sess-2 to be Zombi=true")
	}
	if len(vm.Heatmap) != 365 {
		t.Errorf("Heatmap len=%d want 365 (always padded to 365 days)", len(vm.Heatmap))
	}
	// Confirm heatmap data was merged into the right days.
	totalSaves := 0
	totalSessions := 0
	for _, d := range vm.Heatmap {
		totalSaves += d.Saves
		totalSessions += d.Sessions
	}
	if totalSaves != 10 || totalSessions != 3 {
		t.Errorf("Heatmap merge: saves=%d (want 10), sessions=%d (want 3)", totalSaves, totalSessions)
	}
	if len(vm.Timeline) != 2 {
		t.Errorf("Timeline len=%d want 2", len(vm.Timeline))
	}
	if len(vm.Pending.DraftQuotes) != 1 {
		t.Errorf("Pending DraftQuotes len=%d want 1", len(vm.Pending.DraftQuotes))
	}
	if vm.Contributions.RankLabel != "top 25%" {
		t.Errorf("RankLabel: got %q want %q", vm.Contributions.RankLabel, "top 25%")
	}
}

func TestPersonalCockpitVMWithoutServiceShowsNotice(t *testing.T) {
	h := &handlers{cfg: MountConfig{PersonalCockpit: nil}}
	p := Principal{uid: "any-uid", displayName: "Anon"}
	vm := h.buildPersonalCockpitVM(context.Background(), p)
	if vm.HasService {
		t.Errorf("expected HasService=false when svc is nil")
	}
	if strings.TrimSpace(vm.Notice) == "" {
		t.Errorf("expected non-empty Notice when svc is nil")
	}
	if len(vm.Heatmap) != 365 {
		t.Errorf("Heatmap padded len=%d want 365 even without service", len(vm.Heatmap))
	}
}

func TestPersonalCockpitPageRendersWithEmptyVM(t *testing.T) {
	// Smoke test: render an empty VM via the full handler path and confirm
	// templ renders without panic and returns 200 OK.
	now := time.Now()
	svc := &stubPersonalCockpitSvc{}

	mux := http.NewServeMux()
	Mount(mux, MountConfig{
		RequireSession: func(_ *http.Request) error { return nil },
		IsAdmin:        func(_ *http.Request) bool { return false },
		GetRoles:       func(_ *http.Request) []string { return []string{"dev"} },
		GetDisplayName: func(_ *http.Request) string { return "Alan" },
		GetUID:         func(_ *http.Request) string { return "alan-uid" },
		PersonalCockpit: svc,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d, body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Mi cockpit") && !strings.Contains(body, "MI ACTIVIDAD") {
		preview := body
		if len(preview) > 300 {
			preview = preview[:300]
		}
		t.Errorf("expected cockpit identity in body, got=%q", preview)
	}
	if !strings.Contains(body, "Sesiones esta semana") {
		t.Errorf("expected stat labels rendered")
	}
	if !strings.Contains(body, "cockpit-heatmap") {
		t.Errorf("expected heatmap SVG class present")
	}
	if !strings.Contains(body, "Timeline reciente") {
		t.Errorf("expected timeline section present")
	}
	if !strings.Contains(body, "Lo que dejaste pendiente") {
		t.Errorf("expected pending section present")
	}

	// Also exercise relativeTime / formatDateES indirectly by passing now.
	_ = now
}

func TestPersonalCockpitRedirectsWhenNoUID(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, MountConfig{
		RequireSession: func(_ *http.Request) error { return nil },
		// Intentional: no GetUID configured.
		GetDisplayName: func(_ *http.Request) string { return "Alan" },
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect to login, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/dashboard/login") {
		t.Errorf("expected Location to contain /dashboard/login, got %q", loc)
	}
}

func TestSessionResumeRequiresOwnership(t *testing.T) {
	svc := &stubPersonalCockpitSvc{
		sessionOwners: map[string]string{
			"sess-owned":     "alan-uid",
			"sess-not-mine":  "bob-uid",
		},
	}
	mux := http.NewServeMux()
	Mount(mux, MountConfig{
		RequireSession:  func(_ *http.Request) error { return nil },
		GetUID:          func(_ *http.Request) string { return "alan-uid" },
		GetDisplayName:  func(_ *http.Request) string { return "Alan" },
		PersonalCockpit: svc,
	})

	t.Run("owner can resume", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/sess-owned/resume", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("expected 303 redirect, got %d body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); !strings.Contains(got, "proj-cockpit") {
			t.Errorf("expected redirect to project, got Location=%q", got)
		}
		if len(svc.resumedSessions) != 1 || svc.resumedSessions[0] != "sess-owned" {
			t.Errorf("expected ResumeSession called once with sess-owned, got %v", svc.resumedSessions)
		}
	})

	t.Run("non-owner gets 403", func(t *testing.T) {
		svc.resumedSessions = nil
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/sess-not-mine/resume", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 for non-owner, got %d body=%s", rec.Code, rec.Body.String())
		}
		if len(svc.resumedSessions) != 0 {
			t.Errorf("ResumeSession should not be called when ACL fails, got %v", svc.resumedSessions)
		}
	})
}

func TestSessionCloseRequiresOwnership(t *testing.T) {
	svc := &stubPersonalCockpitSvc{
		sessionOwners: map[string]string{
			"sess-mine":     "alan-uid",
			"sess-not-mine": "bob-uid",
		},
	}
	mux := http.NewServeMux()
	Mount(mux, MountConfig{
		RequireSession:  func(_ *http.Request) error { return nil },
		GetUID:          func(_ *http.Request) string { return "alan-uid" },
		GetDisplayName:  func(_ *http.Request) string { return "Alan" },
		PersonalCockpit: svc,
	})

	t.Run("owner closes with summary", func(t *testing.T) {
		form := url.Values{}
		form.Set("summary", "wrap up notes")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/sess-mine/close",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("expected 303 redirect, got %d body=%s", rec.Code, rec.Body.String())
		}
		if len(svc.closedSessions) != 1 || svc.closedSessions[0] != "sess-mine" {
			t.Errorf("expected CloseSession called once with sess-mine, got %v", svc.closedSessions)
		}
	})

	t.Run("non-owner gets 403", func(t *testing.T) {
		svc.closedSessions = nil
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/sess-not-mine/close",
			strings.NewReader("summary=foo"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 for non-owner, got %d body=%s", rec.Code, rec.Body.String())
		}
		if len(svc.closedSessions) != 0 {
			t.Errorf("CloseSession should not be called when ACL fails, got %v", svc.closedSessions)
		}
	})

	t.Run("close errors propagate as 500/404", func(t *testing.T) {
		svc.closeErr = ErrSessionNotFound
		defer func() { svc.closeErr = nil }()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/sess-mine/close",
			strings.NewReader("summary=foo"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 when service returns ErrSessionNotFound, got %d", rec.Code)
		}
	})

	t.Run("generic close error 500", func(t *testing.T) {
		svc.closeErr = errors.New("db down")
		defer func() { svc.closeErr = nil }()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/sess-mine/close",
			strings.NewReader("summary=foo"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 on generic close error, got %d", rec.Code)
		}
	})
}

func TestHeatmapBucketScale(t *testing.T) {
	cases := []struct {
		total int
		want  int
	}{
		{0, 0}, {1, 1}, {2, 1}, {3, 2}, {5, 2}, {6, 3}, {10, 3}, {11, 4}, {99, 4},
	}
	for _, c := range cases {
		d := HeatmapDay{Saves: c.total}
		if got := d.Bucket(); got != c.want {
			t.Errorf("total=%d bucket=%d want=%d", c.total, got, c.want)
		}
	}
}

func TestRelativeTimeFormatting(t *testing.T) {
	now := time.Now()
	cases := []struct {
		ago      time.Duration
		contains string
	}{
		{30 * time.Second, "segundos"},
		{1 * time.Minute, "1 minuto"},
		{5 * time.Minute, "5 minutos"},
		{1*time.Hour + 1*time.Minute, "hora"},
		{3 * time.Hour, "horas"},
		{25 * time.Hour, "1 día"},
		{50 * time.Hour, "días"},
	}
	for _, c := range cases {
		t.Run(c.contains, func(t *testing.T) {
			got := relativeTime(now.Add(-c.ago))
			if !strings.Contains(got, c.contains) {
				t.Errorf("ago=%v: got %q want contains %q", c.ago, got, c.contains)
			}
		})
	}
	if got := relativeTime(time.Time{}); got != "—" {
		t.Errorf("zero time: got %q want '—'", got)
	}
}

func TestGroupTimelineByDay(t *testing.T) {
	base := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	events := []TimelineEvent{
		{Action: "a", OccurredAt: base},
		{Action: "b", OccurredAt: base.Add(2 * time.Hour)},
		{Action: "c", OccurredAt: base.AddDate(0, 0, -1)},
		{Action: "d", OccurredAt: base.AddDate(0, 0, -2)},
	}
	groups := groupTimelineByDay(events)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	// Groups should be sorted by date desc (newest first).
	if len(groups[0].Events) != 2 {
		t.Errorf("first group should have 2 events (today), got %d", len(groups[0].Events))
	}
}

