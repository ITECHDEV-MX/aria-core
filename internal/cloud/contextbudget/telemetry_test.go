package contextbudget

import (
	"math"
	"testing"
)

func TestWilsonLowerBound_Edges(t *testing.T) {
	cases := []struct {
		helped, used int
		min, max     float64
	}{
		{0, 0, 0, 0},      // no data → 0
		{0, 10, 0, 0.31},  // 0 of 10 → low LB
		{10, 10, 0.69, 1}, // perfect 10/10
		{1, 1, 0, 1},      // 1/1: amplio rango
		{50, 100, 0.39, 0.61},
		{500, 1000, 0.46, 0.54}, // muchos datos → estrecho
	}
	for _, tc := range cases {
		got := WilsonLowerBound(tc.helped, tc.used)
		if got < tc.min || got > tc.max {
			t.Errorf("Wilson(%d/%d) = %v, want in [%v, %v]",
				tc.helped, tc.used, got, tc.min, tc.max)
		}
	}
}

func TestWilsonLowerBound_MoreDataNarrowsBound(t *testing.T) {
	// 50% rate con más datos debe dar LB más alto (más confianza).
	low := WilsonLowerBound(5, 10)
	mid := WilsonLowerBound(50, 100)
	high := WilsonLowerBound(500, 1000)
	if !(low < mid && mid < high) {
		t.Errorf("expected Wilson LB to increase with sample size: %v < %v < %v", low, mid, high)
	}
	// y nunca debe pasar 0.5 (la rate real es 0.5)
	if math.Max(math.Max(low, mid), high) > 0.5 {
		t.Errorf("Wilson LB should not exceed actual rate 0.5")
	}
}

func TestHelpedFromSignal(t *testing.T) {
	if h := HelpedFromSignal(FeedbackCommitReferenced); !h.Valid || !h.Bool {
		t.Errorf("commit_referenced should map to helped=true")
	}
	if h := HelpedFromSignal(FeedbackManualThumbsUp); !h.Valid || !h.Bool {
		t.Errorf("thumbs_up should map to helped=true")
	}
	if h := HelpedFromSignal(FeedbackUsedInRecipe); !h.Valid || !h.Bool {
		t.Errorf("used_in_recipe should map to helped=true")
	}
	if h := HelpedFromSignal(FeedbackManualThumbsDown); !h.Valid || h.Bool {
		t.Errorf("thumbs_down should map to helped=false")
	}
	if h := HelpedFromSignal(FeedbackUnused); h.Valid {
		t.Errorf("unused should map to NULL did_help")
	}
}

func TestValidFeedbackSignal(t *testing.T) {
	good := []string{
		"commit_referenced", "manual_thumbs_up", "manual_thumbs_down",
		"used_in_recipe", "unused",
	}
	for _, s := range good {
		if !ValidFeedbackSignal(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	bad := []string{"", "random", "thumbs_up"}
	for _, s := range bad {
		if ValidFeedbackSignal(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}
