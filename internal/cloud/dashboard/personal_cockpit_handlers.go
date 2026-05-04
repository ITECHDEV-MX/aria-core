package dashboard

import (
	"github.com/ITECHDEV-MX/aria-core/internal/obs"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ─── PersonalCockpitService contract ────────────────────────────────────────

// PersonalCockpitService is the contract used by the dashboard to power
// /dashboard/me. The implementation lives in a separate package and queries
// aria_sessions / aria_observations / aria_skills / cotizador_* directly.
type PersonalCockpitService interface {
	// Stats — métricas resumen del dev (sesiones, obs, skills, tiempo activo).
	Stats(ctx context.Context, devUID string, now time.Time) (PersonalCockpitStats, error)
	// OpenSessions — sesiones del dev sin ended_at, ordenadas por started_at DESC.
	OpenSessions(ctx context.Context, devUID string) ([]OpenSessionView, error)
	// Heatmap — actividad diaria del dev (saves+sesiones por día) últimos 365 días.
	Heatmap(ctx context.Context, devUID string, until time.Time) ([]HeatmapDay, error)
	// Timeline — últimos N eventos cronológicos (sesiones, obs, skills, leads, quotes).
	Timeline(ctx context.Context, devUID string, limit int) ([]TimelineEvent, error)
	// Pending — items que el dev dejó abiertos (sesiones zombi, leads inactivos, quotes draft).
	Pending(ctx context.Context, devUID string, now time.Time) (PendingItems, error)
	// Contributions — top observaciones canon, skills usados, ranking de saves.
	Contributions(ctx context.Context, devUID string) (ContributionsView, error)

	// Session ownership / actions.
	// IsSessionOwner returns true iff devUID == aria_sessions.developer_uid.
	IsSessionOwner(ctx context.Context, sessionID, devUID string) (bool, error)
	// ResumeSession refreshes activity timestamps; returns project for redirect.
	ResumeSession(ctx context.Context, sessionID, devUID string) (project string, err error)
	// CloseSession persists a summary and marks ended_at.
	CloseSession(ctx context.Context, sessionID, devUID, summary string) error
}

// ─── PersonalCockpit ViewModels ─────────────────────────────────────────────

// PersonalCockpitVM is the full view model rendered by the /dashboard/me page.
type PersonalCockpitVM struct {
	Username      string
	Today         time.Time
	Stats         PersonalCockpitStats
	OpenSessions  []OpenSessionView
	Heatmap       []HeatmapDay
	Timeline      []TimelineEvent
	Pending       PendingItems
	Contributions ContributionsView
	HasService    bool   // false → service no configurado, vista degradada
	Notice        string // mensaje informativo (e.g. "service unavailable")
}

// PersonalCockpitStats — 4 stat cards.
type PersonalCockpitStats struct {
	SessionsThisWeek int            // count of aria_sessions started this ISO week
	SessionsLastWeek int            // count of aria_sessions started prior week
	ObservationsWeek int            // count of aria_observations created this week
	ObservationsByType map[string]int // breakdown {decision: 3, learning: 2, ...}
	TopSkills        []TopSkill     // top 3 skills used last 30d (from aria_skills usage proxy)
	ActiveMinutes    int64          // approx time = sum(last_summary - started_at)
}

// SessionsDelta returns the delta vs last week (positive = improvement).
func (s PersonalCockpitStats) SessionsDelta() int {
	return s.SessionsThisWeek - s.SessionsLastWeek
}

// ActiveHours returns active minutes as hours rounded to 1 decimal.
func (s PersonalCockpitStats) ActiveHours() float64 {
	return float64(s.ActiveMinutes) / 60.0
}

// TopSkill — un skill usado recientemente con un contador de uso.
type TopSkill struct {
	ID    string
	Name  string
	Count int
}

// OpenSessionView — sesión sin ended_at del dev.
type OpenSessionView struct {
	ID                string
	Project           string
	Goal              string
	StartedAt         time.Time
	LastActivityAt    time.Time // max(started_at, último update de obs)
	LastFileTouched   string
	Zombi             bool // >24h sin actividad
}

// StartedRelative formatea "hace X" para started_at.
func (s OpenSessionView) StartedRelative() string { return relativeTime(s.StartedAt) }

// LastRelative formatea "hace X" para last_activity_at.
func (s OpenSessionView) LastRelative() string { return relativeTime(s.LastActivityAt) }

// HeatmapDay — un punto del heatmap (1 día).
type HeatmapDay struct {
	Date     time.Time // date at 00:00 local
	Saves    int
	Sessions int
}

// Total returns the activity intensity for color bucketing.
func (d HeatmapDay) Total() int { return d.Saves + d.Sessions }

// Bucket returns 0..4 for color scale.
func (d HeatmapDay) Bucket() int {
	t := d.Total()
	switch {
	case t == 0:
		return 0
	case t <= 2:
		return 1
	case t <= 5:
		return 2
	case t <= 10:
		return 3
	default:
		return 4
	}
}

// Label returns "DD MMM YYYY · X saves · Y sesiones".
func (d HeatmapDay) Label() string {
	return fmt.Sprintf("%s · %d saves · %d sesiones",
		d.Date.Local().Format("02 Jan 2006"), d.Saves, d.Sessions)
}

// TimelineEvent — un evento atómico mostrado en la timeline.
type TimelineEvent struct {
	Kind       string    // "session" | "observation" | "skill" | "lead" | "quote"
	Action     string    // verbo en español: "Sesión iniciada", "Observación guardada", etc.
	Title      string    // texto principal
	Subtitle   string    // contexto secundario (project/scope/...)
	Icon       string    // emoji
	Variant    string    // success|warning|danger|muted (para badge)
	OccurredAt time.Time
	Href       string    // drill-down link (puede ser "")
}

// Relative para mostrar timestamps amigables.
func (e TimelineEvent) Relative() string { return relativeTime(e.OccurredAt) }

// PendingItems — agrupador de "lo que dejaste abierto".
type PendingItems struct {
	ZombieSessions   []OpenSessionView
	InactiveLeads    []PendingLead
	DraftQuotes      []PendingQuote
	DraftObservations []PendingObservation
}

type PendingLead struct {
	ID          string
	Name        string
	Company     string
	Status      string
	LastActivity time.Time
}

type PendingQuote struct {
	ID         string
	LeadName   string
	Currency   string
	Total      float64
	UpdatedAt  time.Time
}

type PendingObservation struct {
	ID        string
	Title     string
	Project   string
	Scope     string
	UpdatedAt time.Time
}

// ContributionsView — métricas de aportes del dev.
type ContributionsView struct {
	TopCanonObservations []TopObservation
	TopSkillsEdited      []TopSkill
	TotalSaves           int
	RankPercentile       int    // 0..100, "top X%"
	RankLabel            string // "top 25%" o ""
}

// TopObservation — entrada del top 3 de obs canon.
type TopObservation struct {
	ID             string
	Title          string
	Project        string
	RelevanceCount int
}

// ─── Handlers ───────────────────────────────────────────────────────────────

// handlePersonalCockpit renders /dashboard/me — el cockpit del dev.
func (h *handlers) handlePersonalCockpit(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if strings.TrimSpace(p.UID()) == "" {
		// Sin UID no podemos render. Redirige a login con next=/dashboard/me.
		http.Redirect(w, r, dashboardLoginPathWithNext("/dashboard/me"), http.StatusSeeOther)
		return
	}
	vm := h.buildPersonalCockpitVM(r.Context(), p)
	component := PersonalCockpitPage(vm)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Mi cockpit", p.DisplayName(), "me", p.Roles(), component))
}

// buildPersonalCockpitVM consulta el service y arma el ViewModel completo.
// Si el service es nil, retorna un VM vacío con flag HasService=false.
func (h *handlers) buildPersonalCockpitVM(ctx context.Context, p Principal) PersonalCockpitVM {
	now := time.Now()
	vm := PersonalCockpitVM{
		Username:   p.DisplayName(),
		Today:      now,
		HasService: h.cfg.PersonalCockpit != nil,
		Heatmap:    emptyHeatmap(now),
	}
	if h.cfg.PersonalCockpit == nil {
		vm.Notice = "Cockpit personal aún no configurado en el servidor."
		return vm
	}
	svc := h.cfg.PersonalCockpit
	uid := p.UID()

	if stats, err := svc.Stats(ctx, uid, now); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit stats error uid=%s: %v", uid, err))
	} else {
		vm.Stats = stats
	}
	if sessions, err := svc.OpenSessions(ctx, uid); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit open sessions error uid=%s: %v", uid, err))
	} else {
		vm.OpenSessions = sessions
	}
	if heatmap, err := svc.Heatmap(ctx, uid, now); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit heatmap error uid=%s: %v", uid, err))
	} else if len(heatmap) > 0 {
		vm.Heatmap = mergeHeatmap(emptyHeatmap(now), heatmap)
	}
	if timeline, err := svc.Timeline(ctx, uid, 50); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit timeline error uid=%s: %v", uid, err))
	} else {
		vm.Timeline = timeline
	}
	if pending, err := svc.Pending(ctx, uid, now); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit pending error uid=%s: %v", uid, err))
	} else {
		vm.Pending = pending
	}
	if contrib, err := svc.Contributions(ctx, uid); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit contributions error uid=%s: %v", uid, err))
	} else {
		vm.Contributions = contrib
	}
	return vm
}

// handleSessionResume — POST /dashboard/sessions/{id}/resume
// ACL: solo el dueño (developer_uid == authed UID) puede tocar.
// Comportamiento: refresca la actividad de la sesión y redirige al proyecto.
func (h *handlers) handleSessionResume(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	uid := strings.TrimSpace(p.UID())
	id := strings.TrimSpace(r.PathValue("id"))
	if uid == "" || id == "" {
		http.Error(w, "missing identity", http.StatusBadRequest)
		return
	}
	if h.cfg.PersonalCockpit == nil {
		http.Error(w, "personal cockpit not configured", http.StatusServiceUnavailable)
		return
	}
	owner, err := h.cfg.PersonalCockpit.IsSessionOwner(r.Context(), id, uid)
	if err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit resume IsSessionOwner err: %v", err))
		http.Error(w, "session lookup failed", http.StatusInternalServerError)
		return
	}
	if !owner {
		http.Error(w, "forbidden: only the session owner can resume", http.StatusForbidden)
		return
	}
	project, err := h.cfg.PersonalCockpit.ResumeSession(r.Context(), id, uid)
	if err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit resume err: %v", err))
		http.Error(w, "resume failed", http.StatusInternalServerError)
		return
	}
	target := "/dashboard/me"
	if strings.TrimSpace(project) != "" {
		target = "/dashboard/projects/" + project
	}
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleSessionClose — POST /dashboard/sessions/{id}/close
// ACL: solo el dueño puede cerrar.
// Form fields: summary (text).
func (h *handlers) handleSessionClose(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	uid := strings.TrimSpace(p.UID())
	id := strings.TrimSpace(r.PathValue("id"))
	if uid == "" || id == "" {
		http.Error(w, "missing identity", http.StatusBadRequest)
		return
	}
	if h.cfg.PersonalCockpit == nil {
		http.Error(w, "personal cockpit not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	summary := strings.TrimSpace(r.PostForm.Get("summary"))
	owner, err := h.cfg.PersonalCockpit.IsSessionOwner(r.Context(), id, uid)
	if err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit close IsSessionOwner err: %v", err))
		http.Error(w, "session lookup failed", http.StatusInternalServerError)
		return
	}
	if !owner {
		http.Error(w, "forbidden: only the session owner can close", http.StatusForbidden)
		return
	}
	if err := h.cfg.PersonalCockpit.CloseSession(r.Context(), id, uid, summary); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: cockpit close err: %v", err))
		if errors.Is(err, ErrSessionNotFound) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "close failed", http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/dashboard/me")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/dashboard/me", http.StatusSeeOther)
}

// ErrSessionNotFound señala que la sesión solicitada no existe o no es accesible.
var ErrSessionNotFound = errors.New("dashboard: session not found")

// ─── Helpers ────────────────────────────────────────────────────────────────

// emptyHeatmap returns 365 zero days ending on `until` (inclusive).
func emptyHeatmap(until time.Time) []HeatmapDay {
	out := make([]HeatmapDay, 365)
	end := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, until.Location())
	for i := 0; i < 365; i++ {
		out[i] = HeatmapDay{Date: end.AddDate(0, 0, -(364 - i))}
	}
	return out
}

// mergeHeatmap superpone los HeatmapDay con datos sobre el grid vacío.
func mergeHeatmap(base, with []HeatmapDay) []HeatmapDay {
	if len(base) == 0 {
		return with
	}
	idx := make(map[string]int, len(base))
	for i, d := range base {
		idx[d.Date.Format("2006-01-02")] = i
	}
	for _, d := range with {
		key := d.Date.Format("2006-01-02")
		if i, ok := idx[key]; ok {
			base[i].Saves += d.Saves
			base[i].Sessions += d.Sessions
		}
	}
	// Re-sort by date ASC just in case.
	sort.SliceStable(base, func(i, j int) bool { return base[i].Date.Before(base[j].Date) })
	return base
}

// relativeTime returns "hace X" para timestamps recientes, "DD MMM" para >7 días.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		return "ahora"
	}
	if d < time.Minute {
		return "hace segundos"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "hace 1 minuto"
		}
		return fmt.Sprintf("hace %d minutos", mins)
	}
	if d < 24*time.Hour {
		hrs := int(d.Hours())
		if hrs == 1 {
			return "hace 1 hora"
		}
		return fmt.Sprintf("hace %d horas", hrs)
	}
	if d < 7*24*time.Hour {
		days := int(d.Hours() / 24)
		if days == 1 {
			return "hace 1 día"
		}
		return fmt.Sprintf("hace %d días", days)
	}
	return t.Local().Format("02 Jan")
}

// formatDateES formatea una fecha al español ("sábado, 26 de abril de 2026").
func formatDateES(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	weekdays := map[time.Weekday]string{
		time.Sunday: "domingo", time.Monday: "lunes", time.Tuesday: "martes",
		time.Wednesday: "miércoles", time.Thursday: "jueves",
		time.Friday: "viernes", time.Saturday: "sábado",
	}
	months := map[time.Month]string{
		time.January: "enero", time.February: "febrero", time.March: "marzo",
		time.April: "abril", time.May: "mayo", time.June: "junio",
		time.July: "julio", time.August: "agosto", time.September: "septiembre",
		time.October: "octubre", time.November: "noviembre", time.December: "diciembre",
	}
	return fmt.Sprintf("%s, %d de %s de %d",
		weekdays[t.Weekday()], t.Day(), months[t.Month()], t.Year())
}

// signedDelta formatea +N o -N o "=" para deltas de stats.
func signedDelta(n int) string {
	switch {
	case n > 0:
		return fmt.Sprintf("+%d", n)
	case n < 0:
		return fmt.Sprintf("%d", n)
	default:
		return "="
	}
}

// deltaVariant returns CSS variant class por signo.
func deltaVariant(n int) string {
	switch {
	case n > 0:
		return "success"
	case n < 0:
		return "danger"
	default:
		return "muted"
	}
}

// timelineDayKey returns "YYYY-MM-DD" of an event for grouping.
func timelineDayKey(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

// timelineDayLabel formatea "DD MMM" para el header del grupo.
func timelineDayLabel(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("02 Jan 2006")
}

// observationTypeLabel devuelve display label para un type de observación.
func observationTypeLabel(t string) string {
	switch t {
	case "decision":
		return "Decisión"
	case "architecture":
		return "Arquitectura"
	case "bugfix":
		return "Bugfix"
	case "discovery":
		return "Descubrimiento"
	case "learning":
		return "Aprendizaje"
	case "feature":
		return "Feature"
	case "adr":
		return "ADR"
	case "tech_debt":
		return "Tech debt"
	case "risk":
		return "Riesgo"
	case "commitment":
		return "Compromiso"
	case "meeting":
		return "Reunión"
	case "i18n":
		return "i18n"
	case "config":
		return "Config"
	case "general":
		return "General"
	default:
		return t
	}
}

// groupTimelineByDay agrupa eventos por día (YYYY-MM-DD), preservando orden.
type timelineGroup struct {
	DayKey string
	Label  string
	Events []TimelineEvent
}

func groupTimelineByDay(events []TimelineEvent) []timelineGroup {
	if len(events) == 0 {
		return nil
	}
	ordered := make([]TimelineEvent, len(events))
	copy(ordered, events)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].OccurredAt.After(ordered[j].OccurredAt)
	})
	groups := make([]timelineGroup, 0, 8)
	idx := make(map[string]int, 8)
	for _, e := range ordered {
		key := timelineDayKey(e.OccurredAt)
		if i, ok := idx[key]; ok {
			groups[i].Events = append(groups[i].Events, e)
			continue
		}
		idx[key] = len(groups)
		groups = append(groups, timelineGroup{
			DayKey: key,
			Label:  timelineDayLabel(e.OccurredAt),
			Events: []TimelineEvent{e},
		})
	}
	return groups
}
