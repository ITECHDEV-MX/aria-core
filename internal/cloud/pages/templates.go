package pages

import (
	"context"
	"fmt"
	"strings"
)

// TemplateDef describe un template builtin: clave, nombre, body en markdown.
// page_type='template' las distingue de docs reales y permiten clonarlas vía
// CreateFromTemplate (Create con template_key + ContentMD = body).
type TemplateDef struct {
	Key         string
	Name        string
	Description string
	Icon        string
	BodyMD      string
}

// BuiltinTemplates retorna los 5 templates builtin de la fundación mini-Notion.
// Estables — sus keys forman parte del contrato público (CLI, MCP, dashboard).
func BuiltinTemplates() []TemplateDef {
	return []TemplateDef{
		{
			Key:         "prd-v1",
			Name:        "PRD (Product Requirements Document)",
			Description: "Documento de requisitos de producto con problema, métricas, RF/RNF y decisión.",
			Icon:        "📋",
			BodyMD: strings.TrimSpace(`
# PRD: [Producto/Feature]

## Problema
[Descripción del problema que resolvemos]

## Usuarios afectados
-

## Métricas de éxito
-

## Requisitos funcionales
- [ ]

## Requisitos no funcionales
-

## Out of scope
-

## Riesgos
-

## Decisión final
**Status**: draft | approved | shipped
**Approved by**:
**Date**:
`),
		},
		{
			Key:         "incident-v1",
			Name:        "Incident Report",
			Description: "Reporte de incidente con timestamp, severity, impact, root cause, remediation y lessons.",
			Icon:        "🚨",
			BodyMD: strings.TrimSpace(`
# Incident Report: [Título corto]

## Resumen ejecutivo
[1-2 líneas describiendo qué pasó y el impacto]

## Detalles
- **Timestamp inicio (UTC)**:
- **Timestamp fin (UTC)**:
- **Severity**: SEV1 | SEV2 | SEV3 | SEV4
- **Servicios afectados**:
- **Usuarios afectados (estimado)**:

## Impacto
[Descripción del impacto cualitativo y cuantitativo]

## Timeline
- **HH:MM** — Detección inicial
- **HH:MM** — Mitigación aplicada
- **HH:MM** — Resolución confirmada

## Root cause
[Análisis técnico del root cause]

## Remediation aplicada
[Qué hicimos para mitigar]

## Lessons learned
-

## Action items
- [ ]
`),
		},
		{
			Key:         "one-on-one-v1",
			Name:        "1-on-1 Notes",
			Description: "Notas de reunión 1:1 con wins, blockers, asks, career y notas libres.",
			Icon:        "👥",
			BodyMD: strings.TrimSpace(`
# 1-on-1: [Nombre] — [Fecha]

## Wins (semana pasada)
-

## Blockers / Friction
-

## Asks (cosas que necesita de mí o del equipo)
-

## Career / Growth
[Conversaciones sobre crecimiento, skills, próximos pasos]

## Notes
[Cualquier otra cosa relevante]

## Action items
- [ ]
`),
		},
		{
			Key:         "adr-v1",
			Name:        "ADR (Architecture Decision Record)",
			Description: "Registro de decisión arquitectónica con context, decision, consequences y alternatives.",
			Icon:        "🏛️",
			BodyMD: strings.TrimSpace(`
# ADR-NNN: [Título corto de la decisión]

**Status**: proposed | accepted | superseded | deprecated
**Date**: YYYY-MM-DD
**Deciders**: [nombres]

## Context
[Qué problema técnico estamos enfrentando, fuerzas en juego, restricciones]

## Decision
[La decisión tomada en una frase clara]

## Consequences

### Positivas
-

### Negativas / Trade-offs
-

## Alternatives considered

### Opción A — [nombre]
**Rejected because**:

### Opción B — [nombre]
**Rejected because**:

## References
-
`),
		},
		{
			Key:         "client-onboarding-v1",
			Name:        "Cliente Onboarding",
			Description: "Onboarding de cliente con contactos, stack técnico, historial y preferencias de comms.",
			Icon:        "🤝",
			BodyMD: strings.TrimSpace(`
# Cliente: [Nombre legal]

## Contactos
| Nombre | Rol | Email | Teléfono | Notas |
|--------|-----|-------|----------|-------|
|        |     |       |          |       |

## Stack técnico
- **Backend**:
- **Frontend**:
- **DB**:
- **Hosting / Cloud**:
- **CI/CD**:
- **Otros**:

## Historial / Contexto
[Cómo llegó el cliente, qué problemas enfrenta, qué le hemos entregado antes]

## Preferencias de comunicación
- **Canal preferido**: email | slack | whatsapp | call
- **Frecuencia**: diaria | semanal | quincenal | mensual
- **Día/hora preferida**:
- **Idioma**: es | en
- **Sensibilidades culturales / formato preferido**:

## Acceso & credenciales
[Referencias al vault de aria-core, links a docs internos. NUNCA pegar secrets aquí]

## Roadmap / Próximos pasos
- [ ]
`),
		},
	}
}

// SeedTemplates crea/actualiza los templates builtin como páginas con
// page_type='template'. Idempotente: ON CONFLICT DO NOTHING por (template_key, page_type).
//
// El UID 'system' (UUID nil) es válido como created_by_uid solo en este contexto
// — se usa una constante porque las templates son fixtures.
func (s *PgStore) SeedTemplates(ctx context.Context, byUID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("pages: store not initialized")
	}
	if !isUUID(byUID) {
		return fmt.Errorf("%w: byUID must be valid uuid for SeedTemplates", ErrInvalidInput)
	}
	for _, t := range BuiltinTemplates() {
		const q = `
			INSERT INTO aria_pages (
				id, parent_id, title, content_md, icon, project, scope, client_id,
				page_type, template_key, sensitivity, sort_order,
				created_by_uid, updated_by_uid
			) VALUES (
				gen_random_uuid(), NULL, $1, $2, $3, NULL, 'team', NULL,
				'template', $4, 'internal', 0,
				$5::uuid, $5::uuid
			)
			ON CONFLICT DO NOTHING`
		// No hay UNIQUE en template_key, así que para idempotencia hacemos check antes.
		var existingID string
		if err := s.db.QueryRowContext(ctx,
			`SELECT id::text FROM aria_pages WHERE page_type='template' AND template_key = $1 LIMIT 1`,
			t.Key,
		).Scan(&existingID); err == nil {
			// Ya existe — refrescar body por si actualizamos el template fixture.
			if _, err := s.db.ExecContext(ctx,
				`UPDATE aria_pages SET title = $2, content_md = $3, icon = $4, updated_at = NOW() WHERE id = $1::uuid`,
				existingID, t.Name, t.BodyMD, t.Icon,
			); err != nil {
				return fmt.Errorf("pages: refresh template %s: %w", t.Key, err)
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, t.Name, t.BodyMD, t.Icon, t.Key, byUID); err != nil {
			return fmt.Errorf("pages: seed template %s: %w", t.Key, err)
		}
	}
	return nil
}

// GetBuiltinTemplate retorna el TemplateDef builtin por key, o nil si no existe.
func GetBuiltinTemplate(key string) *TemplateDef {
	for _, t := range BuiltinTemplates() {
		if t.Key == key {
			tt := t
			return &tt
		}
	}
	return nil
}
