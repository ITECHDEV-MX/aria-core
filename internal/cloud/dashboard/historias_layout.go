package dashboard

import (
	"net/http"

	"github.com/a-h/templ"
)

// frameLayoutFn is the extension hook the historias handlers use to
// render a templ.Component inside the standard dashboard frame.
//
// Default implementation: render the component directly. The real
// dashboard layout (with sidebar, breadcrumbs, etc.) is wrapped by
// the existing frame helpers in dashboard.go — but those are
// internal. To avoid pulling those internals into historias_handlers,
// we ship a minimal default and let the test/integration layer
// override if needed.
//
// Tests can stub this var. In production, this just renders the
// component into a basic HTML shell.
var frameLayoutFn = func(w http.ResponseWriter, r *http.Request, title string, comp templ.Component) error {
	_, _ = w.Write([]byte(`<!doctype html><html lang="es"><head><meta charset="utf-8"><title>`))
	_, _ = w.Write([]byte(title))
	_, _ = w.Write([]byte(` — ARIA Core</title><link rel="stylesheet" href="/dashboard/static/aria-styles.css"></head><body class="dashboard-shell"><main class="frame">`))
	if err := comp.Render(r.Context(), w); err != nil {
		return err
	}
	_, _ = w.Write([]byte(`</main></body></html>`))
	return nil
}
