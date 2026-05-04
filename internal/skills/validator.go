package skills

import (
	"bytes"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateDir walks dir looking for SKILL.md files (one per
// subdirectory) and validates each. Returns an aggregated Result.
//
// dir is typically the repo's "skills/" directory.
func ValidateDir(dir string) (Result, error) {
	var r Result
	entries, err := os.ReadDir(dir)
	if err != nil {
		return r, fmt.Errorf("read skills dir %s: %w", dir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip the "schema" directory and any other meta-folders.
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "schema" {
			continue
		}

		skillPath := filepath.Join(dir, name, "SKILL.md")
		if _, statErr := os.Stat(skillPath); statErr != nil {
			r.Findings = append(r.Findings, Finding{
				Path:     filepath.Join(name, "SKILL.md"),
				Field:    "file",
				Severity: SeverityWarning,
				Message:  "skill directory exists but no SKILL.md found",
			})
			continue
		}

		fileFindings, err := ValidateFile(skillPath, name)
		if err != nil {
			r.Findings = append(r.Findings, Finding{
				Path:     skillPath,
				Field:    "file",
				Severity: SeverityError,
				Message:  err.Error(),
			})
			r.Skills++
			continue
		}
		r.Findings = append(r.Findings, fileFindings...)
		r.Skills++
	}

	r.summarize()
	return r, nil
}

// ValidateFile validates a single SKILL.md file. dirName is the
// expected `name` field (parent directory). Returns findings; an
// error is only returned for unrecoverable I/O issues.
func ValidateFile(path, dirName string) ([]Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	front, body, err := splitFrontmatter(data)
	if err != nil {
		return []Finding{{
			Path: path, Field: "frontmatter", Severity: SeverityError,
			Message: err.Error(),
		}}, nil
	}

	var fm map[string]any
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return []Finding{{
			Path: path, Field: "frontmatter", Severity: SeverityError,
			Message: "YAML parse: " + err.Error(),
		}}, nil
	}

	findings := validateFrontmatter(path, dirName, fm)
	findings = append(findings, validateBody(path, body)...)
	return findings, nil
}

// frontmatterSep is the ``---`` marker.
var frontmatterSep = []byte("---\n")

// splitFrontmatter peels the top YAML block from a markdown file.
// Returns (frontmatter, body, error). Body is empty if file is
// frontmatter-only.
func splitFrontmatter(data []byte) ([]byte, []byte, error) {
	// Normalize CRLF
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	if !bytes.HasPrefix(data, frontmatterSep) {
		return nil, nil, fmt.Errorf("file does not start with --- frontmatter")
	}
	rest := data[len(frontmatterSep):]
	end := bytes.Index(rest, frontmatterSep)
	if end < 0 {
		return nil, nil, fmt.Errorf("frontmatter never closed (missing trailing ---)")
	}
	front := rest[:end]
	body := rest[end+len(frontmatterSep):]
	return front, body, nil
}

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	semverRe  = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z\-\.]+)?$`)
	dateRe    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	domainRe  = regexp.MustCompile(`^[a-z0-9\-]+$`)
	artifactRe = regexp.MustCompile(`^\d+-[a-z0-9\-]+\.md$`)
)

// validateFrontmatter checks fm against the canonical schema.
// dirName is the parent dir; the `name` field must match it.
func validateFrontmatter(path, dirName string, fm map[string]any) []Finding {
	var out []Finding
	require := func(field string, ok bool, msg string) {
		if !ok {
			out = append(out, Finding{Path: path, Field: field, Severity: SeverityError, Message: msg})
		}
	}

	// name
	nameStr, _ := fm["name"].(string)
	require("name", nameStr != "", "is required")
	if nameStr != "" {
		if len(nameStr) > 64 {
			out = append(out, Finding{Path: path, Field: "name", Severity: SeverityError,
				Message: "exceeds 64 chars"})
		}
		if !nameRe.MatchString(nameStr) {
			out = append(out, Finding{Path: path, Field: "name", Severity: SeverityError,
				Message: "must match ^[a-z0-9]+(-[a-z0-9]+)*$ (no double hyphens, no leading/trailing)"})
		}
		if dirName != "" && nameStr != dirName {
			out = append(out, Finding{Path: path, Field: "name", Severity: SeverityError,
				Message: fmt.Sprintf("name=%q must match parent directory %q", nameStr, dirName)})
		}
	}

	// description
	descStr, _ := fm["description"].(string)
	require("description", strings.TrimSpace(descStr) != "", "is required")
	if l := len(descStr); l > 0 && (l < 10 || l > 1024) {
		out = append(out, Finding{Path: path, Field: "description", Severity: SeverityError,
			Message: fmt.Sprintf("length %d not in [10, 1024]", l)})
	}

	// version
	versionStr, _ := fm["version"].(string)
	require("version", versionStr != "", "is required (use semver)")
	if versionStr != "" && !semverRe.MatchString(versionStr) {
		out = append(out, Finding{Path: path, Field: "version", Severity: SeverityError,
			Message: fmt.Sprintf("%q is not valid semver", versionStr)})
	}

	// owner
	ownerStr, _ := fm["owner"].(string)
	require("owner", ownerStr != "", "is required (maintainer email)")
	if ownerStr != "" {
		if _, err := mail.ParseAddress(ownerStr); err != nil {
			out = append(out, Finding{Path: path, Field: "owner", Severity: SeverityError,
				Message: fmt.Sprintf("not a valid email: %v", err)})
		}
	}

	// expertise_domains (optional)
	if domainsAny, ok := fm["expertise_domains"]; ok {
		if list, lok := domainsAny.([]any); lok {
			for i, v := range list {
				if s, sok := v.(string); sok {
					if !domainRe.MatchString(s) {
						out = append(out, Finding{Path: path, Field: fmt.Sprintf("expertise_domains[%d]", i),
							Severity: SeverityWarning,
							Message:  fmt.Sprintf("%q should be lowercase a-z 0-9 hyphens", s)})
					}
				}
			}
		}
	}

	// last_reviewed (optional)
	if lr, ok := fm["last_reviewed"].(string); ok && lr != "" && !dateRe.MatchString(lr) {
		out = append(out, Finding{Path: path, Field: "last_reviewed", Severity: SeverityError,
			Message: fmt.Sprintf("%q must be YYYY-MM-DD", lr)})
	}

	// agent fields (optional but consistent if any present)
	agentBool, agentOK := fm["agent"].(bool)
	if agentOK && agentBool {
		// position_in_chain required
		if _, ok := fm["position_in_chain"]; !ok {
			out = append(out, Finding{Path: path, Field: "position_in_chain",
				Severity: SeverityError,
				Message: "required when agent: true"})
		}
		// outputs.artifact pattern
		if outAny, ok := fm["outputs"].(map[string]any); ok {
			if art, ok2 := outAny["artifact"].(string); ok2 {
				if !artifactRe.MatchString(art) {
					out = append(out, Finding{Path: path, Field: "outputs.artifact",
						Severity: SeverityError,
						Message:  fmt.Sprintf("%q must match ^\\d+-[a-z0-9-]+\\.md$", art)})
				}
			}
		}
	}

	return out
}

// validateBody runs body-level rules (Gentleman-Skills inspired).
// Soft (warn) for now — promote to error in F1.b once authors have
// adapted.
func validateBody(path string, body []byte) []Finding {
	var out []Finding
	bodyStr := string(body)
	trimmed := strings.TrimSpace(bodyStr)

	if len(trimmed) < 200 {
		out = append(out, Finding{
			Path: path, Field: "body", Severity: SeverityWarning,
			Message: fmt.Sprintf("body is only %d chars; aim for ≥ 500 with When to Use / Rules / Verification sections", len(trimmed)),
		})
	}
	requireSection := func(heading string) {
		// Match `## Heading` or `### Heading` case-insensitive
		needle := strings.ToLower(heading)
		if !strings.Contains(strings.ToLower(bodyStr), "## "+needle) &&
			!strings.Contains(strings.ToLower(bodyStr), "### "+needle) {
			out = append(out, Finding{
				Path: path, Field: "body", Severity: SeverityWarning,
				Message: fmt.Sprintf("missing recommended section: %q", heading),
			})
		}
	}
	requireSection("when to use")
	requireSection("verification")

	return out
}
