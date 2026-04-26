package pages

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// QuickHit es una unidad de resultado para Cmd+K — uniforme entre 6 fuentes.
type QuickHit struct {
	ID       string
	Type     string // page | observation | skill | recipe | lead | quote
	Title    string
	Subtitle string
	URL      string
	Score    float64
}

// QuickSearchResult agrupa hits por tipo (top-3 cada uno por defecto).
type QuickSearchResult struct {
	Pages        []QuickHit
	Observations []QuickHit
	Skills       []QuickHit
	Recipes      []QuickHit
	Leads        []QuickHit
	Quotes       []QuickHit
}

// All retorna los hits combinados ordenados por Score desc.
func (r *QuickSearchResult) All() []QuickHit {
	if r == nil {
		return nil
	}
	out := []QuickHit{}
	out = append(out, r.Pages...)
	out = append(out, r.Observations...)
	out = append(out, r.Skills...)
	out = append(out, r.Recipes...)
	out = append(out, r.Leads...)
	out = append(out, r.Quotes...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// SearchPages busca páginas (no archivadas, no templates) por FTS spanish + unaccent.
func (s *PgStore) SearchPages(ctx context.Context, query, project string, limit int) ([]QuickHit, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pages: store not initialized")
	}
	if limit <= 0 {
		limit = 10
	}
	q := strings.TrimSpace(query)
	conds := []string{"NOT is_archived", "page_type <> 'template'"}
	args := []any{}
	idx := 1
	rankExpr := "0.0"
	if q != "" {
		// Usamos to_tsvector + unaccent() en query — coincide con patrón ariamem.
		conds = append(conds, fmt.Sprintf(
			"to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(content_md,''))) @@ plainto_tsquery('spanish', unaccent($%d))", idx))
		rankExpr = fmt.Sprintf("ts_rank(to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(content_md,''))), plainto_tsquery('spanish', unaccent($%d)))", idx)
		args = append(args, q)
		idx++
	}
	if p := strings.TrimSpace(project); p != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, p)
		idx++
	}
	args = append(args, limit)
	sqlStr := fmt.Sprintf(`
		SELECT id::text, title, COALESCE(icon,''), COALESCE(project,''), %s AS score
		FROM aria_pages
		WHERE %s
		ORDER BY score DESC, updated_at DESC
		LIMIT $%d`, rankExpr, strings.Join(conds, " AND "), idx)
	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("pages: search: %w", err)
	}
	defer rows.Close()
	hits := []QuickHit{}
	for rows.Next() {
		var id, title, icon, proj string
		var score float64
		if err := rows.Scan(&id, &title, &icon, &proj, &score); err != nil {
			return nil, fmt.Errorf("pages: search scan: %w", err)
		}
		display := title
		if icon != "" {
			display = icon + " " + title
		}
		sub := proj
		if sub == "" {
			sub = "Wiki"
		}
		hits = append(hits, QuickHit{
			ID:       id,
			Type:     "page",
			Title:    display,
			Subtitle: sub,
			URL:      "/dashboard/pages?id=" + id,
			Score:    score + 0.1, // pages reciben pequeño boost — son la fuente canónica del wiki
		})
	}
	return hits, rows.Err()
}

// QuickSearchAll corre 6 queries paralelas (pages + obs + skills + recipes + leads + quotes)
// y retorna un combinado top-N por fuente. Si una fuente falla, se loguea y devuelve [].
func (s *PgStore) QuickSearchAll(ctx context.Context, query string, limit int) (*QuickSearchResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pages: store not initialized")
	}
	if limit <= 0 {
		limit = 3
	}
	out := &QuickSearchResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	type job struct {
		name string
		run  func() ([]QuickHit, error)
	}
	jobs := []job{
		{"pages", func() ([]QuickHit, error) { return s.SearchPages(ctx, query, "", limit) }},
		{"observations", func() ([]QuickHit, error) { return s.searchObservations(ctx, query, limit) }},
		{"skills", func() ([]QuickHit, error) { return s.searchSkills(ctx, query, limit) }},
		{"recipes", func() ([]QuickHit, error) { return s.searchRecipes(ctx, query, limit) }},
		{"leads", func() ([]QuickHit, error) { return s.searchLeads(ctx, query, limit) }},
		{"quotes", func() ([]QuickHit, error) { return s.searchQuotes(ctx, query, limit) }},
	}

	for _, j := range jobs {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits, err := j.run()
			mu.Lock()
			defer mu.Unlock()
			if err != nil || len(hits) == 0 {
				// Failures de fuentes individuales no abortan el set — Cmd+K
				// es best-effort. Si una tabla no existe (edge), simplemente vacía.
				return
			}
			switch j.name {
			case "pages":
				out.Pages = hits
			case "observations":
				out.Observations = hits
			case "skills":
				out.Skills = hits
			case "recipes":
				out.Recipes = hits
			case "leads":
				out.Leads = hits
			case "quotes":
				out.Quotes = hits
			}
		}()
	}
	wg.Wait()
	return out, nil
}

// ─── Per-source search functions ────────────────────────────────────────────
//
// Cada función habla directamente con su tabla canónica. Si la tabla
// no existe (edge: clean DB pre-migrations), el QueryContext devuelve error
// y el caller en QuickSearchAll lo silencia (best-effort).

func (s *PgStore) searchObservations(ctx context.Context, query string, limit int) ([]QuickHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		const sqlStr = `
			SELECT id, title, COALESCE(project,''), COALESCE(scope,'')
			FROM aria_observations
			WHERE COALESCE(superseded_by,'') = ''
			ORDER BY created_at DESC LIMIT $1`
		return scanGenericHits(ctx, s.db, "observation", "/dashboard/memorias/", sqlStr, limit)
	}
	const sqlStr = `
		SELECT id, title, COALESCE(project,''), COALESCE(scope,''),
		       ts_rank(to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(narrative,''))),
		               plainto_tsquery('spanish', unaccent($1))) AS score
		FROM aria_observations
		WHERE to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(narrative,'')))
		      @@ plainto_tsquery('spanish', unaccent($1))
		      AND COALESCE(superseded_by,'') = ''
		ORDER BY score DESC LIMIT $2`
	return scanScoredHits(ctx, s.db, "observation", "/dashboard/memorias/", sqlStr, q, limit)
}

func (s *PgStore) searchSkills(ctx context.Context, query string, limit int) ([]QuickHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		const sqlStr = `
			SELECT id, name, COALESCE(description,''), ''
			FROM aria_skills WHERE active = TRUE
			ORDER BY updated_at DESC LIMIT $1`
		return scanGenericHits(ctx, s.db, "skill", "/dashboard/admin/skills/", sqlStr, limit)
	}
	const sqlStr = `
		SELECT id, name, COALESCE(description,''), '',
		       ts_rank(to_tsvector('spanish', unaccent(coalesce(name,'') || ' ' || coalesce(description,'') || ' ' || coalesce(content,''))),
		               plainto_tsquery('spanish', unaccent($1))) AS score
		FROM aria_skills
		WHERE active = TRUE
		      AND to_tsvector('spanish', unaccent(coalesce(name,'') || ' ' || coalesce(description,'') || ' ' || coalesce(content,'')))
		          @@ plainto_tsquery('spanish', unaccent($1))
		ORDER BY score DESC LIMIT $2`
	return scanScoredHits(ctx, s.db, "skill", "/dashboard/admin/skills/", sqlStr, q, limit)
}

func (s *PgStore) searchRecipes(ctx context.Context, query string, limit int) ([]QuickHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		const sqlStr = `
			SELECT id, COALESCE(recipe_key, id), task_pattern, ''
			FROM aria_recipes
			ORDER BY id LIMIT $1`
		return scanGenericHits(ctx, s.db, "recipe", "/dashboard/recipes/", sqlStr, limit)
	}
	const sqlStr = `
		SELECT id, COALESCE(recipe_key, id), task_pattern, '',
		       ts_rank(to_tsvector('spanish', unaccent(coalesce(task_pattern,''))),
		               plainto_tsquery('spanish', unaccent($1))) AS score
		FROM aria_recipes
		WHERE to_tsvector('spanish', unaccent(coalesce(task_pattern,'')))
		      @@ plainto_tsquery('spanish', unaccent($1))
		ORDER BY score DESC LIMIT $2`
	return scanScoredHits(ctx, s.db, "recipe", "/dashboard/recipes/", sqlStr, q, limit)
}

func (s *PgStore) searchLeads(ctx context.Context, query string, limit int) ([]QuickHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		const sqlStr = `
			SELECT id::text, name, COALESCE(company,''), COALESCE(status,'')
			FROM cotizador_leads
			ORDER BY created_at DESC LIMIT $1`
		return scanGenericHits(ctx, s.db, "lead", "/dashboard/cotizador/leads/", sqlStr, limit)
	}
	const sqlStr = `
		SELECT id::text, name, COALESCE(company,''), COALESCE(status,''),
		       ts_rank(to_tsvector('spanish', unaccent(coalesce(name,'') || ' ' || coalesce(company,'') || ' ' || coalesce(notes,''))),
		               plainto_tsquery('spanish', unaccent($1))) AS score
		FROM cotizador_leads
		WHERE to_tsvector('spanish', unaccent(coalesce(name,'') || ' ' || coalesce(company,'') || ' ' || coalesce(notes,'')))
		      @@ plainto_tsquery('spanish', unaccent($1))
		ORDER BY score DESC LIMIT $2`
	return scanScoredHits(ctx, s.db, "lead", "/dashboard/cotizador/leads/", sqlStr, q, limit)
}

func (s *PgStore) searchQuotes(ctx context.Context, query string, limit int) ([]QuickHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		const sqlStr = `
			SELECT id::text, COALESCE(NULLIF(folio,''), 'Quote ' || id::text), COALESCE(product_name,''), COALESCE(status,'')
			FROM cotizador_quotes
			ORDER BY created_at DESC LIMIT $1`
		return scanGenericHits(ctx, s.db, "quote", "/dashboard/cotizador/quotes/", sqlStr, limit)
	}
	const sqlStr = `
		SELECT id::text, COALESCE(NULLIF(folio,''), 'Quote ' || id::text),
		       COALESCE(product_name,''), COALESCE(status,''),
		       ts_rank(to_tsvector('spanish', unaccent(coalesce(folio,'') || ' ' || coalesce(product_name,'') || ' ' || coalesce(justification,'') || ' ' || coalesce(terms,''))),
		               plainto_tsquery('spanish', unaccent($1))) AS score
		FROM cotizador_quotes
		WHERE to_tsvector('spanish', unaccent(coalesce(folio,'') || ' ' || coalesce(product_name,'') || ' ' || coalesce(justification,'') || ' ' || coalesce(terms,'')))
		      @@ plainto_tsquery('spanish', unaccent($1))
		ORDER BY score DESC LIMIT $2`
	return scanScoredHits(ctx, s.db, "quote", "/dashboard/cotizador/quotes/", sqlStr, q, limit)
}

// scanGenericHits ejecuta una query sin score (orden por created_at u otro)
// y la mapea al QuickHit con score=0.0 (caller agrega boost si quiere).
func scanGenericHits(ctx context.Context, db *sql.DB, kind, urlPrefix, sqlStr string, limit int) ([]QuickHit, error) {
	rows, err := db.QueryContext(ctx, sqlStr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []QuickHit{}
	for rows.Next() {
		var id, title, sub1, sub2 string
		if err := rows.Scan(&id, &title, &sub1, &sub2); err != nil {
			return nil, err
		}
		hits = append(hits, QuickHit{
			ID:       id,
			Type:     kind,
			Title:    title,
			Subtitle: composeSubtitle(sub1, sub2),
			URL:      urlPrefix + id,
			Score:    0.01, // valor mínimo pero positivo para no caer del orden
		})
	}
	return hits, rows.Err()
}

func scanScoredHits(ctx context.Context, db *sql.DB, kind, urlPrefix, sqlStr, query string, limit int) ([]QuickHit, error) {
	rows, err := db.QueryContext(ctx, sqlStr, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []QuickHit{}
	for rows.Next() {
		var id, title, sub1, sub2 string
		var score float64
		if err := rows.Scan(&id, &title, &sub1, &sub2, &score); err != nil {
			return nil, err
		}
		hits = append(hits, QuickHit{
			ID:       id,
			Type:     kind,
			Title:    title,
			Subtitle: composeSubtitle(sub1, sub2),
			URL:      urlPrefix + id,
			Score:    score,
		})
	}
	return hits, rows.Err()
}

func composeSubtitle(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, " · ")
}
