package pages

import (
	"testing"
)

// Los tests de queries SQL (FTS, paralelismo) viven en pages_pg_test.go (build tag pg)
// porque requieren Postgres real. Acá validamos la estructura/shape del result
// y la composición top-level de QuickSearchAll sin tocar DB.

func TestQuickHitFieldsRequired(t *testing.T) {
	hit := QuickHit{
		ID:       "abc",
		Type:     "page",
		Title:    "Hello",
		Subtitle: "Wiki",
		URL:      "/dashboard/pages?id=abc",
		Score:    0.5,
	}
	if hit.ID == "" || hit.Type == "" || hit.Title == "" || hit.URL == "" {
		t.Error("QuickHit must have ID/Type/Title/URL populated")
	}
}

func TestQuickSearchResultEmptyAllReturnsEmpty(t *testing.T) {
	r := &QuickSearchResult{}
	if got := r.All(); len(got) != 0 {
		t.Errorf("expected empty slice from empty result, got %v", got)
	}
}

func TestQuickSearchResultMixedTypes(t *testing.T) {
	r := &QuickSearchResult{
		Pages:   []QuickHit{{ID: "p", Type: "page", Score: 0.3}},
		Recipes: []QuickHit{{ID: "r", Type: "recipe", Score: 0.9}},
		Leads:   []QuickHit{{ID: "l", Type: "lead", Score: 0.5}},
		Quotes:  []QuickHit{{ID: "q", Type: "quote", Score: 0.7}},
	}
	all := r.All()
	if len(all) != 4 {
		t.Fatalf("expected 4 hits, got %d", len(all))
	}
	// Top by score: r > q > l > p
	wantOrder := []string{"r", "q", "l", "p"}
	for i, w := range wantOrder {
		if all[i].ID != w {
			t.Errorf("position %d: want %q, got %q", i, w, all[i].ID)
		}
	}
}
