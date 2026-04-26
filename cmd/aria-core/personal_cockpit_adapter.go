package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// personalCockpitAdapter implementa dashboard.PersonalCockpitService contra Postgres.
type personalCockpitAdapter struct {
	db *sql.DB
}

func newPersonalCockpitAdapter(cs *cloudstore.CloudStore) *personalCockpitAdapter {
	return &personalCockpitAdapter{db: cs.DB()}
}

// Stats — count sesiones esta semana, obs, breakdown por tipo.
func (a *personalCockpitAdapter) Stats(ctx context.Context, devUID string, now time.Time) (dashboard.PersonalCockpitStats, error) {
	out := dashboard.PersonalCockpitStats{ObservationsByType: map[string]int{}}
	if a == nil || a.db == nil || devUID == "" {
		return out, nil
	}
	weekStart := now.AddDate(0, 0, -int(now.Weekday())+1) // Monday-based ish
	if now.Weekday() == time.Sunday {
		weekStart = weekStart.AddDate(0, 0, -7)
	}
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.UTC)
	prevWeekStart := weekStart.AddDate(0, 0, -7)

	// Sesiones esta semana / pasada
	_ = a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM aria_sessions
		WHERE developer_uid::text = $1 AND started_at >= $2
	`, devUID, weekStart).Scan(&out.SessionsThisWeek)
	_ = a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM aria_sessions
		WHERE developer_uid::text = $1 AND started_at >= $2 AND started_at < $3
	`, devUID, prevWeekStart, weekStart).Scan(&out.SessionsLastWeek)

	// Observaciones esta semana
	_ = a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM aria_observations
		WHERE developer_uid::text = $1 AND created_at >= $2
	`, devUID, weekStart).Scan(&out.ObservationsWeek)

	// Breakdown por tipo (esta semana)
	rows, err := a.db.QueryContext(ctx, `
		SELECT observation_type, COUNT(*) FROM aria_observations
		WHERE developer_uid::text = $1 AND created_at >= $2
		GROUP BY observation_type
	`, devUID, weekStart)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			var c int
			if err := rows.Scan(&t, &c); err == nil {
				out.ObservationsByType[t] = c
			}
		}
	}

	// Active minutes: estimación grosera = sum(last_summary or NOW() - started_at) últimos 30d
	since := now.AddDate(0, 0, -30)
	var minutes sql.NullInt64
	_ = a.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at, NOW()) - started_at))/60)::bigint, 0)
		FROM aria_sessions
		WHERE developer_uid::text = $1 AND started_at >= $2
	`, devUID, since).Scan(&minutes)
	if minutes.Valid {
		out.ActiveMinutes = minutes.Int64
	}

	return out, nil
}

func (a *personalCockpitAdapter) OpenSessions(ctx context.Context, devUID string) ([]dashboard.OpenSessionView, error) {
	out := []dashboard.OpenSessionView{}
	if a == nil || a.db == nil || devUID == "" {
		return out, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(project,''), COALESCE(goal,''), started_at, COALESCE(updated_at, started_at)
		FROM aria_sessions
		WHERE developer_uid::text = $1 AND ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 20
	`, devUID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var s dashboard.OpenSessionView
		if err := rows.Scan(&s.ID, &s.Project, &s.Goal, &s.StartedAt, &s.LastActivityAt); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (a *personalCockpitAdapter) Heatmap(ctx context.Context, devUID string, until time.Time) ([]dashboard.HeatmapDay, error) {
	out := make([]dashboard.HeatmapDay, 0, 365)
	if a == nil || a.db == nil || devUID == "" {
		return out, nil
	}
	since := until.AddDate(-1, 0, 0)
	rows, err := a.db.QueryContext(ctx, `
		SELECT day::date, COALESCE(saves,0), COALESCE(sessions,0)
		FROM (
			SELECT generate_series($1::date, $2::date, '1 day'::interval) AS day
		) d
		LEFT JOIN (
			SELECT created_at::date AS day, COUNT(*) AS saves
			FROM aria_observations
			WHERE developer_uid::text = $3 AND created_at >= $1
			GROUP BY 1
		) o USING (day)
		LEFT JOIN (
			SELECT started_at::date AS day, COUNT(*) AS sessions
			FROM aria_sessions
			WHERE developer_uid::text = $3 AND started_at >= $1
			GROUP BY 1
		) s USING (day)
		ORDER BY day
	`, since, until, devUID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d dashboard.HeatmapDay
		if err := rows.Scan(&d.Date, &d.Saves, &d.Sessions); err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (a *personalCockpitAdapter) Timeline(ctx context.Context, devUID string, limit int) ([]dashboard.TimelineEvent, error) {
	out := []dashboard.TimelineEvent{}
	if a == nil || a.db == nil || devUID == "" {
		return out, nil
	}
	if limit <= 0 {
		limit = 50
	}
	// Union de eventos: obs + sessions
	rows, err := a.db.QueryContext(ctx, `
		(SELECT 'observation' AS kind, id::text, title AS label, COALESCE(project,'') AS project, created_at
		 FROM aria_observations WHERE developer_uid::text = $1 ORDER BY created_at DESC LIMIT $2)
		UNION ALL
		(SELECT 'session' AS kind, id::text, COALESCE(goal,'(sesión sin goal)') AS label, COALESCE(project,'') AS project, started_at AS created_at
		 FROM aria_sessions WHERE developer_uid::text = $1 ORDER BY started_at DESC LIMIT $2)
		ORDER BY created_at DESC LIMIT $2
	`, devUID, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e dashboard.TimelineEvent
		var id, label, project string
		if err := rows.Scan(&e.Kind, &id, &label, &project, &e.OccurredAt); err != nil {
			continue
		}
		e.Title = label
		e.Subtitle = project
		switch e.Kind {
		case "observation":
			e.Action = "Observación guardada"
			e.Icon = "💡"
			e.Variant = "success"
			e.Href = "/dashboard/memorias/" + id
		case "session":
			e.Action = "Sesión iniciada"
			e.Icon = "▶"
			e.Variant = "muted"
		}
		out = append(out, e)
	}
	return out, nil
}

func (a *personalCockpitAdapter) Pending(ctx context.Context, devUID string, now time.Time) (dashboard.PendingItems, error) {
	out := dashboard.PendingItems{}
	if a == nil || a.db == nil || devUID == "" {
		return out, nil
	}
	cutoff := now.Add(-24 * time.Hour)
	rows, err := a.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(project,''), COALESCE(goal,''), started_at
		FROM aria_sessions
		WHERE developer_uid::text = $1 AND ended_at IS NULL AND started_at < $2
		ORDER BY started_at DESC LIMIT 10
	`, devUID, cutoff)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s dashboard.OpenSessionView
			if err := rows.Scan(&s.ID, &s.Project, &s.Goal, &s.StartedAt); err == nil {
				s.LastActivityAt = s.StartedAt
				out.ZombieSessions = append(out.ZombieSessions, s)
			}
		}
	}
	return out, nil
}

func (a *personalCockpitAdapter) Contributions(ctx context.Context, devUID string) (dashboard.ContributionsView, error) {
	out := dashboard.ContributionsView{}
	if a == nil || a.db == nil || devUID == "" {
		return out, nil
	}
	// Top 3 obs canon del dev
	rows, err := a.db.QueryContext(ctx, `
		SELECT id::text, title, COALESCE(project,''), relevance_count
		FROM aria_observations
		WHERE developer_uid::text = $1 AND canon = TRUE
		ORDER BY relevance_count DESC, created_at DESC
		LIMIT 3
	`, devUID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var o dashboard.TopObservation
			if err := rows.Scan(&o.ID, &o.Title, &o.Project, &o.RelevanceCount); err == nil {
				out.TopCanonObservations = append(out.TopCanonObservations, o)
			}
		}
	}
	// Total saves
	var total int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM aria_observations WHERE developer_uid::text = $1`, devUID).Scan(&total)
	out.TotalSaves = total
	return out, nil
}

func (a *personalCockpitAdapter) IsSessionOwner(ctx context.Context, sessionID, devUID string) (bool, error) {
	if a == nil || a.db == nil {
		return false, nil
	}
	var owner string
	err := a.db.QueryRowContext(ctx, `SELECT developer_uid::text FROM aria_sessions WHERE id::text = $1`, sessionID).Scan(&owner)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return owner == devUID, nil
}

func (a *personalCockpitAdapter) ResumeSession(ctx context.Context, sessionID, devUID string) (string, error) {
	if a == nil || a.db == nil {
		return "", errors.New("cockpit not configured")
	}
	var project sql.NullString
	err := a.db.QueryRowContext(ctx, `
		UPDATE aria_sessions SET updated_at = NOW()
		WHERE id::text = $1 AND developer_uid::text = $2
		RETURNING COALESCE(project,'')
	`, sessionID, devUID).Scan(&project)
	if err != nil {
		return "", err
	}
	return project.String, nil
}

func (a *personalCockpitAdapter) CloseSession(ctx context.Context, sessionID, devUID, summary string) error {
	if a == nil || a.db == nil {
		return errors.New("cockpit not configured")
	}
	_, err := a.db.ExecContext(ctx, `
		UPDATE aria_sessions SET ended_at = NOW(), updated_at = NOW()
		WHERE id::text = $1 AND developer_uid::text = $2
	`, sessionID, devUID)
	if err != nil {
		return err
	}
	if summary != "" {
		_, _ = a.db.ExecContext(ctx, `
			INSERT INTO aria_session_summaries (session_id, learned, completed)
			VALUES ($1, $2, '')
			ON CONFLICT (session_id) DO UPDATE SET learned = EXCLUDED.learned
		`, sessionID, summary)
	}
	return nil
}
