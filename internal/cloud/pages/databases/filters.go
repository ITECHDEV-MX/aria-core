package databases

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FilterOp es el operador de un filter sobre una property.
type FilterOp string

const (
	OpEq         FilterOp = "eq"
	OpNeq        FilterOp = "neq"
	OpContains   FilterOp = "contains"
	OpNotContain FilterOp = "not_contains"
	OpGT         FilterOp = "gt"
	OpGTE        FilterOp = "gte"
	OpLT         FilterOp = "lt"
	OpLTE        FilterOp = "lte"
	OpEmpty      FilterOp = "empty"
	OpNotEmpty   FilterOp = "not_empty"
	OpInArray    FilterOp = "in_array"
)

// Filter es un filtro individual a aplicar sobre rows.props_json.
type Filter struct {
	Key   string   `json:"key"`
	Op    FilterOp `json:"op"`
	Value any      `json:"value,omitempty"`
}

// Sort es un criterio de orden.
type Sort struct {
	Key       string `json:"key"`
	Direction string `json:"direction"` // "asc" | "desc"
}

// ViewConfig es la configuración serializada de una view (config_json).
type ViewConfig struct {
	GroupByKey   string   `json:"group_by_key,omitempty"`
	VisibleProps []string `json:"visible_props,omitempty"`
	Filters      []Filter `json:"filters,omitempty"`
	Sorts        []Sort   `json:"sorts,omitempty"`
	CoverPropKey string   `json:"cover_prop_key,omitempty"`
	TitlePropKey string   `json:"title_prop_key,omitempty"`
}

// BuildWhereClause construye la cláusula WHERE para SELECT sobre
// aria_page_database_rows usando filters. Retorna SQL fragment + args.
//
// Convención: la columna props_json es JSONB. Usamos operadores postgres
// estándar (->>, jsonb_array_elements, @>).
//
// IMPORTANTE: el caller debe proveer ya el filtro database_id=$1 antes de
// concatenar este resultado. Args inicia en $argOffset+1.
func BuildWhereClause(filters []Filter, schema []PropDef, argOffset int) (string, []any, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}
	defByKey := indexSchema(schema)
	parts := []string{}
	args := []any{}
	for _, f := range filters {
		def, ok := defByKey[f.Key]
		if !ok {
			return "", nil, fmt.Errorf("filter key %q not in schema", f.Key)
		}
		frag, more, err := buildFragment(def, f, argOffset+len(args))
		if err != nil {
			return "", nil, err
		}
		if frag == "" {
			continue
		}
		parts = append(parts, frag)
		args = append(args, more...)
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", args, nil
}

func buildFragment(def PropDef, f Filter, argBase int) (string, []any, error) {
	col := jsonbAccessor(def, f.Key)
	switch f.Op {
	case OpEq:
		return fmt.Sprintf("%s = $%d", col, argBase+1), []any{toString(f.Value)}, nil
	case OpNeq:
		return fmt.Sprintf("%s <> $%d", col, argBase+1), []any{toString(f.Value)}, nil
	case OpContains:
		return fmt.Sprintf("%s ILIKE $%d", col, argBase+1), []any{"%" + toString(f.Value) + "%"}, nil
	case OpNotContain:
		return fmt.Sprintf("%s NOT ILIKE $%d", col, argBase+1), []any{"%" + toString(f.Value) + "%"}, nil
	case OpGT:
		return numericComparison(def, col, ">", f.Value, argBase)
	case OpGTE:
		return numericComparison(def, col, ">=", f.Value, argBase)
	case OpLT:
		return numericComparison(def, col, "<", f.Value, argBase)
	case OpLTE:
		return numericComparison(def, col, "<=", f.Value, argBase)
	case OpEmpty:
		return fmt.Sprintf("(props_json->>'%s' IS NULL OR props_json->>'%s' = '')", escIdent(f.Key), escIdent(f.Key)), nil, nil
	case OpNotEmpty:
		return fmt.Sprintf("(props_json->>'%s' IS NOT NULL AND props_json->>'%s' <> '')", escIdent(f.Key), escIdent(f.Key)), nil, nil
	case OpInArray:
		// para multi_select: comprueba que el array JSONB contenga el valor.
		// Usamos el operador @> con un JSONB construido.
		jsonArg, err := json.Marshal(map[string]any{f.Key: []any{toString(f.Value)}})
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("props_json @> $%d::jsonb", argBase+1), []any{string(jsonArg)}, nil
	}
	return "", nil, fmt.Errorf("unknown filter op %q", f.Op)
}

func numericComparison(def PropDef, col, op string, value any, argBase int) (string, []any, error) {
	// Para PropNumber → cast a numeric. Para PropDate → cast a timestamptz.
	switch def.Type {
	case PropNumber:
		return fmt.Sprintf("(%s)::numeric %s $%d::numeric", col, op, argBase+1), []any{toString(value)}, nil
	case PropDate:
		return fmt.Sprintf("(%s)::timestamptz %s $%d::timestamptz", col, op, argBase+1), []any{toString(value)}, nil
	default:
		return fmt.Sprintf("%s %s $%d", col, op, argBase+1), []any{toString(value)}, nil
	}
}

// jsonbAccessor retorna la expresión SQL para extraer el value como text de
// props_json. Para multi_select los filtros usan operador @> arriba.
func jsonbAccessor(_ PropDef, key string) string {
	return fmt.Sprintf("(props_json->>'%s')", escIdent(key))
}

func escIdent(s string) string {
	// Schema validator garantiza que keys son [A-Za-z0-9_], así que sólo
	// duplicamos comilla simple por seguridad.
	return strings.ReplaceAll(s, "'", "''")
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%v", t)
	case int, int32, int64:
		return fmt.Sprintf("%v", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
