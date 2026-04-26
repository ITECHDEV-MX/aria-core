package roi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MetricsStore expone las queries SQL crudas que sostienen las métricas ROI.
// Se separa del Service para poder testear con mock o sqlite in-memory.
type MetricsStore struct {
	db *sql.DB
}

// NewMetricsStore arma un MetricsStore sobre un *sql.DB ya pingueado.
func NewMetricsStore(db *sql.DB) *MetricsStore {
	return &MetricsStore{db: db}
}

// DB expone el *sql.DB (usado por el adapter para hacer LogSearch directamente).
func (m *MetricsStore) DB() *sql.DB {
	if m == nil {
		return nil
	}
	return m.db
}

// optionalDevFilter agrega `AND <colName> = $N::uuid` si devUID no es vacío.
// Devuelve el chunk SQL y el slice de args extendido. idxStart es el índice
// inicial para placeholders ($1, $2, ...).
func optionalDevFilter(devUID, colName string, args []any, idxStart int) (string, []any, int) {
	devUID = strings.TrimSpace(devUID)
	if devUID == "" {
		return "", args, idxStart
	}
	args = append(args, devUID)
	return fmt.Sprintf(" AND %s = $%d::uuid", colName, idxStart), args, idxStart + 1
}

// CalcTTC retorna el promedio de minutos entre aria_session_start y la primera
// observación con observation_type IN ('decision','architecture','discovery')
// guardada en esa sesión.
//
// Definición:
//
//	TTC = MIN(observations.created_at) - sessions.started_at
//	      filtrado por observation_type IN ('decision','architecture','discovery')
//
// Si una sesión no tiene observation de tipo decision/etc., no cuenta.
func (m *MetricsStore) CalcTTC(ctx context.Context, devUID string, since, until time.Time) (avgMinutes float64, samples int, err error) {
	if m == nil || m.db == nil {
		return 0, 0, nil
	}
	args := []any{since.UTC(), until.UTC()}
	idx := 3
	devClause, args, _ := optionalDevFilter(devUID, "s.developer_uid", args, idx)
	q := fmt.Sprintf(`
		SELECT
			COALESCE(AVG(EXTRACT(EPOCH FROM (first_decision_at - s.started_at))/60.0), 0) AS avg_minutes,
			COUNT(*) AS samples
		FROM aria_sessions s
		JOIN LATERAL (
			SELECT MIN(o.created_at) AS first_decision_at
			FROM aria_observations o
			WHERE o.session_id = s.id
			  AND o.observation_type IN ('decision','architecture','discovery')
		) fd ON fd.first_decision_at IS NOT NULL
		WHERE s.started_at >= $1 AND s.started_at < $2
		%s
	`, devClause)
	row := m.db.QueryRowContext(ctx, q, args...)
	if err := row.Scan(&avgMinutes, &samples); err != nil {
		return 0, 0, fmt.Errorf("roi: calc ttc: %w", err)
	}
	return avgMinutes, samples, nil
}

// CalcRDR retorna el % de búsquedas (aria_search_log) que devolvieron al menos
// un resultado canon. Si no hay queries en la ventana, retorna (0, 0, nil).
//
// Definición:
//
//	RDR = COUNT(canon_hit_count > 0) / COUNT(*)
func (m *MetricsStore) CalcRDR(ctx context.Context, devUID string, since, until time.Time) (pct float64, total int, err error) {
	if m == nil || m.db == nil {
		return 0, 0, nil
	}
	args := []any{since.UTC(), until.UTC()}
	idx := 3
	devClause, args, _ := optionalDevFilter(devUID, "developer_uid", args, idx)
	q := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE canon_hit_count > 0)::float / NULLIF(COUNT(*),0)::float AS pct,
			COUNT(*) AS total
		FROM aria_search_log
		WHERE created_at >= $1 AND created_at < $2
		%s
	`, devClause)
	var pctNull sql.NullFloat64
	row := m.db.QueryRowContext(ctx, q, args...)
	if err := row.Scan(&pctNull, &total); err != nil {
		return 0, 0, fmt.Errorf("roi: calc rdr: %w", err)
	}
	if pctNull.Valid {
		pct = pctNull.Float64
	}
	return pct, total, nil
}

// CalcCWR retorna el % de accesos a vault que fueron `use_in_cmd` vs el total
// (use_in_cmd + read). Retorna (0, nil) si no hay accesos.
//
// Definición:
//
//	CWR = COUNT(action='use_in_cmd') / COUNT(action IN ('use_in_cmd','read'))
func (m *MetricsStore) CalcCWR(ctx context.Context, devUID string, since, until time.Time) (pct float64, err error) {
	if m == nil || m.db == nil {
		return 0, nil
	}
	args := []any{since.UTC(), until.UTC()}
	idx := 3
	devClause, args, _ := optionalDevFilter(devUID, "accessed_by_uid", args, idx)
	q := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE action = 'use_in_cmd')::float
				/ NULLIF(COUNT(*) FILTER (WHERE action IN ('use_in_cmd','read')),0)::float
		FROM aria_secret_access_log
		WHERE accessed_at >= $1 AND accessed_at < $2
		  AND action IN ('use_in_cmd','read')
		%s
	`, devClause)
	var pctNull sql.NullFloat64
	if err := m.db.QueryRowContext(ctx, q, args...).Scan(&pctNull); err != nil {
		return 0, fmt.Errorf("roi: calc cwr: %w", err)
	}
	if pctNull.Valid {
		pct = pctNull.Float64
	}
	return pct, nil
}

// CalcSVR retorna el % de skill_usage con did_help=true sobre los rows con
// did_help IS NOT NULL (sea true o false). Skills sin feedback no cuentan.
//
// Definición:
//
//	SVR = COUNT(did_help=true) / COUNT(did_help IS NOT NULL)
func (m *MetricsStore) CalcSVR(ctx context.Context, devUID string, since, until time.Time) (pct float64, err error) {
	if m == nil || m.db == nil {
		return 0, nil
	}
	args := []any{since.UTC(), until.UTC()}
	idx := 3
	devClause, args, _ := optionalDevFilter(devUID, "developer_uid", args, idx)
	q := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE did_help = TRUE)::float
				/ NULLIF(COUNT(*) FILTER (WHERE did_help IS NOT NULL),0)::float
		FROM aria_skill_usage
		WHERE created_at >= $1 AND created_at < $2
		  AND did_help IS NOT NULL
		%s
	`, devClause)
	var pctNull sql.NullFloat64
	if err := m.db.QueryRowContext(ctx, q, args...).Scan(&pctNull); err != nil {
		return 0, fmt.Errorf("roi: calc svr: %w", err)
	}
	if pctNull.Valid {
		pct = pctNull.Float64
	}
	return pct, nil
}

// CalcDTT retorna el promedio de minutos por sesión cuyo goal contiene 'deploy'
// (case-insensitive). Placeholder hasta que recipe runner exista.
//
// Definición:
//
//	DTT = AVG(ended_at - started_at) WHERE goal ILIKE '%deploy%'
//	      sólo sesiones con ended_at NOT NULL.
func (m *MetricsStore) CalcDTT(ctx context.Context, devUID string, since, until time.Time) (avgMinutes float64, err error) {
	if m == nil || m.db == nil {
		return 0, nil
	}
	args := []any{since.UTC(), until.UTC()}
	idx := 3
	devClause, args, _ := optionalDevFilter(devUID, "developer_uid", args, idx)
	q := fmt.Sprintf(`
		SELECT
			COALESCE(AVG(EXTRACT(EPOCH FROM (ended_at - started_at))/60.0), 0)
		FROM aria_sessions
		WHERE started_at >= $1 AND started_at < $2
		  AND ended_at IS NOT NULL
		  AND goal ILIKE '%%deploy%%'
		%s
	`, devClause)
	if err := m.db.QueryRowContext(ctx, q, args...).Scan(&avgMinutes); err != nil {
		return 0, fmt.Errorf("roi: calc dtt: %w", err)
	}
	return avgMinutes, nil
}

// LogSearchParams es el payload recibido cuando un caller registra una búsqueda.
type LogSearchParams struct {
	Query          string
	ResultCount    int
	CanonHitCount  int
	TotalTokens    int
	TruncatedCount int
	DeveloperUID   string
	Project        string
	Scope          string
	ClientID       string
	DurationMs     int
}

// LogSearch persiste una fila en aria_search_log. Tolerante a UID vacío.
func (m *MetricsStore) LogSearch(ctx context.Context, p LogSearchParams) error {
	if m == nil || m.db == nil {
		return nil
	}
	var devUID, clientID *string
	if v := strings.TrimSpace(p.DeveloperUID); v != "" {
		devUID = &v
	}
	if v := strings.TrimSpace(p.ClientID); v != "" {
		clientID = &v
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO aria_search_log
			(query, result_count, canon_hit_count, total_tokens, truncated_count,
			 developer_uid, project, scope, client_id, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, p.Query, p.ResultCount, p.CanonHitCount, p.TotalTokens, p.TruncatedCount,
		devUID, strings.TrimSpace(p.Project), strings.TrimSpace(p.Scope), clientID, p.DurationMs)
	if err != nil {
		return fmt.Errorf("roi: log search: %w", err)
	}
	return nil
}

// ContributorScore mide aporte de un dev: cuántas observaciones canon tiene
// en el período y cuántas veces fueron referenciadas/relevantes.
type ContributorScore struct {
	DeveloperUID    string
	DeveloperEmail  string
	CanonCount      int
	RelevanceTotal  int
}

// TopContributors retorna los devs ordenados por aporte canon en `since`..now.
// Limit por defecto 5 si limit <= 0.
func (m *MetricsStore) TopContributors(ctx context.Context, since time.Time, limit int) ([]ContributorScore, error) {
	if m == nil || m.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT
			COALESCE(o.developer_uid::text, ''),
			COALESCE(u.email, ''),
			COUNT(*) AS canon_count,
			COALESCE(SUM(o.relevance_count), 0) AS relevance_total
		FROM aria_observations o
		LEFT JOIN cloud_users u ON u.uid = o.developer_uid
		WHERE o.canon = TRUE
		  AND o.created_at >= $1
		  AND o.developer_uid IS NOT NULL
		GROUP BY o.developer_uid, u.email
		ORDER BY canon_count DESC, relevance_total DESC
		LIMIT $2
	`, since.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("roi: top contributors: %w", err)
	}
	defer rows.Close()
	out := make([]ContributorScore, 0, limit)
	for rows.Next() {
		var c ContributorScore
		if err := rows.Scan(&c.DeveloperUID, &c.DeveloperEmail, &c.CanonCount, &c.RelevanceTotal); err != nil {
			return nil, fmt.Errorf("roi: scan contributor: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClientROI agrega métricas por client_id: cantidad de obs en scope
// client_knowledge, cuántos accesos a vault gateados, etc. — usado para reporte
// mensual.
type ClientROI struct {
	ClientID         string
	ObservationCount int
	CanonCount       int
	VaultAccess      int
	VaultUseInCmd    int
}

// PerClientBreakdown agrupa obs+accesos vault por client_id desde `since`.
// Sólo incluye client_ids con al menos una observación scope=client_knowledge.
func (m *MetricsStore) PerClientBreakdown(ctx context.Context, since time.Time) ([]ClientROI, error) {
	if m == nil || m.db == nil {
		return nil, nil
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT
			COALESCE(o.client_id::text, ''),
			COUNT(*) AS observation_count,
			COUNT(*) FILTER (WHERE o.canon = TRUE) AS canon_count,
			COALESCE((
				SELECT COUNT(*) FROM aria_secret_access_log a
				WHERE a.accessed_at >= $1 AND a.action IN ('use_in_cmd','read')
			), 0) AS vault_access_global,
			COALESCE((
				SELECT COUNT(*) FROM aria_secret_access_log a
				WHERE a.accessed_at >= $1 AND a.action = 'use_in_cmd'
			), 0) AS vault_use_in_cmd_global
		FROM aria_observations o
		WHERE o.scope = 'client_knowledge'
		  AND o.created_at >= $1
		  AND o.client_id IS NOT NULL
		GROUP BY o.client_id
		ORDER BY observation_count DESC
		LIMIT 20
	`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("roi: per-client breakdown: %w", err)
	}
	defer rows.Close()
	out := make([]ClientROI, 0, 8)
	for rows.Next() {
		var c ClientROI
		if err := rows.Scan(&c.ClientID, &c.ObservationCount, &c.CanonCount, &c.VaultAccess, &c.VaultUseInCmd); err != nil {
			return nil, fmt.Errorf("roi: scan client roi: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WeeklyPoint representa un punto en la serie temporal saved_min_per_week.
type WeeklyPoint struct {
	WeekStart       time.Time
	SavedMinutes    float64
	SavedMXN        float64
}

// WeeklyTimeline retorna los últimos N=12 puntos semanales con savings totales
// del dev (o global si devUID="").
func (m *MetricsStore) WeeklyTimeline(ctx context.Context, devUID string, weeks int, costPerMin float64) ([]WeeklyPoint, error) {
	if weeks <= 0 {
		weeks = 12
	}
	now := time.Now().UTC()
	out := make([]WeeklyPoint, 0, weeks)
	for i := weeks - 1; i >= 0; i-- {
		end := now.AddDate(0, 0, -7*i)
		start := end.AddDate(0, 0, -7)
		// Usamos los pillars básicos: RDR + CWR + SVR ponderados.
		rdr, _, err := m.CalcRDR(ctx, devUID, start, end)
		if err != nil {
			rdr = 0
		}
		cwr, err := m.CalcCWR(ctx, devUID, start, end)
		if err != nil {
			cwr = 0
		}
		svr, err := m.CalcSVR(ctx, devUID, start, end)
		if err != nil {
			svr = 0
		}
		saved := SavedMinutesFromPcts(rdr, cwr, svr, 0)
		out = append(out, WeeklyPoint{
			WeekStart:    start,
			SavedMinutes: saved,
			SavedMXN:     saved * costPerMin,
		})
	}
	return out, nil
}
