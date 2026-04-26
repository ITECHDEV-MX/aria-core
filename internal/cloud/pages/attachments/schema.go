// Package attachments provides file/image attachments and public share links
// for aria_pages rows.
//
// Storage layout:
//   {root}/{yyyy}/{mm}/{uuid}.{ext}   — original files
//   {root}/thumbnails/{uuid}.jpg      — generated previews (image/* and pdf)
//   {root}/tombstone/{uuid}.{ext}     — soft-deleted files (purged by janitor)
//
// Migrations:
//   The DDL is embedded here so cloudstore.go can call attachments.Migrate(ctx, db)
//   at the end of its own migrate() function. The migration is idempotent.
//
// Coordination with the Agent PAGES (which owns aria_pages):
//   The DDL does NOT add a hard FK because aria_pages may be created in a parallel
//   migration. We attach the FK opportunistically post-table creation; if the parent
//   table is missing, the ALTER is logged and skipped — the attachment table still
//   works because the FK is purely advisory.
package attachments

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed attachments_schema.sql
var schemaSQL string

// Migrate aplica el schema de attachments y share links. Idempotente.
//
// Called from cloudstore.runMigrations() after the BEGIN ATTACHMENTS MIGRATIONS marker.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("attachments: nil db")
	}
	for _, stmt := range splitSQLStatements(schemaSQL) {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("attachments: migrate stmt %q: %w", firstLine(s), err)
		}
	}
	// Best-effort: add FK to aria_pages once the parent table exists. If aria_pages
	// is not yet present (PAGES agent migration ran later), this NOT EXISTS query
	// silently no-ops so we don't break the cloudstore boot sequence.
	addFKIfPossible(ctx, db, "aria_page_attachments", "fk_page_attachments_page", "page_id")
	addFKIfPossible(ctx, db, "aria_page_share_links", "fk_page_share_links_page", "page_id")
	return nil
}

// addFKIfPossible attaches a deferred FK to aria_pages(id) if and only if the
// parent table exists and the constraint is not already present. Errors are
// swallowed because the migration is meant to be tolerant of partial state.
func addFKIfPossible(ctx context.Context, db *sql.DB, table, fkName, column string) {
	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'aria_pages'
		)`).Scan(&exists); err != nil || !exists {
		return
	}
	var constraintExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.table_constraints
			WHERE table_name = $1 AND constraint_name = $2
		)`, table, fkName).Scan(&constraintExists); err != nil || constraintExists {
		return
	}
	stmt := fmt.Sprintf(
		`ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES aria_pages(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED`,
		table, fkName, column,
	)
	_, _ = db.ExecContext(ctx, stmt)
}

func splitSQLStatements(src string) []string {
	out := []string{}
	var buf strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "--") {
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		if strings.HasSuffix(trim, ";") {
			out = append(out, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		tail := strings.TrimSpace(buf.String())
		if tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			if len(t) > 80 {
				return t[:80] + "..."
			}
			return t
		}
	}
	return s
}
