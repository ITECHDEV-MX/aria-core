package main

import (
	"context"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/ariamem"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// ariaMemAdapter conecta ariamem.Store al contrato cloudserver.AriaMemService.
type ariaMemAdapter struct {
	store *ariamem.Store
}

func newAriaMemAdapter(cs *cloudstore.CloudStore) *ariaMemAdapter {
	return &ariaMemAdapter{store: ariamem.New(cs.DB())}
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
