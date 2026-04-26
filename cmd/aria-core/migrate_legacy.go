package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func jsonUnmarshalLocal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

func pqStringArrayValueAdmin(v []string) any {
	return pq.Array(v)
}

// adminMigrateFromLegacy lee el SQLite legacy de aria-global (~/.aria/aria.db por
// default) y migra observations + sessions + session_summaries + skills al
// Postgres aria_core_cloud (aria_observations, aria_sessions, etc).
//
// Idempotente: usa ON CONFLICT (id) DO NOTHING para no duplicar.
//
// Ejecutar:
//   ARIA_CORE_DATABASE_URL=postgres://... aria-core admin migrate-from-legacy --sqlite ~/.aria/aria.db
func adminMigrateFromLegacy(args []string) {
	fs := flag.NewFlagSet("migrate-from-legacy", flag.ContinueOnError)
	sqlitePath := fs.String("sqlite", os.Getenv("HOME")+"/.aria/aria.db", "path al SQLite legacy aria.db")
	dryRun := fs.Bool("dry-run", false, "solo cuenta qué migraría sin escribir")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		exitFunc(1)
		return
	}
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "ARIA_CORE_DATABASE_URL is required (postgres://...)")
		exitFunc(1)
		return
	}

	src, err := sql.Open("sqlite", *sqlitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open sqlite: %v\n", err)
		exitFunc(1)
		return
	}
	defer src.Close()

	dst, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open postgres: %v\n", err)
		exitFunc(1)
		return
	}
	defer dst.Close()
	if err := dst.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "ping postgres: %v\n", err)
		exitFunc(1)
		return
	}

	ctx := context.Background()
	if *dryRun {
		fmt.Println("== DRY-RUN — counting only ==")
	}

	// Sessions primero (FK target)
	if n, err := migrateSessions(ctx, src, dst, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", err)
		exitFunc(1)
		return
	} else {
		fmt.Printf("✓ sessions migradas: %d\n", n)
	}

	if n, err := migrateSessionSummaries(ctx, src, dst, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "session_summaries: %v\n", err)
		exitFunc(1)
		return
	} else {
		fmt.Printf("✓ session_summaries migradas: %d\n", n)
	}

	if n, err := migrateObservations(ctx, src, dst, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "observations: %v\n", err)
		exitFunc(1)
		return
	} else {
		fmt.Printf("✓ observations migradas: %d\n", n)
	}

	if n, err := migrateSkills(ctx, src, dst, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		exitFunc(1)
		return
	} else {
		fmt.Printf("✓ skills migrados: %d\n", n)
	}

	fmt.Println("\nMigración completa. Verificá en /dashboard o vía /v1/memory/search?q=")
}

func migrateSessions(ctx context.Context, src, dst *sql.DB, dryRun bool) (int, error) {
	rows, err := src.QueryContext(ctx, `
		SELECT id, developer_id, developer_email, developer_role, client_id, machine_id,
		       project, directory, goal, status, started_at, ended_at, created_at
		FROM sessions
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, devID, machineID, status string
		var devEmail, devRole, clientID, project, directory, goal sql.NullString
		var startedAt, endedAt, createdAt sql.NullString
		if err := rows.Scan(&id, &devID, &devEmail, &devRole, &clientID, &machineID,
			&project, &directory, &goal, &status, &startedAt, &endedAt, &createdAt); err != nil {
			return n, err
		}
		if dryRun {
			n++
			continue
		}
		// developer_id legacy es texto (uid o email). Ignoramos UID linkage por ahora.
		_, err := dst.ExecContext(ctx, `
			INSERT INTO aria_sessions (id, developer_email, developer_role, machine_id,
				project, directory, goal, status, started_at, ended_at, created_at)
			VALUES ($1, NULLIF($2,''), $3, $4, NULLIF($5,''), NULLIF($6,''), NULLIF($7,''),
			        $8, COALESCE($9::timestamptz, NOW()), $10::timestamptz, COALESCE($11::timestamptz, NOW()))
			ON CONFLICT (id) DO NOTHING
		`, id, nullToString(devEmail), nullToStringDefault(devRole, "dev"), machineID,
			nullToString(project), nullToString(directory), nullToString(goal),
			status, nullToInterface(startedAt), nullToInterface(endedAt), nullToInterface(createdAt))
		if err != nil {
			return n, fmt.Errorf("insert session %s: %w", id, err)
		}
		n++
	}
	return n, rows.Err()
}

func migrateSessionSummaries(ctx context.Context, src, dst *sql.DB, dryRun bool) (int, error) {
	rows, err := src.QueryContext(ctx, `
		SELECT id, session_id, request, investigated, learned, completed, next_steps,
		       files_read, files_edited, notes, drift_score, quality_grade, tool_calls_count, created_at
		FROM session_summaries
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, sessionID string
		var request, investigated, learned, completed, nextSteps, filesRead, filesEdited, notes, qualityGrade sql.NullString
		var driftScore sql.NullFloat64
		var toolCallsCount int
		var createdAt sql.NullString
		if err := rows.Scan(&id, &sessionID, &request, &investigated, &learned, &completed,
			&nextSteps, &filesRead, &filesEdited, &notes, &driftScore, &qualityGrade,
			&toolCallsCount, &createdAt); err != nil {
			return n, err
		}
		if dryRun {
			n++
			continue
		}
		// Validar que la sesión exista en destino (FK)
		var exists int
		_ = dst.QueryRowContext(ctx, `SELECT 1 FROM aria_sessions WHERE id = $1`, sessionID).Scan(&exists)
		if exists == 0 {
			continue
		}
		_, err := dst.ExecContext(ctx, `
			INSERT INTO aria_session_summaries (id, session_id, request, investigated, learned,
				completed, next_steps, files_read, files_edited, notes, drift_score, quality_grade,
				tool_calls_count, created_at)
			VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,''),
			        NULLIF($7,''), NULLIF($8,''), NULLIF($9,''), NULLIF($10,''),
			        $11, NULLIF($12,''), $13, COALESCE($14::timestamptz, NOW()))
			ON CONFLICT (id) DO NOTHING
		`, id, sessionID, nullToString(request), nullToString(investigated), nullToString(learned),
			nullToString(completed), nullToString(nextSteps), nullToString(filesRead),
			nullToString(filesEdited), nullToString(notes), nullableFloat(driftScore),
			nullToString(qualityGrade), toolCallsCount, nullToInterface(createdAt))
		if err != nil {
			return n, fmt.Errorf("insert summary %s: %w", id, err)
		}
		n++
	}
	return n, rows.Err()
}

func migrateObservations(ctx context.Context, src, dst *sql.DB, dryRun bool) (int, error) {
	rows, err := src.QueryContext(ctx, `
		SELECT id, session_id, developer_id, developer_role, client_id, project, scope,
		       observation_type, title, subtitle, narrative, facts, concepts, files_touched,
		       reasoning_trace, generated_by_model, relevance_count, discovery_tokens,
		       quality_score, drift_detected, valid_from, valid_until, superseded_by,
		       topic_key, source, created_at, updated_at
		FROM observations
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, devID, scope, obsType, title, source string
		var sessionID, devRole, clientID, project, subtitle, narrative, facts, concepts sql.NullString
		var filesTouched, reasoningTrace, generatedByModel, supersededBy, topicKey sql.NullString
		var validFrom, validUntil, createdAt, updatedAt sql.NullString
		var relevanceCount, discoveryTokens, driftDetected int
		var qualityScore sql.NullFloat64
		if err := rows.Scan(&id, &sessionID, &devID, &devRole, &clientID, &project, &scope,
			&obsType, &title, &subtitle, &narrative, &facts, &concepts, &filesTouched,
			&reasoningTrace, &generatedByModel, &relevanceCount, &discoveryTokens,
			&qualityScore, &driftDetected, &validFrom, &validUntil, &supersededBy,
			&topicKey, &source, &createdAt, &updatedAt); err != nil {
			return n, err
		}
		if dryRun {
			n++
			continue
		}
		// Si session_id existe en sqlite pero no en destino, lo dejamos null (FK opcional).
		var sessionExists int
		var sessIDForInsert sql.NullString
		if sessionID.Valid {
			_ = dst.QueryRowContext(ctx, `SELECT 1 FROM aria_sessions WHERE id = $1`, sessionID.String).Scan(&sessionExists)
			if sessionExists > 0 {
				sessIDForInsert = sessionID
			}
		}
		_, err := dst.ExecContext(ctx, `
			INSERT INTO aria_observations (id, session_id, developer_role, project, scope,
				observation_type, title, subtitle, narrative, facts, concepts, files_touched,
				reasoning_trace, generated_by_model, relevance_count, discovery_tokens,
				quality_score, drift_detected, valid_from, valid_until, superseded_by,
				topic_key, source, created_at, updated_at)
			VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, $7, NULLIF($8,''), NULLIF($9,''),
			        NULLIF($10,''), NULLIF($11,''), NULLIF($12,''),
			        NULLIF($13,'')::jsonb, NULLIF($14,''), $15, $16, $17, $18,
			        COALESCE($19::timestamptz, NOW()), $20::timestamptz, NULLIF($21,''),
			        NULLIF($22,''), $23,
			        COALESCE($24::timestamptz, NOW()), COALESCE($25::timestamptz, NOW()))
			ON CONFLICT (id) DO NOTHING
		`, id, nullToInterface(sessIDForInsert), nullToStringDefault(devRole, "dev"),
			nullToString(project), normalizeScope(scope), obsType, title,
			nullToString(subtitle), nullToString(narrative), nullToString(facts),
			nullToString(concepts), nullToString(filesTouched),
			nullToString(reasoningTrace), nullToString(generatedByModel),
			relevanceCount, discoveryTokens, nullableFloat(qualityScore), driftDetected != 0,
			nullToInterface(validFrom), nullToInterface(validUntil), nullToString(supersededBy),
			nullToString(topicKey), source, nullToInterface(createdAt), nullToInterface(updatedAt))
		if err != nil {
			return n, fmt.Errorf("insert obs %s: %w", id, err)
		}
		n++
	}
	return n, rows.Err()
}

func migrateSkills(ctx context.Context, src, dst *sql.DB, dryRun bool) (int, error) {
	// Sólo intentar si la tabla skills existe
	rows, err := src.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='skills'`)
	if err != nil || !rows.Next() {
		_ = rows.Close()
		return 0, nil
	}
	_ = rows.Close()
	// Legacy schema: sync_id (pk), name, title, content_md, stack (json string), scope, version, synced_at
	skillRows, err := src.QueryContext(ctx, `
		SELECT sync_id, name, COALESCE(title,''), COALESCE(content_md,''), COALESCE(stack,'[]')
		FROM skills
	`)
	if err != nil {
		return 0, nil // No bloquea migración
	}
	defer skillRows.Close()
	n := 0
	for skillRows.Next() {
		var id, name, title, content, stackJSON string
		if err := skillRows.Scan(&id, &name, &title, &content, &stackJSON); err != nil {
			return n, err
		}
		if dryRun {
			n++
			continue
		}
		// stack legacy es JSON string ["typescript","react",...]. Lo convertimos a TEXT[].
		stack := parseStackJSON(stackJSON)
		// description = title (legacy no tiene description separado)
		_, err := dst.ExecContext(ctx, `
			INSERT INTO aria_skills (id, name, description, content, stack, source, active)
			VALUES ($1, $2, $3, $4, $5, 'imported-legacy', TRUE)
			ON CONFLICT (id) DO NOTHING
		`, id, name, title, content, pqStringArrayValueAdmin(stack))
		if err != nil {
			continue
		}
		n++
	}
	return n, skillRows.Err()
}

// parseStackJSON parsea ["a","b","c"] de SQLite a []string Go.
func parseStackJSON(s string) []string {
	var out []string
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return out
	}
	// Naive parse para mantener simple — ya está en formato JSON simple.
	type wrap struct{}
	_ = wrap{}
	// Use json.Unmarshal sin importar otro paquete (ya está usado upstream).
	if err := jsonUnmarshalLocal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// === helpers ===

func nullToString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func nullToStringDefault(s sql.NullString, def string) string {
	if s.Valid && s.String != "" {
		return s.String
	}
	return def
}

func nullToInterface(s sql.NullString) any {
	if s.Valid && s.String != "" {
		return s.String
	}
	return nil
}

func nullableFloat(f sql.NullFloat64) any {
	if f.Valid {
		return f.Float64
	}
	return nil
}

// normalizeScope mapea scopes legacy → set válido del CHECK constraint.
func normalizeScope(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "personal", "project", "team", "global", "client_knowledge":
		return s
	case "client", "cliente":
		return "client_knowledge"
	default:
		return "personal"
	}
}
