package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/ariamem"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/contextbudget"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// ariaMemAdapter conecta ariamem.Store al contrato cloudserver.AriaMemService.
type ariaMemAdapter struct {
	store     *ariamem.Store
	telemetry *contextbudget.SQLTelemetry
	counter   contextbudget.Counter
}

func newAriaMemAdapter(cs *cloudstore.CloudStore) *ariaMemAdapter {
	db := cs.DB()
	return &ariaMemAdapter{
		store:     ariamem.New(db),
		telemetry: contextbudget.NewSQLTelemetry(db),
		counter:   contextbudget.NewHeuristicCounter(),
	}
}

func (a *ariaMemAdapter) Save(ctx context.Context, in cloudserver.AriaMemSaveInput) (*cloudserver.AriaMemObservation, error) {
	o, err := a.store.Save(ctx, ariamem.SaveParams{
		SessionID: in.SessionID, DeveloperUID: in.DeveloperUID, DeveloperRole: in.DeveloperRole,
		ClientID: in.ClientID, Project: in.Project, Scope: in.Scope,
		ObservationType: in.ObservationType, Title: in.Title, Subtitle: in.Subtitle,
		Narrative: in.Narrative, Facts: in.Facts, Concepts: in.Concepts,
		FilesTouched: in.FilesTouched, ReasoningTrace: in.ReasoningTrace,
		TopicKey: in.TopicKey, Source: in.Source, GeneratedByModel: in.GeneratedByModel,
		Sensitivity: in.Sensitivity,
		ForceSave:   in.ForceSave,
	})
	if err != nil {
		return nil, err
	}
	return toMemObs(o), nil
}

func (a *ariaMemAdapter) GetByID(ctx context.Context, id string) (*cloudserver.AriaMemObservation, error) {
	o, err := a.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return toMemObs(o), nil
}

func (a *ariaMemAdapter) Search(ctx context.Context, in cloudserver.AriaMemSearchInput) ([]*cloudserver.AriaMemObservation, error) {
	rs, err := a.store.Search(ctx, ariamem.SearchParams{
		Query: in.Query, Project: in.Project, Scope: in.Scope,
		ObservationType: in.ObservationType, Limit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*cloudserver.AriaMemObservation, 0, len(rs))
	for _, o := range rs {
		out = append(out, toMemObs(o))
	}
	return out, nil
}

func (a *ariaMemAdapter) Timeline(ctx context.Context, project string, since, until *time.Time, limit int) ([]*cloudserver.AriaMemObservation, error) {
	rs, err := a.store.Timeline(ctx, project, since, until, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*cloudserver.AriaMemObservation, 0, len(rs))
	for _, o := range rs {
		out = append(out, toMemObs(o))
	}
	return out, nil
}

func (a *ariaMemAdapter) PromoteCanon(ctx context.Context, id, byUID string) error {
	return a.store.PromoteCanon(ctx, id, byUID)
}

func (a *ariaMemAdapter) RecordQuality(ctx context.Context, id, signal string, score float64, notes, byUID string) error {
	return a.store.RecordQuality(ctx, id, signal, score, notes, byUID)
}

func (a *ariaMemAdapter) StartSession(ctx context.Context, in cloudserver.AriaMemStartSessionInput) (*cloudserver.AriaMemSession, error) {
	sess, err := a.store.StartSession(ctx, ariamem.StartSessionParams{
		DeveloperUID: in.DeveloperUID, DeveloperEmail: in.DeveloperEmail, DeveloperRole: in.DeveloperRole,
		ClientID: in.ClientID, MachineID: in.MachineID,
		Project: in.Project, Directory: in.Directory, Goal: in.Goal,
	})
	if err != nil {
		return nil, err
	}
	return toMemSession(sess), nil
}

func (a *ariaMemAdapter) GetSession(ctx context.Context, id string) (*cloudserver.AriaMemSession, error) {
	sess, err := a.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return toMemSession(sess), nil
}

func (a *ariaMemAdapter) SaveSummary(ctx context.Context, in cloudserver.AriaMemSaveSummaryInput) error {
	return a.store.SaveSummary(ctx, ariamem.SaveSummaryParams{
		SessionID: in.SessionID, Request: in.Request, Investigated: in.Investigated,
		Learned: in.Learned, Completed: in.Completed, NextSteps: in.NextSteps,
		FilesRead: in.FilesRead, FilesEdited: in.FilesEdited, Notes: in.Notes,
		QualityGrade: in.QualityGrade,
	})
}

func (a *ariaMemAdapter) GetContextStatus(ctx context.Context, project string) (*cloudserver.AriaMemContextStatus, error) {
	st, err := a.store.GetContextStatus(ctx, project)
	if err != nil {
		return nil, err
	}
	return &cloudserver.AriaMemContextStatus{
		Project: st.Project, ActiveSessionID: st.ActiveSessionID,
		TotalObservations: st.TotalObservations, Last7DaysCount: st.Last7DaysCount,
		SkillsLoaded: st.SkillsLoaded,
	}, nil
}

func (a *ariaMemAdapter) ListSkills(ctx context.Context, stack []string) ([]*cloudserver.AriaMemSkill, error) {
	rs, err := a.store.ListSkills(ctx, stack)
	if err != nil {
		return nil, err
	}
	out := make([]*cloudserver.AriaMemSkill, 0, len(rs))
	for _, sk := range rs {
		out = append(out, &cloudserver.AriaMemSkill{
			ID: sk.ID, Name: sk.Name, Description: sk.Description, Content: sk.Content,
			Source: sk.Source, Stack: sk.Stack, Active: sk.Active,
		})
	}
	return out, nil
}

func (a *ariaMemAdapter) GetRecipes(ctx context.Context, taskDescription string, stack []string, limit int) ([]*cloudserver.AriaMemRecipe, error) {
	rs, err := a.store.GetRecipes(ctx, taskDescription, stack, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*cloudserver.AriaMemRecipe, 0, len(rs))
	for _, r := range rs {
		var srcID string
		if r.SourceSessionID.Valid {
			srcID = r.SourceSessionID.String
		}
		out = append(out, &cloudserver.AriaMemRecipe{
			ID: r.ID, TaskPattern: r.TaskPattern, StepsJSON: r.StepsJSON,
			SourceSessionID: srcID, Stack: r.Stack, UsageCount: r.UsageCount,
		})
	}
	return out, nil
}

func toMemObs(o *ariamem.Observation) *cloudserver.AriaMemObservation {
	v := &cloudserver.AriaMemObservation{
		ID: o.ID, DeveloperRole: o.DeveloperRole, Scope: o.Scope,
		ObservationType: o.ObservationType, Title: o.Title, Source: o.Source,
		RelevanceCount: o.RelevanceCount, DiscoveryTokens: o.DiscoveryTokens,
		QualityScore: o.QualityScore, DriftDetected: o.DriftDetected, Canon: o.Canon,
		Sensitivity: o.Sensitivity,
		ValidFrom: o.ValidFrom, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
	if o.SessionID.Valid {
		v.SessionID = o.SessionID.String
	}
	if o.DeveloperUID.Valid {
		v.DeveloperUID = o.DeveloperUID.String
	}
	if o.ClientID.Valid {
		v.ClientID = o.ClientID.String
	}
	if o.Project.Valid {
		v.Project = o.Project.String
	}
	if o.Subtitle.Valid {
		v.Subtitle = o.Subtitle.String
	}
	if o.Narrative.Valid {
		v.Narrative = o.Narrative.String
	}
	if o.Facts.Valid {
		v.Facts = o.Facts.String
	}
	if o.Concepts.Valid {
		v.Concepts = o.Concepts.String
	}
	if o.FilesTouched.Valid {
		v.FilesTouched = o.FilesTouched.String
	}
	if o.ReasoningTrace.Valid {
		v.ReasoningTrace = o.ReasoningTrace.String
	}
	if o.GeneratedByModel.Valid {
		v.GeneratedByModel = o.GeneratedByModel.String
	}
	if o.SupersededBy.Valid {
		v.SupersededBy = o.SupersededBy.String
	}
	if o.TopicKey.Valid {
		v.TopicKey = o.TopicKey.String
	}
	if o.ValidUntil.Valid {
		t := o.ValidUntil.Time
		v.ValidUntil = &t
	}
	return v
}

// === Dashboard adapter — alimenta /dashboard/memorias con aria_observations ===

type ariaMemDashboardAdapter struct {
	store *ariamem.Store
}

func newAriaMemDashboardAdapter(cs *cloudstore.CloudStore) *ariaMemDashboardAdapter {
	return &ariaMemDashboardAdapter{store: ariamem.New(cs.DB())}
}

func (a *ariaMemDashboardAdapter) Search(ctx context.Context, query, project, scope, obsType string, limit int) ([]dashboard.AriaMemoryView, error) {
	rs, err := a.store.Search(ctx, ariamem.SearchParams{
		Query: query, Project: project, Scope: scope, ObservationType: obsType, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.AriaMemoryView, 0, len(rs))
	for _, o := range rs {
		out = append(out, toMemoryView(o))
	}
	return out, nil
}

func (a *ariaMemDashboardAdapter) GetByID(ctx context.Context, id string) (*dashboard.AriaMemoryView, error) {
	o, err := a.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toMemoryView(o)
	return &v, nil
}

func (a *ariaMemDashboardAdapter) PromoteCanon(ctx context.Context, id, byUID string) error {
	return a.store.PromoteCanon(ctx, id, byUID)
}

func (a *ariaMemDashboardAdapter) ListProjects(ctx context.Context) ([]string, error) {
	rows, err := a.store.DBRaw().QueryContext(ctx, `
		SELECT DISTINCT project FROM aria_observations WHERE project IS NOT NULL AND project <> '' ORDER BY project
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// === Skills admin ===

func (a *ariaMemDashboardAdapter) ListAllSkills(ctx context.Context) ([]dashboard.AriaSkillView, error) {
	skills, err := a.store.ListAllSkills(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.AriaSkillView, 0, len(skills))
	for _, s := range skills {
		out = append(out, toSkillView(s))
	}
	return out, nil
}

func (a *ariaMemDashboardAdapter) GetSkillByID(ctx context.Context, id string) (*dashboard.AriaSkillView, error) {
	sk, err := a.store.GetSkillByID(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toSkillView(sk)
	return &v, nil
}

func (a *ariaMemDashboardAdapter) UpsertSkill(ctx context.Context, p dashboard.UpsertAriaSkillInput) error {
	source := p.Source
	if source == "" {
		source = "manual"
	}
	return a.store.UpsertSkill(ctx, ariamem.UpsertSkillParams{
		ID: p.ID, Name: p.Name, Description: p.Description,
		Stack: p.Stack, Content: p.Content, Source: source, Active: p.Active,
	})
}

func (a *ariaMemDashboardAdapter) SetSkillActive(ctx context.Context, id string, active bool) error {
	return a.store.SetSkillActive(ctx, id, active)
}

func (a *ariaMemDashboardAdapter) DeleteSkill(ctx context.Context, id string) error {
	return a.store.DeleteSkill(ctx, id)
}

func (a *ariaMemDashboardAdapter) SearchSkills(ctx context.Context, query, stack string, activeOnly bool) ([]dashboard.AriaSkillView, error) {
	skills, err := a.store.SearchSkills(ctx, query, stack, activeOnly)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.AriaSkillView, 0, len(skills))
	for _, s := range skills {
		out = append(out, toSkillView(s))
	}
	return out, nil
}

func (a *ariaMemDashboardAdapter) ListUniqueStacks(ctx context.Context) ([]string, error) {
	return a.store.ListUniqueStacks(ctx)
}

func toSkillView(s *ariamem.Skill) dashboard.AriaSkillView {
	return dashboard.AriaSkillView{
		ID: s.ID, Name: s.Name, Description: s.Description,
		Stack: s.Stack, Content: s.Content, Source: s.Source, Active: s.Active,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func toMemoryView(o *ariamem.Observation) dashboard.AriaMemoryView {
	v := dashboard.AriaMemoryView{
		ID: o.ID, Scope: o.Scope, ObservationType: o.ObservationType,
		Title: o.Title, Source: o.Source, Canon: o.Canon,
		Sensitivity: o.Sensitivity,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
	if o.SessionID.Valid {
		v.SessionID = o.SessionID.String
	}
	if o.Project.Valid {
		v.Project = o.Project.String
	}
	if o.Subtitle.Valid {
		v.Subtitle = o.Subtitle.String
	}
	if o.Narrative.Valid {
		v.Narrative = o.Narrative.String
	}
	if o.Facts.Valid {
		v.Facts = o.Facts.String
	}
	if o.Concepts.Valid {
		v.Concepts = o.Concepts.String
	}
	if o.FilesTouched.Valid {
		v.FilesTouched = o.FilesTouched.String
	}
	if o.ReasoningTrace.Valid {
		v.ReasoningTrace = o.ReasoningTrace.String
	}
	if o.TopicKey.Valid {
		v.TopicKey = o.TopicKey.String
	}
	return v
}

// === Token budget + telemetry adapters ===

func (a *ariaMemAdapter) SearchWithBudget(ctx context.Context, in cloudserver.AriaMemSearchInput, tokenBudget int, strategy string) ([]*cloudserver.AriaMemObservation, int, int, error) {
	res, err := a.store.SearchWithBudget(ctx, ariamem.SearchParams{
		Query: in.Query, Project: in.Project, Scope: in.Scope,
		ObservationType: in.ObservationType, Limit: in.Limit,
	}, ariamem.SearchOptions{
		TokenBudget:  tokenBudget,
		RankStrategy: strategy,
	})
	if err != nil {
		return nil, 0, 0, err
	}
	out := make([]*cloudserver.AriaMemObservation, 0, len(res.Results))
	for _, o := range res.Results {
		out = append(out, toMemObs(o))
	}
	return out, res.TruncatedCount, res.TokensUsed, nil
}

func (a *ariaMemAdapter) GetSkillsRanked(ctx context.Context, in cloudserver.AriaMemSkillsRankedInput) (*cloudserver.AriaMemSkillsRankedOutput, error) {
	skills, err := a.store.SkillsForGoal(ctx, in.TaskDescription, in.Stack, max(in.Limit, 10))
	if err != nil {
		// Fallback al ListSkills clásico si falla la query con FTS.
		skills, err = a.store.ListSkills(ctx, in.Stack)
		if err != nil {
			return nil, err
		}
	}
	// Bulk effectiveness para calcular Wilson lower bound.
	ids := make([]string, 0, len(skills))
	for _, sk := range skills {
		ids = append(ids, sk.ID)
	}
	effMap := map[string]contextbudget.SkillScored{}
	if a.telemetry != nil {
		if m, err := a.telemetry.BulkEffectiveness(ctx, ids); err == nil {
			effMap = m
		}
	}
	// Score + budget.
	now := time.Now().UTC()
	strategy := contextbudget.RankStrategy(strategy(in.Strategy))
	items := make([]contextbudget.ScoredItem, 0, len(skills))
	for _, sk := range skills {
		eff := effMap[sk.ID].WilsonLB
		cbSk := contextbudget.Skill{
			ID:            sk.ID,
			Name:          sk.Name,
			Description:   sk.Description,
			Content:       sk.Content,
			Stack:         sk.Stack,
			FTSRank:       0.5, // se podría persistir; para ahora un constante razonable
			Effectiveness: eff,
			UpdatedAtDays: now.Sub(sk.UpdatedAt).Hours() / 24.0,
		}
		score := contextbudget.ScoreSkill(cbSk, strategy)
		items = append(items, contextbudget.ScoredItem{
			Key:    sk.ID,
			Score:  score,
			Tokens: a.counter.CountSkill(cbSk),
			Ref:    sk,
		})
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	// Aplicamos primero la selección por budget si se pidió.
	budget := in.TokenBudget
	out := &cloudserver.AriaMemSkillsRankedOutput{Strategy: string(strategy)}
	var selected []contextbudget.ScoredItem
	var truncated, tokens int
	if budget > 0 {
		selected, truncated, tokens = contextbudget.SelectWithinBudget(items, budget)
	} else {
		// sin budget: ordenar por score y respetar limit.
		selected, _, _ = contextbudget.SelectWithinBudget(items, 1<<30)
	}
	if len(selected) > limit {
		// truncar manualmente al limit, contabilizando como truncados.
		truncated += len(selected) - limit
		selected = selected[:limit]
	}
	out.TruncatedCount = truncated
	out.TokensUsed = tokens
	out.Skills = make([]*cloudserver.AriaMemSkill, 0, len(selected))

	for i, it := range selected {
		sk, ok := it.Ref.(*ariamem.Skill)
		if !ok {
			continue
		}
		out.Skills = append(out.Skills, &cloudserver.AriaMemSkill{
			ID: sk.ID, Name: sk.Name, Description: sk.Description, Content: sk.Content,
			Source: sk.Source, Stack: sk.Stack, Active: sk.Active,
		})
		// Telemetry: registrar retrieval. No bloquear el response si falla.
		if a.telemetry != nil {
			_ = a.telemetry.RecordRetrieval(ctx, contextbudget.RecordRetrievalParams{
				SkillID:           sk.ID,
				SessionID:         in.SessionID,
				DeveloperUID:      in.DeveloperUID,
				Project:           in.Project,
				TaskDescription:   in.TaskDescription,
				PositionInResults: i + 1,
			})
		}
	}
	return out, nil
}

func (a *ariaMemAdapter) BuildSessionAutoContext(ctx context.Context, in cloudserver.AriaMemAutoContextInput) (*cloudserver.AriaMemAutoContextOutput, error) {
	srcs := contextbudget.InjectorSources{
		TopCanonObservations: func(project string, limit int) ([]contextbudget.Observation, error) {
			rows, err := a.store.TopCanonObservations(ctx, project, limit)
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			out := make([]contextbudget.Observation, 0, len(rows))
			for _, o := range rows {
				out = append(out, contextbudget.Observation{
					ID: o.ID, Title: o.Title,
					Subtitle:      ns(o.Subtitle),
					Narrative:     ns(o.Narrative),
					Facts:         ns(o.Facts),
					Concepts:      ns(o.Concepts),
					Project:       ns(o.Project),
					Scope:         o.Scope,
					Canon:         o.Canon,
					CreatedAtDays: now.Sub(o.CreatedAt).Hours() / 24.0,
					FTSRank:       0.5,
				})
			}
			return out, nil
		},
		SkillsForGoal: func(goal string, stack []string, limit int) ([]contextbudget.Skill, error) {
			rows, err := a.store.SkillsForGoal(ctx, goal, stack, limit)
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			out := make([]contextbudget.Skill, 0, len(rows))
			for _, sk := range rows {
				out = append(out, contextbudget.Skill{
					ID:            sk.ID,
					Name:          sk.Name,
					Description:   sk.Description,
					Content:       sk.Content,
					Stack:         sk.Stack,
					FTSRank:       0.5,
					UpdatedAtDays: now.Sub(sk.UpdatedAt).Hours() / 24.0,
				})
			}
			return out, nil
		},
		RecipesForStack: func(taskDescription string, stack []string, limit int) ([]contextbudget.Recipe, error) {
			rows, err := a.store.GetRecipes(ctx, taskDescription, stack, limit)
			if err != nil {
				return nil, err
			}
			out := make([]contextbudget.Recipe, 0, len(rows))
			for _, r := range rows {
				out = append(out, contextbudget.Recipe{
					ID:          r.ID,
					TaskPattern: r.TaskPattern,
					StepsJSON:   r.StepsJSON,
					Stack:       r.Stack,
				})
			}
			return out, nil
		},
		OpenSessionsForDev: func(devUID, excludeSessionID string, limit int) ([]contextbudget.OpenSessionRef, error) {
			rows, err := a.store.OpenSessionsForDev(ctx, devUID, excludeSessionID, limit)
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			out := make([]contextbudget.OpenSessionRef, 0, len(rows))
			for _, sess := range rows {
				out = append(out, contextbudget.OpenSessionRef{
					ID:         sess.ID,
					Project:    ns(sess.Project),
					Goal:       ns(sess.Goal),
					StartedAgo: contextbudget.HumanAgo(sess.StartedAt, now),
				})
			}
			return out, nil
		},
	}
	res := contextbudget.BuildSessionStartContext(contextbudget.SessionStartParams{
		Project:      in.Project,
		Goal:         in.Goal,
		Stack:        in.Stack,
		DeveloperUID: in.DeveloperUID,
		SessionID:    in.SessionID,
		TokenBudget:  in.TokenBudget,
	}, a.counter, srcs)
	return &cloudserver.AriaMemAutoContextOutput{
		Markdown:       res.Markdown,
		TokensUsed:     res.TokensUsed,
		TruncatedItems: res.TruncatedItems,
	}, nil
}

func (a *ariaMemAdapter) RecordSkillFeedback(ctx context.Context, in cloudserver.AriaMemSkillFeedbackInput) error {
	if a.telemetry == nil {
		return nil
	}
	signal := contextbudget.FeedbackSignal(in.Signal)
	if !contextbudget.ValidFeedbackSignal(in.Signal) {
		return contextbudget.ErrInvalidSignal
	}
	helped := contextbudget.HelpedFromSignal(signal)
	// Si el caller pasó un explicit Helped, sobrescribe la inferencia.
	if in.Helped != nil {
		helped = sql.NullBool{Bool: *in.Helped, Valid: true}
	}
	return a.telemetry.RecordFeedback(ctx, in.SkillID, in.DeveloperUID, signal, helped, in.Notes)
}

func ns(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func strategy(s string) string {
	if s == "" {
		return string(contextbudget.StrategyCanonFirst)
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func toMemSession(sess *ariamem.Session) *cloudserver.AriaMemSession {
	v := &cloudserver.AriaMemSession{
		ID: sess.ID, DeveloperRole: sess.DeveloperRole, MachineID: sess.MachineID,
		Status: sess.Status, StartedAt: sess.StartedAt, CreatedAt: sess.CreatedAt,
	}
	if sess.DeveloperUID.Valid {
		v.DeveloperUID = sess.DeveloperUID.String
	}
	if sess.DeveloperEmail.Valid {
		v.DeveloperEmail = sess.DeveloperEmail.String
	}
	if sess.ClientID.Valid {
		v.ClientID = sess.ClientID.String
	}
	if sess.Project.Valid {
		v.Project = sess.Project.String
	}
	if sess.Directory.Valid {
		v.Directory = sess.Directory.String
	}
	if sess.Goal.Valid {
		v.Goal = sess.Goal.String
	}
	if sess.EndedAt.Valid {
		t := sess.EndedAt.Time
		v.EndedAt = &t
	}
	return v
}
