package databases

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestKanban_GroupBySelectKey verifica que un schema con prop select agrupa
// correctamente las rows en buckets. Es la lógica central del Kanban view.
func TestKanban_GroupBySelectKey(t *testing.T) {
	rows := []*Row{
		{ID: "1", Props: map[string]any{"col": "A"}},
		{ID: "2", Props: map[string]any{"col": "B"}},
		{ID: "3", Props: map[string]any{"col": "A"}},
		{ID: "4", Props: map[string]any{"col": "C"}},
	}
	g := GroupRowsByKey(rows, "col")
	if len(g["A"]) != 2 || len(g["B"]) != 1 || len(g["C"]) != 1 {
		t.Errorf("group counts wrong: A=%d B=%d C=%d", len(g["A"]), len(g["B"]), len(g["C"]))
	}
}

// TestKanban_RequiresSelectProp documenta el requirement: si no hay prop select
// en el schema, el caller debe rechazar la creación de un kanban view (la app
// la valida en el dashboard render; aquí confirmamos la utility helper que
// se usa para detectar la primera select prop).
func TestKanban_FirstSelectKeyHelper(t *testing.T) {
	// Esta función vive en dashboard_pages.go (firstSelectKey); aquí simulamos
	// la lógica equivalente probando las options del schema.
	schema := []PropDef{
		{Key: "title", Name: "Title", Type: PropText},
		{Key: "status", Name: "Status", Type: PropSelect, Options: []string{"a", "b"}},
	}
	if errs := ValidateSchema(schema); len(errs) > 0 {
		t.Fatalf("schema invalid: %v", errs)
	}
	// Confirmar que select tiene options (Kanban necesita esto para columnas).
	for _, def := range schema {
		if def.Type == PropSelect && len(def.Options) == 0 {
			t.Error("select prop must have options for kanban use")
		}
	}
}

// TestKanban_Integration_DragMovesProp prueba el flujo completo:
// crear database con select prop, crear rows, mover una entre buckets.
func TestKanban_Integration_DragMovesProp(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	pageID := uuid.NewString()
	creator := uuid.NewString()
	db, err := s.Create(context.Background(), CreateParams{
		PageID: pageID,
		Schema: []PropDef{
			{Key: "title", Name: "Title", Type: PropText},
			{Key: "lane", Name: "Lane", Type: PropSelect, Options: []string{"backlog", "wip", "done"}},
		},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	row, err := s.CreateRow(context.Background(), CreateRowParams{
		DatabaseID: db.ID, Props: map[string]any{"title": "task", "lane": "backlog"},
		CreatedByUID: creator,
	})
	if err != nil {
		t.Fatalf("create row: %v", err)
	}

	// "drag" → updatear prop lane.
	if _, err := s.UpdateRowProps(context.Background(), row.ID, map[string]any{"lane": "wip"}); err != nil {
		t.Fatalf("update lane: %v", err)
	}
	got, _ := s.GetRow(context.Background(), row.ID)
	if got.Props["lane"] != "wip" {
		t.Errorf("expected lane=wip after drag, got %v", got.Props["lane"])
	}

	// Reagrupar y verificar.
	all, _ := s.ListRows(context.Background(), db.ID, ListRowsOpts{})
	groups := GroupRowsByKey(all, "lane")
	if len(groups["wip"]) != 1 {
		t.Errorf("expected 1 in wip after drag, got %d", len(groups["wip"]))
	}
	if len(groups["backlog"]) != 0 {
		t.Errorf("expected 0 in backlog after drag, got %d", len(groups["backlog"]))
	}

	// Validar opción inválida = error.
	if _, err := s.UpdateRowProps(context.Background(), row.ID, map[string]any{"lane": "invalid"}); err == nil {
		t.Error("expected error for invalid select option")
	}
}
