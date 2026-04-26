package databases

import (
	"fmt"
	"strings"
)

// BuildOrderBy construye la cláusula ORDER BY para SELECT sobre
// aria_page_database_rows. Sorts no especificados → orden por sort_order, created_at.
func BuildOrderBy(sorts []Sort, schema []PropDef) (string, error) {
	defByKey := indexSchema(schema)
	if len(sorts) == 0 {
		return "ORDER BY sort_order ASC, created_at ASC", nil
	}
	parts := []string{}
	for _, s := range sorts {
		def, ok := defByKey[s.Key]
		if !ok {
			return "", fmt.Errorf("sort key %q not in schema", s.Key)
		}
		dir := strings.ToUpper(strings.TrimSpace(s.Direction))
		if dir != "ASC" && dir != "DESC" {
			dir = "ASC"
		}
		switch def.Type {
		case PropNumber:
			parts = append(parts, fmt.Sprintf("(props_json->>'%s')::numeric %s NULLS LAST", escIdent(s.Key), dir))
		case PropDate:
			parts = append(parts, fmt.Sprintf("(props_json->>'%s')::timestamptz %s NULLS LAST", escIdent(s.Key), dir))
		case PropCheckbox:
			parts = append(parts, fmt.Sprintf("(props_json->>'%s')::boolean %s NULLS LAST", escIdent(s.Key), dir))
		default:
			parts = append(parts, fmt.Sprintf("(props_json->>'%s') %s NULLS LAST", escIdent(s.Key), dir))
		}
	}
	parts = append(parts, "sort_order ASC", "created_at ASC")
	return "ORDER BY " + strings.Join(parts, ", "), nil
}
