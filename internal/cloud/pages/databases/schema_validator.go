// Package databases implementa inline databases (Notion-style) para aria_pages
// con page_type='database'. Soporta property types tipadas, multiple views
// (table/kanban/gallery/list/calendar), filters, sort, y validación schema.
package databases

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PropType es el tipo de una property column de una inline database.
type PropType string

const (
	PropText        PropType = "text"
	PropNumber      PropType = "number"
	PropDate        PropType = "date"
	PropSelect      PropType = "select"
	PropMultiSelect PropType = "multi_select"
	PropCheckbox    PropType = "checkbox"
	PropURL         PropType = "url"
	PropEmail       PropType = "email"
	PropRelation    PropType = "relation"
	PropPerson      PropType = "person"
	PropRichText    PropType = "rich_text"
)

// validPropTypes contiene los tipos aceptados al crear/migrar schemas.
var validPropTypes = map[PropType]struct{}{
	PropText: {}, PropNumber: {}, PropDate: {}, PropSelect: {},
	PropMultiSelect: {}, PropCheckbox: {}, PropURL: {}, PropEmail: {},
	PropRelation: {}, PropPerson: {}, PropRichText: {},
}

// PropDef es la definición de una columna del schema. Persiste en
// aria_page_databases.schema_json como elemento de un array.
type PropDef struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Type     PropType `json:"type"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
	RelTo    string   `json:"rel_to,omitempty"`
}

// Errores públicos del package.
var (
	ErrInvalidSchema = errors.New("databases: invalid schema")
	ErrInvalidRow    = errors.New("databases: invalid row")
	ErrNotFound      = errors.New("databases: not found")
	ErrConflict      = errors.New("databases: conflict")
)

// ValidationError es un error específico de validación que incluye el campo y
// motivo, para retornar al cliente un mensaje útil.
type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("databases: invalid %s: %s", e.Field, e.Reason)
}

// ValidateSchema verifica que el schema sea bien-formado:
//   - keys únicas, no-empty, slug-friendly
//   - tipos válidos
//   - select/multi_select tienen options no-vacío
//   - relation tiene rel_to (UUID válido)
func ValidateSchema(schema []PropDef) []ValidationError {
	errs := []ValidationError{}
	seen := make(map[string]struct{}, len(schema))
	for i, def := range schema {
		field := fmt.Sprintf("schema[%d]", i)
		key := strings.TrimSpace(def.Key)
		if key == "" {
			errs = append(errs, ValidationError{Field: field + ".key", Reason: "key is required"})
			continue
		}
		if !isValidKey(key) {
			errs = append(errs, ValidationError{Field: field + ".key", Reason: "key must be alphanumeric/underscore"})
		}
		if _, dup := seen[key]; dup {
			errs = append(errs, ValidationError{Field: field + ".key", Reason: "duplicate key " + key})
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(def.Name) == "" {
			errs = append(errs, ValidationError{Field: field + ".name", Reason: "name is required"})
		}
		if _, ok := validPropTypes[def.Type]; !ok {
			errs = append(errs, ValidationError{Field: field + ".type", Reason: "unknown type " + string(def.Type)})
			continue
		}
		switch def.Type {
		case PropSelect, PropMultiSelect:
			if len(def.Options) == 0 {
				errs = append(errs, ValidationError{Field: field + ".options", Reason: "select/multi_select require options"})
			}
		case PropRelation:
			if strings.TrimSpace(def.RelTo) == "" {
				errs = append(errs, ValidationError{Field: field + ".rel_to", Reason: "relation requires rel_to (target database id)"})
			} else if _, err := uuid.Parse(def.RelTo); err != nil {
				errs = append(errs, ValidationError{Field: field + ".rel_to", Reason: "rel_to must be a valid UUID"})
			}
		}
	}
	return errs
}

// ValidateRow valida props (mapa key → value) contra un schema. Retorna lista
// de errores; vacía si todo OK. Las props que no estén en schema son rechazadas.
func ValidateRow(schema []PropDef, props map[string]any) []ValidationError {
	errs := []ValidationError{}
	defByKey := indexSchema(schema)

	// Verificar required + tipo correcto
	for _, def := range schema {
		raw, present := props[def.Key]
		if def.Required && (!present || isEmpty(raw)) {
			errs = append(errs, ValidationError{Field: def.Key, Reason: "required"})
			continue
		}
		if !present || raw == nil {
			continue
		}
		if err := validateValue(def, raw); err != nil {
			errs = append(errs, ValidationError{Field: def.Key, Reason: err.Error()})
		}
	}

	// Verificar que no haya props extra fuera del schema
	for k := range props {
		if _, ok := defByKey[k]; !ok {
			errs = append(errs, ValidationError{Field: k, Reason: "unknown property (not in schema)"})
		}
	}

	return errs
}

func indexSchema(schema []PropDef) map[string]PropDef {
	out := make(map[string]PropDef, len(schema))
	for _, def := range schema {
		out[def.Key] = def
	}
	return out
}

func validateValue(def PropDef, raw any) error {
	switch def.Type {
	case PropText, PropRichText:
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case PropNumber:
		switch raw.(type) {
		case float64, float32, int, int32, int64:
			// ok
		case json.Number:
			// ok
		default:
			return fmt.Errorf("expected number")
		}
	case PropCheckbox:
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("expected bool")
		}
	case PropDate:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected RFC3339 date string")
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			// permitir YYYY-MM-DD también
			if _, err2 := time.Parse("2006-01-02", s); err2 != nil {
				return fmt.Errorf("expected RFC3339 or YYYY-MM-DD date")
			}
		}
	case PropSelect:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected string")
		}
		if !containsString(def.Options, s) {
			return fmt.Errorf("value %q not in options", s)
		}
	case PropMultiSelect:
		arr, ok := raw.([]any)
		if !ok {
			// Permitir también []string
			if strs, ok2 := raw.([]string); ok2 {
				for _, s := range strs {
					if !containsString(def.Options, s) {
						return fmt.Errorf("value %q not in options", s)
					}
				}
				return nil
			}
			return fmt.Errorf("expected array")
		}
		for _, v := range arr {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("array values must be strings")
			}
			if !containsString(def.Options, s) {
				return fmt.Errorf("value %q not in options", s)
			}
		}
	case PropURL:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected URL string")
		}
		if s != "" {
			if _, err := url.ParseRequestURI(s); err != nil {
				return fmt.Errorf("invalid URL")
			}
		}
	case PropEmail:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected email string")
		}
		if s != "" {
			if _, err := mail.ParseAddress(s); err != nil {
				return fmt.Errorf("invalid email")
			}
		}
	case PropRelation, PropPerson:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected UUID string")
		}
		if s != "" {
			if _, err := uuid.Parse(s); err != nil {
				return fmt.Errorf("invalid UUID")
			}
		}
	}
	return nil
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	}
	return false
}

func isValidKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
