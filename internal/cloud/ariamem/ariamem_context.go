package ariamem

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/contextbudget"
)

// SearchOptions habilita ranking inteligente y budgeting de tokens sobre los
// resultados de Search. NO rompe la signature original — Search sigue
// funcionando.
type SearchOptions struct {
	TokenBudget  int
	RankStrategy string
	// MaxCandidates es el número de filas que se traen del DB antes de rankear
	// y truncar por budget. Cuanto mayor, mejor calidad de top-N pero más
	// trabajo en cliente. Default: 50.
	MaxCandidates int
}

// SearchResult contiene el subset que cabe en TokenBudget.
type SearchResult struct {
	Results        []*Observation
	TokensUsed     int
	TruncatedCount int
	Strategy       string
}

// SearchWithBudget hace FTS, rankea con la heurística y devuelve solo lo que
// cabe en opts.TokenBudget. Si TokenBudget <= 0, retorna todo (compatibilidad).
func (s *Store) SearchWithBudget(ctx context.Context, p SearchParams, opts SearchOptions) (SearchResult, error) {
	maxCand := opts.MaxCandidates
	if maxCand <= 0 {
		maxCand = 50
	}
	// Sobreescribimos limit del SearchParams con maxCand para traer más
	// candidatos.
	candParams := p
	if candParams.Limit <= 0 || candParams.Limit > maxCand {
		candParams.Limit = maxCand
	}

	rows, err := s.searchWithRank(ctx, candParams)
	if err != nil {
		return SearchResult{}, err
	}

	if opts.TokenBudget <= 0 {
		return SearchResult{
			Results:    rows.observations,
			TokensUsed: 0,
			Strategy:   opts.RankStrategy,
		}, nil
	}

	strat := contextbudget.RankStrategy(strings.TrimSpace(opts.RankStrategy))
	if strat == "" {
		strat = contextbudget.StrategyCanonFirst
	}

	// Map a contextbudget.Observation y rankeamos.
	counter := contextbudget.NewHeuristicCounter()
	now := time.Now().UTC()
	q := contextbudget.QueryContext{
		Project: strings.TrimSpace(p.Project),
		Scope:   strings.TrimSpace(p.Scope),
		Query:   strings.TrimSpace(p.Query),
	}
	items := make([]contextbudget.ScoredItem, 0, len(rows.observations))
	for i, o := range rows.observations {
		cbObs := contextbudget.Observation{
			ID:            o.ID,
			Title:         o.Title,
			Subtitle:      stringOrEmpty(o.Subtitle),
			Narrative:     stringOrEmpty(o.Narrative),
			Facts:         stringOrEmpty(o.Facts),
			Concepts:      stringOrEmpty(o.Concepts),
			Project:       stringOrEmpty(o.Project),
			Scope:         o.Scope,
			Canon:         o.Canon,
			CreatedAtDays: now.Sub(o.CreatedAt).Hours() / 24.0,
			FTSRank:       rows.ranks[i],
		}
		score := contextbudget.ScoreObservation(cbObs, q, strat)
		items = append(items, contextbudget.ScoredItem{
			Key:    o.ID,
			Score:  score,
			Tokens: counter.CountObservation(cbObs),
			Ref:    o,
		})
	}
	selected, truncated, used := contextbudget.SelectWithinBudget(items, opts.TokenBudget)
	out := make([]*Observation, 0, len(selected))
	for _, it := range selected {
		if v, ok := it.Ref.(*Observation); ok {
			out = append(out, v)
		}
	}
	return SearchResult{
		Results:        out,
		TokensUsed:     used,
		TruncatedCount: truncated,
		Strategy:       string(strat),
	}, nil
}

// searchRows agrupa observations + sus ts_rank.
type searchRows struct {
	observations []*Observation
	ranks        []float64
}

// searchWithRank reusa la lógica de Search pero también devuelve el ts_rank por
// fila — necesario para el ranker multi-signal.
func (s *Store) searchWithRank(ctx context.Context, p SearchParams) (searchRows, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}
	conds := []string{"valid_until IS NULL"}
	args := []any{}
	idx := 1
	queryText := strings.TrimSpace(p.Query)
	if queryText != "" {
		conds = append(conds, fmt.Sprintf("to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(subtitle,'') || ' ' || coalesce(narrative,'') || ' ' || coalesce(facts,'') || ' ' || coalesce(concepts,''))) @@ plainto_tsquery('spanish', unaccent($%d))", idx))
		args = append(args, queryText)
		idx++
	}
	if proj := strings.TrimSpace(p.Project); proj != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, proj)
		idx++
	}
	if scope := strings.TrimSpace(p.Scope); scope != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, scope)
		idx++
	}
	if t := strings.TrimSpace(p.ObservationType); t != "" {
		conds = append(conds, fmt.Sprintf("observation_type = $%d", idx))
		args = append(args, t)
		idx++
	}
	args = append(args, p.Limit)
	rankExpr := "0::real"
	if queryText != "" {
		rankExpr = fmt.Sprintf("ts_rank(to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(subtitle,'') || ' ' || coalesce(narrative,'') || ' ' || coalesce(facts,'') || ' ' || coalesce(concepts,''))), plainto_tsquery('spanish', unaccent($1)))")
	}
	query := observationSelect + ", " + rankExpr + " AS fts_rank" +
		" WHERE " + strings.Join(conds, " AND ") +
		" ORDER BY fts_rank DESC, created_at DESC LIMIT $" + fmt.Sprint(idx)

	rs, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return searchRows{}, err
	}
	defer rs.Close()

	out := searchRows{}
	for rs.Next() {
		o, rank, err := scanObsWithRank(rs)
		if err != nil {
			return searchRows{}, err
		}
		out.observations = append(out.observations, o)
		out.ranks = append(out.ranks, rank)
	}
	return out, rs.Err()
}

// scanObsWithRank lee la misma fila que scanObs + un float trailing.
func scanObsWithRank(rs *sql.Rows) (*Observation, float64, error) {
	var o Observation
	var devUID, clientID sql.NullString
	var rank float64
	if err := rs.Scan(&o.ID, &o.SessionID, &devUID, &o.DeveloperRole, &clientID, &o.Project, &o.Scope,
		&o.ObservationType, &o.Title, &o.Subtitle, &o.Narrative, &o.Facts, &o.Concepts, &o.FilesTouched,
		&o.ReasoningTrace, &o.GeneratedByModel, &o.RelevanceCount, &o.DiscoveryTokens,
		&o.QualityScore, &o.DriftDetected, &o.ValidFrom, &o.ValidUntil, &o.SupersededBy,
		&o.TopicKey, &o.Source, &o.Canon, &o.CreatedAt, &o.UpdatedAt, &rank); err != nil {
		return nil, 0, err
	}
	o.DeveloperUID = devUID
	o.ClientID = clientID
	return &o, rank, nil
}

func stringOrEmpty(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// === Helpers para Injector ===

// TopCanonObservations retorna los N obs canon más recientes para un project.
// Útil al iniciar sesión.
func (s *Store) TopCanonObservations(ctx context.Context, project string, limit int) ([]*Observation, error) {
	if limit <= 0 {
		limit = 5
	}
	q := observationSelect + ` WHERE valid_until IS NULL AND canon = TRUE`
	args := []any{}
	idx := 1
	if p := strings.TrimSpace(project); p != "" {
		q += fmt.Sprintf(" AND project = $%d", idx)
		args = append(args, p)
		idx++
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", idx)
	args = append(args, limit)

	rs, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []*Observation
	for rs.Next() {
		o, err := scanObs(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rs.Err()
}

// SkillsForGoal busca skills cuya descripción matchee la query libre + stack.
func (s *Store) SkillsForGoal(ctx context.Context, goal string, stack []string, limit int) ([]*Skill, error) {
	if limit <= 0 {
		limit = 3
	}
	stackFilter := ""
	if len(stack) > 0 {
		stackFilter = strings.Join(stack, ",")
	}
	skills, err := s.SearchSkills(ctx, goal, "", true)
	if err != nil {
		return nil, err
	}
	// post-filter por stack si se pidió (SearchSkills solo filtra por un stack
	// puntual, aquí hacemos OR sobre varios).
	if stackFilter != "" && len(stack) > 0 {
		filtered := make([]*Skill, 0, len(skills))
		for _, sk := range skills {
			if hasAnyStack(sk.Stack, stack) {
				filtered = append(filtered, sk)
			}
		}
		skills = filtered
	}
	if len(skills) > limit {
		skills = skills[:limit]
	}
	return skills, nil
}

func hasAnyStack(have, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if strings.EqualFold(h, w) {
				return true
			}
		}
	}
	return false
}

// OpenSessionsForDev retorna sesiones del dev sin ended_at (status='active').
func (s *Store) OpenSessionsForDev(ctx context.Context, devUID, excludeSessionID string, limit int) ([]*Session, error) {
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, developer_uid::text, developer_email, developer_role, client_id::text,
		       machine_id, project, directory, goal, status, started_at, ended_at, created_at
		FROM aria_sessions
		WHERE developer_uid = NULLIF($1,'')::uuid
		AND ended_at IS NULL
		AND id <> $2
		ORDER BY started_at DESC LIMIT $3
	`, devUID, excludeSessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var sess Session
		var devUIDNS, clientID, devEmail, project, directory, goal sql.NullString
		if err := rows.Scan(&sess.ID, &devUIDNS, &devEmail, &sess.DeveloperRole, &clientID,
			&sess.MachineID, &project, &directory, &goal, &sess.Status, &sess.StartedAt, &sess.EndedAt, &sess.CreatedAt); err != nil {
			return nil, err
		}
		sess.DeveloperUID = devUIDNS
		sess.DeveloperEmail = devEmail
		sess.ClientID = clientID
		sess.Project = project
		sess.Directory = directory
		sess.Goal = goal
		out = append(out, &sess)
	}
	return out, rows.Err()
}
