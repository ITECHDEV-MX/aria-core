package dashboard

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/skills"
	"gopkg.in/yaml.v3"
)

// handleSkillsHealth renders the read-only audit of the skills catalog
// using the same root configured for the validator.
func (h *handlers) handleSkillsHealth(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.SkillsRoot
	if strings.TrimSpace(root) == "" {
		http.Error(w, "SkillsRoot not configured on MountConfig", http.StatusInternalServerError)
		return
	}

	rows, totals, err := buildSkillsHealth(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := frameLayoutFn(w, r, "Skills Health", SkillsHealthPage(rows, totals)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildSkillsHealth walks root + root/_drafts and builds the rows
// + summary totals. Pure read-only; never mutates the catalog.
func buildSkillsHealth(root string) ([]SkillHealthRow, SkillsHealthTotals, error) {
	var rows []SkillHealthRow
	var totals SkillsHealthTotals

	collect := func(dir string, draft bool) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "schema" {
				continue
			}
			path := filepath.Join(dir, name, "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			row := SkillHealthRow{Name: name, IsDraft: draft, BodyChars: len(data)}
			if fm, body, err := splitFM(data); err == nil {
				if v, ok := fm["version"].(string); ok {
					row.Version = v
				}
				if v, ok := fm["owner"].(string); ok {
					row.Owner = v
				}
				if v, ok := fm["last_reviewed"].(string); ok {
					row.LastReviewed = v
				}
				if v, ok := fm["agent"].(bool); ok && v {
					row.Agent = true
				}
				row.HasDossier = bodyHasDossier(body)
				row.BodyChars = len(body)
			}
			rows = append(rows, row)
		}
		return nil
	}

	if err := collect(root, false); err != nil {
		return nil, totals, err
	}
	if err := collect(filepath.Join(root, "_drafts"), true); err != nil {
		return nil, totals, err
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	totals.Total = len(rows)
	for _, r := range rows {
		if r.Agent {
			totals.Agent++
		}
		if r.IsDraft {
			totals.Drafts++
		}
		if isUnreviewedRecent(r.LastReviewed) {
			totals.UnreviewedRecent++
		}
	}
	return rows, totals, nil
}

// splitFM is a thin re-exposure of skills.splitFrontmatter +
// yaml.Unmarshal so this package does not need to import the entire
// validator. We keep the body separately for dossier detection.
func splitFM(data []byte) (map[string]any, string, error) {
	// Find the closing --- after the opening ---
	const sep = "---\n"
	if !strings.HasPrefix(string(data), sep) {
		return nil, "", nil
	}
	rest := string(data[len(sep):])
	end := strings.Index(rest, sep)
	if end < 0 {
		return nil, "", nil
	}
	front := rest[:end]
	body := rest[end+len(sep):]
	var fm map[string]any
	_ = yaml.Unmarshal([]byte(front), &fm)
	return fm, body, nil
}

// bodyHasDossier returns true when the SKILL.md body shows at least
// 3 of the canonical dossier sections used in our PR template.
func bodyHasDossier(body string) bool {
	lower := strings.ToLower(body)
	hits := 0
	for _, marker := range []string{"## when to use", "## rules", "## verification", "## rationale", "## risks"} {
		if strings.Contains(lower, marker) {
			hits++
		}
	}
	return hits >= 3
}

// isUnreviewedRecent flags rows whose last_reviewed is missing or
// older than 6 months. Threshold tunable later.
func isUnreviewedRecent(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return true
	}
	return time.Since(t) > 6*30*24*time.Hour
}

// silenced reference so the import of skills isn't dead-code on builds
// that don't otherwise touch it.
var _ = skills.LockfileName
