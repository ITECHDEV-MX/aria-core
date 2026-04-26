package roi

import (
	"context"
	"testing"
	"time"
)

// timeNow / durationHours: wrappers minimalistas usados por los tests para
// no atar el código a una clock library.
func timeNow() time.Time              { return time.Now().UTC() }
func durationHours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

// fakeService implementa Service como un mock simple para tests del
// CalculateSavings flow sin tocar Postgres.
type fakeService struct {
	rdrPct  float64
	rdrTot  int
	cwrPct  float64
	svrPct  float64
	ttcMins float64
	ttcSamp int
	dttMins float64
	logs    []LogSearchParams
}

func (f *fakeService) CalcTTC(_ context.Context, _ string, _, _ time.Time) (float64, int, error) {
	return f.ttcMins, f.ttcSamp, nil
}
func (f *fakeService) CalcRDR(_ context.Context, _ string, _, _ time.Time) (float64, int, error) {
	return f.rdrPct, f.rdrTot, nil
}
func (f *fakeService) CalcCWR(_ context.Context, _ string, _, _ time.Time) (float64, error) {
	return f.cwrPct, nil
}
func (f *fakeService) CalcSVR(_ context.Context, _ string, _, _ time.Time) (float64, error) {
	return f.svrPct, nil
}
func (f *fakeService) CalcDTT(_ context.Context, _ string, _, _ time.Time) (float64, error) {
	return f.dttMins, nil
}
func (f *fakeService) CalculateSavings(ctx context.Context, devUID string, since, until time.Time) (*SavingsView, error) {
	rdr, _, _ := f.CalcRDR(ctx, devUID, since, until)
	cwr, _ := f.CalcCWR(ctx, devUID, since, until)
	svr, _ := f.CalcSVR(ctx, devUID, since, until)
	dtt, _ := f.CalcDTT(ctx, devUID, since, until)
	dttDelta := BaselineDeployMin - dtt
	if dtt <= 0 {
		dttDelta = 0
	}
	total := SavedMinutesFromPcts(rdr, cwr, svr, dttDelta)
	cpm := DevCostMXNPerMin()
	return &SavingsView{
		WindowDays:        windowDays(since, until),
		TotalSavedMinutes: total,
		TotalSavedMXN:     total * cpm,
		WorkdayPctSaved:   total / WorkdayMinutes,
		ByPillar: map[string]float64{
			"context_dev":    svr*BaselineSkillAppliedMin + dttDelta,
			"claude_skills":  rdr * BaselineInvestigationMin,
			"vault_redactor": cwr * BaselineCredentialsMin,
		},
		ByPillarMXN: map[string]float64{},
		RDR:         rdr,
		CWR:         cwr,
		SVR:         svr,
		DTT:         dtt,
		CostMXNPerMin: cpm,
	}, nil
}
func (f *fakeService) TopContributors(_ context.Context, _ time.Time, _ int) ([]ContributorScore, error) {
	return nil, nil
}
func (f *fakeService) PerClientBreakdown(_ context.Context, _ time.Time) ([]ClientROI, error) {
	return nil, nil
}
func (f *fakeService) WeeklyTimeline(_ context.Context, _ string, _ int, _ float64) ([]WeeklyPoint, error) {
	return nil, nil
}
func (f *fakeService) LogSearch(_ context.Context, p LogSearchParams) error {
	f.logs = append(f.logs, p)
	return nil
}

// Compile-time check.
var _ Service = (*fakeService)(nil)

func TestFakeService_CalculateSavings_AllZero(t *testing.T) {
	fs := &fakeService{}
	since := timeNow().Add(-7 * 24 * time.Hour)
	until := timeNow()
	view, err := fs.CalculateSavings(context.Background(), "", since, until)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if view.TotalSavedMinutes != 0 {
		t.Fatalf("expected 0 saved minutes, got %v", view.TotalSavedMinutes)
	}
	if view.WindowDays != 7 {
		t.Fatalf("expected window 7 days, got %v", view.WindowDays)
	}
}

func TestFakeService_CalculateSavings_AllPerfect_NoDeploy(t *testing.T) {
	fs := &fakeService{rdrPct: 1.0, cwrPct: 1.0, svrPct: 1.0, dttMins: 0}
	since := timeNow().Add(-7 * 24 * time.Hour)
	until := timeNow()
	view, err := fs.CalculateSavings(context.Background(), "", since, until)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := BaselineInvestigationMin + BaselineCredentialsMin + BaselineSkillAppliedMin
	if view.TotalSavedMinutes != want {
		t.Fatalf("expected %v saved minutes, got %v", want, view.TotalSavedMinutes)
	}
	// MXN debe ser totalMin * costPerMin
	if view.TotalSavedMXN != want*view.CostMXNPerMin {
		t.Fatalf("MXN mismatch: got %v want %v", view.TotalSavedMXN, want*view.CostMXNPerMin)
	}
}

func TestFakeService_CalculateSavings_PillarsSumToTotal(t *testing.T) {
	fs := &fakeService{rdrPct: 0.5, cwrPct: 0.7, svrPct: 0.4, dttMins: 30}
	since := timeNow().Add(-30 * 24 * time.Hour)
	until := timeNow()
	view, _ := fs.CalculateSavings(context.Background(), "uid", since, until)
	sum := view.ByPillar["context_dev"] + view.ByPillar["claude_skills"] + view.ByPillar["vault_redactor"]
	if sum != view.TotalSavedMinutes {
		t.Fatalf("pillars sum %v != total %v", sum, view.TotalSavedMinutes)
	}
}

func TestFakeService_CalculateSavings_DeployDeltaContributes(t *testing.T) {
	// dtt=20 minutes -> delta=BaselineDeployMin-20=40 minutes saved.
	fs := &fakeService{rdrPct: 0, cwrPct: 0, svrPct: 0, dttMins: 20}
	since := timeNow().Add(-7 * 24 * time.Hour)
	until := timeNow()
	view, _ := fs.CalculateSavings(context.Background(), "", since, until)
	want := BaselineDeployMin - 20
	if view.TotalSavedMinutes != want {
		t.Fatalf("expected %v saved minutes from DTT delta, got %v", want, view.TotalSavedMinutes)
	}
}

func TestFakeService_LogSearch_RecordsRow(t *testing.T) {
	fs := &fakeService{}
	if err := fs.LogSearch(context.Background(), LogSearchParams{
		Query:         "alpine deploy",
		ResultCount:   3,
		CanonHitCount: 2,
		DeveloperUID:  "00000000-0000-0000-0000-000000000001",
	}); err != nil {
		t.Fatalf("LogSearch err: %v", err)
	}
	if len(fs.logs) != 1 {
		t.Fatalf("expected 1 log row, got %v", len(fs.logs))
	}
	if fs.logs[0].CanonHitCount != 2 {
		t.Fatalf("canon_hit_count not preserved: %v", fs.logs[0])
	}
}

func TestOptionalDevFilter_EmptyReturnsNoClause(t *testing.T) {
	clause, args, idx := optionalDevFilter("", "developer_uid", []any{"x"}, 2)
	if clause != "" {
		t.Fatalf("expected empty clause for empty uid, got %q", clause)
	}
	if len(args) != 1 {
		t.Fatalf("args should not be modified")
	}
	if idx != 2 {
		t.Fatalf("idx should not advance")
	}
}

func TestOptionalDevFilter_NonEmptyAddsClause(t *testing.T) {
	clause, args, idx := optionalDevFilter("uid-123", "developer_uid", []any{"x"}, 2)
	if clause == "" {
		t.Fatalf("expected non-empty clause")
	}
	if len(args) != 2 {
		t.Fatalf("expected args extended, got %v", args)
	}
	if idx != 3 {
		t.Fatalf("expected idx advance to 3, got %v", idx)
	}
}
