package dashboard

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ITECHDEV-MX/aria-core/internal/historias"
	"github.com/a-h/templ"
)

// withStubLayout swaps frameLayoutFn for a plain renderer that just
// emits the templ component body. Restores after the test.
func withStubLayout(t *testing.T) {
	t.Helper()
	prev := frameLayoutFn
	frameLayoutFn = func(w http.ResponseWriter, r *http.Request, title string, comp templ.Component) error {
		return comp.Render(r.Context(), w)
	}
	t.Cleanup(func() { frameLayoutFn = prev })
}

func writeChainFixture(t *testing.T, root, slug string, positions []int) {
	t.Helper()
	for _, p := range positions {
		_, err := historias.SaveArtifact(historias.SaveArtifactArgs{
			Root: root, Slug: slug, Position: p,
			Filename: filepath.Base(filepath.Join("foo", "bar")) + "_" + slug + ".md",
			Skill:    "test@1.0.0", AgentModel: "claude-test",
			Content:   "Test artifact body.",
			CreatedBy: "tester@itechpymes.com.mx",
		})
		// Some calls will fail because filenames don't match position.
		// We use the canonical pattern instead:
		if err != nil {
			_, err = historias.SaveArtifact(historias.SaveArtifactArgs{
				Root: root, Slug: slug, Position: p,
				Filename:  positionFilename(p),
				Skill:     "test@1.0.0", AgentModel: "claude-test",
				Content:   "Test artifact body.",
				CreatedBy: "tester@itechpymes.com.mx",
			})
			if err != nil {
				t.Fatalf("save artifact pos %d: %v", p, err)
			}
		}
	}
}

func positionFilename(pos int) string {
	switch pos {
	case 0:
		return "0-office.md"
	case 1:
		return "1-ceo.md"
	case 2:
		return "2-eng.md"
	case 3:
		return "3-story.md"
	default:
		return "9-misc.md"
	}
}

func TestHandleHistoriasIndex_Empty(t *testing.T) {
	withStubLayout(t)
	root := t.TempDir()
	h := &handlers{cfg: MountConfig{HistoriasRoot: root}}

	req := httptest.NewRequest(http.MethodGet, "/dashboard/historias", nil)
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	h.handleHistoriasIndex(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Aún no hay historias") {
		t.Errorf("expected empty-state copy, got: %s", body)
	}
}

func TestHandleHistoriasIndex_WithChains(t *testing.T) {
	withStubLayout(t)
	root := t.TempDir()
	writeChainFixture(t, root, "alpha", []int{0, 1, 2})
	writeChainFixture(t, root, "beta", []int{0})

	h := &handlers{cfg: MountConfig{HistoriasRoot: root}}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/historias", nil)
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	h.handleHistoriasIndex(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"alpha", "beta", "in_progress"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestHandleHistoriaDetail_RendersChain(t *testing.T) {
	withStubLayout(t)
	root := t.TempDir()
	writeChainFixture(t, root, "alpha", []int{0, 1})

	h := &handlers{cfg: MountConfig{HistoriasRoot: root}}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/historias/alpha", nil)
	req.SetPathValue("slug", "alpha")
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	h.handleHistoriaDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"alpha", "0-office.md", "1-ceo.md", "test@1.0.0"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestHandleHistoriaDetail_NotFound(t *testing.T) {
	withStubLayout(t)
	root := t.TempDir()
	h := &handlers{cfg: MountConfig{HistoriasRoot: root}}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/historias/missing", nil)
	req.SetPathValue("slug", "missing")
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	h.handleHistoriaDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// Sanity check that ListSlugs / ListChain compile with the historias package.
func TestHistoriasPackageWired(t *testing.T) {
	root := t.TempDir()
	if _, err := historias.ListSlugs(root); err != nil {
		t.Errorf("list slugs: %v", err)
	}
	_, _ = io.Copy(io.Discard, strings.NewReader("noop"))
}
