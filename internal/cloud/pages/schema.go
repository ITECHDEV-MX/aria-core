// Package pages: schema embed + Migrate function.
package pages

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed schema.sql
var schemaSQL string

// Migrate aplica el schema pages. Es idempotente (todas las DDL usan IF NOT EXISTS).
// Llamado desde cloudstore.migrate() después del bloque PAGES MIGRATIONS.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("pages: nil db")
	}
	stmts := splitSQLStatements(schemaSQL)
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("pages: migrate stmt %q: %w", firstLine(stmt), err)
		}
	}
	return nil
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
