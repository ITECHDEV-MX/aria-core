package contextbudget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lib/pq"
)

// FeedbackSignal enumera las señales válidas para aria_skill_usage.
type FeedbackSignal string

const (
	FeedbackCommitReferenced FeedbackSignal = "commit_referenced"
	FeedbackManualThumbsUp   FeedbackSignal = "manual_thumbs_up"
	FeedbackManualThumbsDown FeedbackSignal = "manual_thumbs_down"
	FeedbackUsedInRecipe     FeedbackSignal = "used_in_recipe"
	FeedbackUnused           FeedbackSignal = "unused"
)

// ErrInvalidSignal indica que el caller pasó un signal fuera del enum.
var ErrInvalidSignal = errors.New("contextbudget: invalid feedback signal")

// ValidFeedbackSignal reporta si el string corresponde a una señal conocida.
func ValidFeedbackSignal(s string) bool {
	switch FeedbackSignal(strings.TrimSpace(s)) {
	case FeedbackCommitReferenced, FeedbackManualThumbsUp,
		FeedbackManualThumbsDown, FeedbackUsedInRecipe, FeedbackUnused:
		return true
	}
	return false
}

// HelpedFromSignal mapea la señal al did_help: nil para "unused" (sin info),
// true para señales positivas, false para thumbs-down.
func HelpedFromSignal(s FeedbackSignal) (helped sql.NullBool) {
	switch s {
	case FeedbackCommitReferenced, FeedbackManualThumbsUp, FeedbackUsedInRecipe:
		return sql.NullBool{Bool: true, Valid: true}
	case FeedbackManualThumbsDown:
		return sql.NullBool{Bool: false, Valid: true}
	default:
		return sql.NullBool{}
	}
}

// SkillScored agrega effectiveness con counts y score Wilson lower bound.
type SkillScored struct {
	SkillID      string
	HelpedCount  int
	UsedCount    int
	WilsonLB     float64 // 0..1; usar como Effectiveness en Skill
	UnusedCount  int     // signal=unused, no penaliza
}

// RecordRetrievalParams captura la metadata cuando un skill se retorna en
// aria_get_skills.
type RecordRetrievalParams struct {
	SkillID         string
	SessionID       string
	DeveloperUID    string
	Project         string
	TaskDescription string
	PositionInResults int // 1-based
}

// Telemetry maneja el ciclo de retrieval+feedback de skills. Implementación
// SQL-backed sobre Postgres (tabla aria_skill_usage); la interface permite
// mockear en tests del injector y MCP.
type Telemetry interface {
	RecordRetrieval(ctx context.Context, p RecordRetrievalParams) error
	RecordFeedback(ctx context.Context, skillID, devUID string, signal FeedbackSignal, helped sql.NullBool, notes string) error
	GetEffectiveness(ctx context.Context, skillID string) (SkillScored, error)
	BulkEffectiveness(ctx context.Context, skillIDs []string) (map[string]SkillScored, error)
	TopSkillsForTask(ctx context.Context, query string, limit int) ([]SkillScored, error)
}

// SQLTelemetry implementa Telemetry sobre *sql.DB (Postgres).
type SQLTelemetry struct {
	db *sql.DB
}

// NewSQLTelemetry construye la implementación SQL.
func NewSQLTelemetry(db *sql.DB) *SQLTelemetry {
	return &SQLTelemetry{db: db}
}

// RecordRetrieval inserta una fila en aria_skill_usage con did_help=null
// pendiente de signal.
func (t *SQLTelemetry) RecordRetrieval(ctx context.Context, p RecordRetrievalParams) error {
	if t == nil || t.db == nil {
		return errors.New("contextbudget: telemetry not initialized")
	}
	skill := strings.TrimSpace(p.SkillID)
	if skill == "" {
		return errors.New("contextbudget: skill_id required")
	}
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO aria_skill_usage (
			skill_id, session_id, developer_uid, project, task_description, position_in_results
		) VALUES (
			$1, NULLIF($2,''), NULLIF($3,'')::uuid, NULLIF($4,''), NULLIF($5,''), $6
		)`,
		skill, p.SessionID, p.DeveloperUID, p.Project, p.TaskDescription, p.PositionInResults,
	)
	if err != nil {
		return fmt.Errorf("contextbudget: record retrieval: %w", err)
	}
	return nil
}

// RecordFeedback escribe el signal sobre la última fila pendiente para ese
// developer+skill, o crea una nueva si no existe (caso: thumbs up sin haber
// pasado por aria_get_skills primero).
func (t *SQLTelemetry) RecordFeedback(ctx context.Context, skillID, devUID string, signal FeedbackSignal, helped sql.NullBool, notes string) error {
	if t == nil || t.db == nil {
		return errors.New("contextbudget: telemetry not initialized")
	}
	if strings.TrimSpace(skillID) == "" {
		return errors.New("contextbudget: skill_id required")
	}
	if !ValidFeedbackSignal(string(signal)) {
		return fmt.Errorf("contextbudget: invalid feedback signal %q", signal)
	}

	// Intentar update sobre la fila más reciente sin signal (last_retrieved).
	var rowID string
	err := t.db.QueryRowContext(ctx, `
		SELECT id::text FROM aria_skill_usage
		WHERE skill_id = $1 AND developer_uid = NULLIF($2,'')::uuid
		AND feedback_signal IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, skillID, devUID).Scan(&rowID)
	if err == nil {
		_, uerr := t.db.ExecContext(ctx, `
			UPDATE aria_skill_usage SET
				feedback_signal = $1,
				did_help = $2,
				feedback_at = NOW(),
				task_description = COALESCE(NULLIF(task_description,''), $3)
			WHERE id = $4::uuid
		`, string(signal), helped, notes, rowID)
		return uerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("contextbudget: lookup pending usage: %w", err)
	}

	// No había retrieval previo — insert standalone (manual feedback inline).
	_, ierr := t.db.ExecContext(ctx, `
		INSERT INTO aria_skill_usage (
			skill_id, developer_uid, task_description, position_in_results,
			feedback_signal, did_help, feedback_at
		) VALUES ($1, NULLIF($2,'')::uuid, $3, 0, $4, $5, NOW())
	`, skillID, devUID, notes, string(signal), helped)
	if ierr != nil {
		return fmt.Errorf("contextbudget: insert feedback: %w", ierr)
	}
	return nil
}

// GetEffectiveness retorna el score Wilson lower bound + counts.
func (t *SQLTelemetry) GetEffectiveness(ctx context.Context, skillID string) (SkillScored, error) {
	if t == nil || t.db == nil {
		return SkillScored{}, errors.New("contextbudget: telemetry not initialized")
	}
	const q = `
		SELECT
			COALESCE(SUM(CASE WHEN did_help = TRUE THEN 1 ELSE 0 END), 0) AS helped,
			COALESCE(SUM(CASE WHEN did_help IS NOT NULL THEN 1 ELSE 0 END), 0) AS used,
			COALESCE(SUM(CASE WHEN feedback_signal = 'unused' THEN 1 ELSE 0 END), 0) AS unused
		FROM aria_skill_usage WHERE skill_id = $1
	`
	var s SkillScored
	s.SkillID = skillID
	if err := t.db.QueryRowContext(ctx, q, skillID).Scan(&s.HelpedCount, &s.UsedCount, &s.UnusedCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, nil
		}
		return s, fmt.Errorf("contextbudget: get effectiveness: %w", err)
	}
	s.WilsonLB = WilsonLowerBound(s.HelpedCount, s.UsedCount)
	return s, nil
}

// BulkEffectiveness devuelve scores para muchos skills en una sola query.
func (t *SQLTelemetry) BulkEffectiveness(ctx context.Context, skillIDs []string) (map[string]SkillScored, error) {
	out := make(map[string]SkillScored, len(skillIDs))
	if t == nil || t.db == nil || len(skillIDs) == 0 {
		return out, nil
	}
	rows, err := t.db.QueryContext(ctx, `
		SELECT skill_id::text,
			COALESCE(SUM(CASE WHEN did_help = TRUE THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN did_help IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN feedback_signal = 'unused' THEN 1 ELSE 0 END), 0)
		FROM aria_skill_usage
		WHERE skill_id::text = ANY($1)
		GROUP BY skill_id
	`, pq.Array(skillIDs))
	if err != nil {
		return out, fmt.Errorf("contextbudget: bulk effectiveness: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s SkillScored
		if err := rows.Scan(&s.SkillID, &s.HelpedCount, &s.UsedCount, &s.UnusedCount); err != nil {
			return out, fmt.Errorf("contextbudget: scan effectiveness: %w", err)
		}
		s.WilsonLB = WilsonLowerBound(s.HelpedCount, s.UsedCount)
		out[s.SkillID] = s
	}
	return out, rows.Err()
}

// TopSkillsForTask retorna los skills más efectivos cuya task_description
// matchee la query. Usa ts_query español + accent-insensitive si query !="".
func (t *SQLTelemetry) TopSkillsForTask(ctx context.Context, query string, limit int) ([]SkillScored, error) {
	if t == nil || t.db == nil {
		return nil, errors.New("contextbudget: telemetry not initialized")
	}
	if limit <= 0 {
		limit = 10
	}
	q := strings.TrimSpace(query)
	var rows *sql.Rows
	var err error
	if q == "" {
		rows, err = t.db.QueryContext(ctx, `
			SELECT skill_id::text,
				COALESCE(SUM(CASE WHEN did_help = TRUE THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN did_help IS NOT NULL THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN feedback_signal = 'unused' THEN 1 ELSE 0 END), 0)
			FROM aria_skill_usage
			GROUP BY skill_id
			ORDER BY 2 DESC
			LIMIT $1
		`, limit)
	} else {
		rows, err = t.db.QueryContext(ctx, `
			SELECT skill_id::text,
				COALESCE(SUM(CASE WHEN did_help = TRUE THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN did_help IS NOT NULL THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN feedback_signal = 'unused' THEN 1 ELSE 0 END), 0)
			FROM aria_skill_usage
			WHERE to_tsvector('spanish', unaccent(coalesce(task_description,''))) @@ plainto_tsquery('spanish', unaccent($1))
			GROUP BY skill_id
			ORDER BY 2 DESC
			LIMIT $2
		`, q, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("contextbudget: top skills: %w", err)
	}
	defer rows.Close()
	out := make([]SkillScored, 0, limit)
	for rows.Next() {
		var s SkillScored
		if err := rows.Scan(&s.SkillID, &s.HelpedCount, &s.UsedCount, &s.UnusedCount); err != nil {
			return out, err
		}
		s.WilsonLB = WilsonLowerBound(s.HelpedCount, s.UsedCount)
		out = append(out, s)
	}
	return out, rows.Err()
}

// MarkUnusedAfter recorre filas viejas sin feedback y las marca como unused.
// Pensado para un cron diario (no incluye scheduling, solo la query).
func (t *SQLTelemetry) MarkUnusedAfter(ctx context.Context, age time.Duration) (int64, error) {
	if t == nil || t.db == nil {
		return 0, errors.New("contextbudget: telemetry not initialized")
	}
	if age <= 0 {
		age = 7 * 24 * time.Hour
	}
	res, err := t.db.ExecContext(ctx, `
		UPDATE aria_skill_usage SET feedback_signal = 'unused', feedback_at = NOW()
		WHERE feedback_signal IS NULL
		AND created_at < NOW() - $1::interval
	`, formatPGInterval(age))
	if err != nil {
		return 0, fmt.Errorf("contextbudget: mark unused: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func formatPGInterval(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d.Seconds()))
}

// === Wilson lower bound ===

// WilsonLowerBound devuelve el extremo inferior del intervalo de confianza
// 95% para una proporción binomial helped/used. Útil cuando se tienen pocos
// datos: penaliza skills con poco volumen para no sobre-promoverlos.
//
// Si used == 0 → 0.
// Referencia: https://en.wikipedia.org/wiki/Binomial_proportion_confidence_interval#Wilson_score_interval
func WilsonLowerBound(helped, used int) float64 {
	if used <= 0 || helped < 0 {
		return 0
	}
	if helped > used {
		helped = used
	}
	n := float64(used)
	phat := float64(helped) / n
	const z = 1.96 // 95%
	z2 := z * z
	denom := 1 + z2/n
	center := phat + z2/(2*n)
	margin := z * math.Sqrt(phat*(1-phat)/n+z2/(4*n*n))
	lb := (center - margin) / denom
	if lb < 0 {
		return 0
	}
	if lb > 1 {
		return 1
	}
	return lb
}
