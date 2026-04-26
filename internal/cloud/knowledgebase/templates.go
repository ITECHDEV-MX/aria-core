package knowledgebase

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// PRDFrontmatter es la metadata YAML inyectada en cada PRD/historia que
// se commitea al repo central.
type PRDFrontmatter struct {
	ID           string
	Title        string
	Project      string
	Type         string // "prd" | "historia"
	Status       string
	CreatedBy    string
	CreatedAt    time.Time
	LastUpdated  time.Time
	DashboardURL string
}

// RenderPageMarkdown produce el cuerpo final del PRD/historia listo para
// commit: frontmatter YAML + content markdown del page.
func RenderPageMarkdown(fm PRDFrontmatter, contentMD string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", yamlEscape(fm.ID)))
	b.WriteString(fmt.Sprintf("title: %s\n", yamlEscape(fm.Title)))
	b.WriteString(fmt.Sprintf("project: %s\n", yamlEscape(fm.Project)))
	b.WriteString(fmt.Sprintf("type: %s\n", yamlEscape(fm.Type)))
	if strings.TrimSpace(fm.Status) != "" {
		b.WriteString(fmt.Sprintf("status: %s\n", yamlEscape(fm.Status)))
	}
	if strings.TrimSpace(fm.CreatedBy) != "" {
		b.WriteString(fmt.Sprintf("created_by: %s\n", yamlEscape(fm.CreatedBy)))
	}
	if !fm.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("created_at: %s\n", fm.CreatedAt.UTC().Format(time.RFC3339)))
	}
	if !fm.LastUpdated.IsZero() {
		b.WriteString(fmt.Sprintf("last_updated_at: %s\n", fm.LastUpdated.UTC().Format(time.RFC3339)))
	}
	if strings.TrimSpace(fm.DashboardURL) != "" {
		b.WriteString(fmt.Sprintf("dashboard_url: %s\n", yamlEscape(fm.DashboardURL)))
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(contentMD, "\n"))
	b.WriteString("\n")
	return b.String()
}

// QuoteRenderInput captura los inputs para renderear una cotización a markdown
// (source) que luego pandoc convierte a DOCX.
type QuoteRenderInput struct {
	Folio                   string
	ProductName             string
	ProductSubtitle         string
	ProposalType            string
	PreparedForCompany      string
	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string
	PreparedByName          string
	PreparedByEmail         string
	PreparedByRole          string
	IssueDate               *time.Time
	ValidUntil              *time.Time
	Currency                string
	Subtotal                float64
	Taxes                   float64
	Total                   float64
	Status                  string
	Justification           string
	Terms                   string
	Items                   []QuoteItemView
	Sections                []QuoteSectionView
	Tags                    []string
	DashboardURL            string
}

// QuoteItemView es una línea de la tabla de items.
type QuoteItemView struct {
	SKU         string
	Description string
	Qty         float64
	UnitPrice   float64
	Subtotal    float64
}

// QuoteSectionView es una sección narrativa de la propuesta.
type QuoteSectionView struct {
	Key       string
	Title     string
	ContentMD string
	SortOrder int
}

// RenderQuoteMarkdown produce el .md fuente listo para pandoc → DOCX.
// El orden + estilo emula las propuestas históricas (PARK, Mercedes Serrala,
// Mercedes RPA): header con cliente + folio, tabla de items, totales,
// secciones narrativas (alcance, justificación, términos).
func RenderQuoteMarkdown(in QuoteRenderInput) string {
	var b strings.Builder

	productTitle := strings.TrimSpace(in.ProductName)
	if productTitle == "" {
		productTitle = "Propuesta comercial"
	}
	b.WriteString("# " + productTitle + "\n\n")
	if strings.TrimSpace(in.ProductSubtitle) != "" {
		b.WriteString("*" + in.ProductSubtitle + "*\n\n")
	}

	// Header iTechDev / cliente.
	b.WriteString("## Información de la propuesta\n\n")
	if strings.TrimSpace(in.Folio) != "" {
		b.WriteString(fmt.Sprintf("- **Folio:** %s\n", in.Folio))
	}
	if strings.TrimSpace(in.ProposalType) != "" {
		b.WriteString(fmt.Sprintf("- **Tipo:** %s\n", in.ProposalType))
	}
	if in.IssueDate != nil {
		b.WriteString(fmt.Sprintf("- **Fecha de emisión:** %s\n", in.IssueDate.Format("2006-01-02")))
	}
	if in.ValidUntil != nil {
		b.WriteString(fmt.Sprintf("- **Válida hasta:** %s\n", in.ValidUntil.Format("2006-01-02")))
	}
	if strings.TrimSpace(in.Status) != "" {
		b.WriteString(fmt.Sprintf("- **Estado:** %s\n", in.Status))
	}
	b.WriteString("\n")

	// Bloque "Preparado para".
	if anyNonEmpty(in.PreparedForCompany, in.PreparedForArea, in.PreparedForContactName, in.PreparedForContactEmail) {
		b.WriteString("## Preparado para\n\n")
		if v := strings.TrimSpace(in.PreparedForCompany); v != "" {
			b.WriteString(fmt.Sprintf("- **Cliente:** %s\n", v))
		}
		if v := strings.TrimSpace(in.PreparedForArea); v != "" {
			b.WriteString(fmt.Sprintf("- **Área:** %s\n", v))
		}
		if v := strings.TrimSpace(in.PreparedForContactName); v != "" {
			b.WriteString(fmt.Sprintf("- **Contacto:** %s\n", v))
		}
		if v := strings.TrimSpace(in.PreparedForContactEmail); v != "" {
			b.WriteString(fmt.Sprintf("- **Email:** %s\n", v))
		}
		b.WriteString("\n")
	}

	// Bloque "Preparado por".
	if anyNonEmpty(in.PreparedByName, in.PreparedByEmail, in.PreparedByRole) {
		b.WriteString("## Preparado por\n\n")
		if v := strings.TrimSpace(in.PreparedByName); v != "" {
			b.WriteString(fmt.Sprintf("- **Nombre:** %s\n", v))
		}
		if v := strings.TrimSpace(in.PreparedByRole); v != "" {
			b.WriteString(fmt.Sprintf("- **Rol:** %s\n", v))
		}
		if v := strings.TrimSpace(in.PreparedByEmail); v != "" {
			b.WriteString(fmt.Sprintf("- **Email:** %s\n", v))
		}
		b.WriteString("\n")
	}

	// Items + totales.
	if len(in.Items) > 0 {
		b.WriteString("## Conceptos\n\n")
		b.WriteString("| SKU | Descripción | Cant. | P. Unitario | Subtotal |\n")
		b.WriteString("|-----|-------------|------:|------------:|---------:|\n")
		for _, it := range in.Items {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
				escapeTableCell(it.SKU),
				escapeTableCell(it.Description),
				formatNumber(it.Qty),
				formatMoney(it.UnitPrice, in.Currency),
				formatMoney(it.Subtotal, in.Currency),
			))
		}
		b.WriteString("\n")
		b.WriteString("### Totales\n\n")
		b.WriteString(fmt.Sprintf("- **Subtotal:** %s\n", formatMoney(in.Subtotal, in.Currency)))
		if in.Taxes > 0 {
			b.WriteString(fmt.Sprintf("- **Impuestos:** %s\n", formatMoney(in.Taxes, in.Currency)))
		}
		b.WriteString(fmt.Sprintf("- **Total:** %s\n\n", formatMoney(in.Total, in.Currency)))
	}

	// Secciones narrativas (orden por SortOrder).
	if len(in.Sections) > 0 {
		sections := append([]QuoteSectionView(nil), in.Sections...)
		sort.SliceStable(sections, func(i, j int) bool {
			if sections[i].SortOrder != sections[j].SortOrder {
				return sections[i].SortOrder < sections[j].SortOrder
			}
			return sections[i].Key < sections[j].Key
		})
		for _, sec := range sections {
			title := strings.TrimSpace(sec.Title)
			if title == "" {
				title = strings.Title(strings.ReplaceAll(sec.Key, "_", " "))
			}
			b.WriteString("## " + title + "\n\n")
			body := strings.TrimSpace(sec.ContentMD)
			if body != "" {
				b.WriteString(body)
				b.WriteString("\n\n")
			}
		}
	}

	// Justificación + términos al final si están.
	if v := strings.TrimSpace(in.Justification); v != "" {
		b.WriteString("## Justificación\n\n" + v + "\n\n")
	}
	if v := strings.TrimSpace(in.Terms); v != "" {
		b.WriteString("## Términos y condiciones\n\n" + v + "\n\n")
	}

	if len(in.Tags) > 0 {
		b.WriteString("## Tags\n\n")
		for _, t := range in.Tags {
			b.WriteString("- " + t + "\n")
		}
		b.WriteString("\n")
	}

	if v := strings.TrimSpace(in.DashboardURL); v != "" {
		b.WriteString("---\n\n")
		b.WriteString("*Vista en dashboard ARIA Core: <" + v + ">*\n")
	}

	return b.String()
}

// ProjectInfo captura los datos mínimos de un proyecto del equipo (wave 7)
// para construir su README.md en el repo central + el README del repo
// individual (vía StandardProjectReadme).
type ProjectInfo struct {
	ID           string
	Slug         string
	Name         string
	Description  string
	Status       string
	StartedAt    *time.Time
	DeliveredAt  *time.Time
	DashboardURL string
	CodeRepoURL  string
	Members      []ProjectMember
}

// ProjectMember es la asignación de un developer a un proyecto.
type ProjectMember struct {
	UID  string
	Name string
	Role string
}

// RenderProjectReadme produce el README.md del proyecto que vive en
// proyectos/<slug>/README.md del repo central.
func RenderProjectReadme(p ProjectInfo) string {
	var b strings.Builder
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = p.Slug
	}
	b.WriteString("# " + name + "\n\n")
	if strings.TrimSpace(p.Description) != "" {
		b.WriteString(p.Description + "\n\n")
	}
	b.WriteString("## Información del proyecto\n\n")
	if strings.TrimSpace(p.Status) != "" {
		b.WriteString(fmt.Sprintf("- **Estado:** %s\n", p.Status))
	}
	if p.StartedAt != nil {
		b.WriteString(fmt.Sprintf("- **Inicio:** %s\n", p.StartedAt.Format("2006-01-02")))
	}
	if p.DeliveredAt != nil {
		b.WriteString(fmt.Sprintf("- **Entregado:** %s\n", p.DeliveredAt.Format("2006-01-02")))
	}
	if strings.TrimSpace(p.DashboardURL) != "" {
		b.WriteString(fmt.Sprintf("- **Dashboard:** <%s>\n", p.DashboardURL))
	}
	if strings.TrimSpace(p.CodeRepoURL) != "" {
		b.WriteString(fmt.Sprintf("- **Repositorio de código:** <%s>\n", p.CodeRepoURL))
	}
	b.WriteString("\n")

	if len(p.Members) > 0 {
		b.WriteString("## Equipo\n\n")
		b.WriteString("| Nombre | Rol |\n|--------|-----|\n")
		for _, m := range p.Members {
			name := strings.TrimSpace(m.Name)
			if name == "" {
				name = m.UID
			}
			b.WriteString(fmt.Sprintf("| %s | %s |\n", escapeTableCell(name), escapeTableCell(m.Role)))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Estructura\n\n")
	b.WriteString("- `prds/` — Documentos PRD del proyecto.\n")
	b.WriteString("- `historias/` — Historias de usuario.\n")
	b.WriteString("- `cotizaciones/` — Propuestas comerciales (markdown + DOCX iTechDev + metadata).\n\n")

	b.WriteString("---\n\n*README mantenido automáticamente por ARIA Core (knowledge-base sync).*\n")
	return b.String()
}

// StandardProjectReadme expone una versión consumible por wave 7 para el
// repo individual de cada proyecto (no el central). Mantiene el mismo
// formato pero agrega un encabezado pensado para landing público en GitHub.
func StandardProjectReadme(p ProjectInfo) string {
	var b strings.Builder
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = p.Slug
	}
	b.WriteString("# " + name + "\n\n")
	if strings.TrimSpace(p.Description) != "" {
		b.WriteString(p.Description + "\n\n")
	}
	b.WriteString("> Este repositorio contiene el código y entregables del proyecto.\n")
	b.WriteString("> Toda la documentación viva (PRDs, historias, cotizaciones) se mantiene en\n")
	b.WriteString("> [team-knowledge-base](https://github.com/ITECHDEV-MX/team-knowledge-base) bajo `proyectos/" + p.Slug + "/`.\n\n")

	if strings.TrimSpace(p.DashboardURL) != "" {
		b.WriteString("- **Dashboard ARIA Core:** <" + p.DashboardURL + ">\n")
	}
	if strings.TrimSpace(p.Status) != "" {
		b.WriteString(fmt.Sprintf("- **Estado:** %s\n", p.Status))
	}
	if p.StartedAt != nil {
		b.WriteString(fmt.Sprintf("- **Inicio:** %s\n", p.StartedAt.Format("2006-01-02")))
	}
	if p.DeliveredAt != nil {
		b.WriteString(fmt.Sprintf("- **Entregado:** %s\n", p.DeliveredAt.Format("2006-01-02")))
	}
	b.WriteString("\n")
	if len(p.Members) > 0 {
		b.WriteString("## Equipo\n\n")
		for _, m := range p.Members {
			line := strings.TrimSpace(m.Name)
			if line == "" {
				line = m.UID
			}
			if strings.TrimSpace(m.Role) != "" {
				line += " — " + m.Role
			}
			b.WriteString("- " + line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n*README generado por ARIA Core wave 8 — knowledge-base sync.*\n")
	return b.String()
}

// IndexEntry es una entrada del root README.md del repo central.
type IndexEntry struct {
	Slug         string
	Name         string
	Description  string
	Status       string
	DashboardURL string
	UpdatedAt    time.Time
}

// RenderRootIndex produce el README.md raíz del repo central, que actúa
// como índice navegable de todos los proyectos.
func RenderRootIndex(entries []IndexEntry) string {
	var b strings.Builder
	b.WriteString("# team-knowledge-base\n\n")
	b.WriteString("Repositorio central de knowledge del equipo iTechDev.\n")
	b.WriteString("Mantenido **automáticamente** por [ARIA Core](https://ariacore.itechdev.com.mx) (wave 8 — knowledge-base sync).\n\n")
	b.WriteString("Cada proyecto tiene su propia carpeta bajo `proyectos/` con PRDs, historias y cotizaciones.\n\n")

	if len(entries) == 0 {
		b.WriteString("> Aún no hay proyectos sincronizados.\n")
		return b.String()
	}

	sorted := append([]IndexEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})

	b.WriteString("## Proyectos\n\n")
	b.WriteString("| Proyecto | Estado | Última actualización | Dashboard |\n")
	b.WriteString("|----------|--------|----------------------|-----------|\n")
	for _, e := range sorted {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			name = e.Slug
		}
		linkName := fmt.Sprintf("[%s](proyectos/%s/)", escapeTableCell(name), e.Slug)
		updated := ""
		if !e.UpdatedAt.IsZero() {
			updated = e.UpdatedAt.UTC().Format("2006-01-02")
		}
		dash := "—"
		if v := strings.TrimSpace(e.DashboardURL); v != "" {
			dash = fmt.Sprintf("[Abrir](%s)", v)
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			linkName,
			escapeTableCell(e.Status),
			updated,
			dash,
		))
	}
	b.WriteString("\n")

	b.WriteString("## Estructura\n\n")
	b.WriteString("```\n")
	b.WriteString("team-knowledge-base/\n")
	b.WriteString("├── README.md            ← este índice (auto)\n")
	b.WriteString("├── proyectos/<slug>/\n")
	b.WriteString("│   ├── README.md        ← overview del proyecto + miembros\n")
	b.WriteString("│   ├── prds/<NNNN>-<slug>.md\n")
	b.WriteString("│   ├── historias/<NNNN>-<slug>.md\n")
	b.WriteString("│   └── cotizaciones/<FOLIO>/\n")
	b.WriteString("│       ├── propuesta.md\n")
	b.WriteString("│       ├── propuesta.docx     ← formato iTechDev\n")
	b.WriteString("│       └── metadata.json\n")
	b.WriteString("└── plantillas/\n")
	b.WriteString("    ├── prd-template.md\n")
	b.WriteString("    ├── historia-template.md\n")
	b.WriteString("    └── cotizacion-template.md\n")
	b.WriteString("```\n")
	return b.String()
}

// QuoteMetadata es lo que se serializa a metadata.json al lado del .docx
// para que el repo central pueda inspeccionar la cotización sin abrir el DOCX.
type QuoteMetadata struct {
	QuoteID         string    `json:"quote_id"`
	Folio           string    `json:"folio,omitempty"`
	Status          string    `json:"status"`
	Currency        string    `json:"currency"`
	Subtotal        float64   `json:"subtotal"`
	Taxes           float64   `json:"taxes"`
	Total           float64   `json:"total"`
	IssueDate       string    `json:"issue_date,omitempty"`
	ValidUntil      string    `json:"valid_until,omitempty"`
	PreparedFor     string    `json:"prepared_for,omitempty"`
	PreparedForArea string    `json:"prepared_for_area,omitempty"`
	PreparedBy      string    `json:"prepared_by,omitempty"`
	ProductName     string    `json:"product_name,omitempty"`
	ProposalType    string    `json:"proposal_type,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	DashboardURL    string    `json:"dashboard_url,omitempty"`
	GeneratedAt     time.Time `json:"generated_at"`
}

// PRDTemplateMarkdown es el body del template inicial commiteado a
// plantillas/prd-template.md del repo central.
const PRDTemplateMarkdown = `# PRD: [Producto/Feature]

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
`

// HistoriaTemplateMarkdown es el body del template inicial commiteado a
// plantillas/historia-template.md del repo central.
const HistoriaTemplateMarkdown = `# Historia: [Título corto]

**Como** [tipo de usuario]
**Quiero** [acción / capacidad]
**Para** [valor / beneficio]

## Contexto
[Por qué esta historia importa, dónde encaja en el roadmap]

## Criterios de aceptación
- [ ]
- [ ]
- [ ]

## Estimación
- **Esfuerzo:** S | M | L | XL
- **Dependencias:**
- **Riesgos:**

## Notas técnicas
[Implementación sugerida, archivos involucrados, decisiones de diseño]

## Status
draft | ready | in_progress | done
`

// CotizacionTemplateMarkdown es el body del template inicial commiteado a
// plantillas/cotizacion-template.md del repo central.
const CotizacionTemplateMarkdown = `# Propuesta: [Producto / Servicio]

## Información de la propuesta
- **Folio:** ITD-YYYY-XXX-NNN
- **Tipo:** servicio | producto | mixto
- **Fecha de emisión:** YYYY-MM-DD
- **Válida hasta:** YYYY-MM-DD

## Preparado para
- **Cliente:**
- **Área:**
- **Contacto:**
- **Email:**

## Preparado por
- **Nombre:**
- **Rol:**
- **Email:**

## Conceptos
| SKU | Descripción | Cant. | P. Unitario | Subtotal |
|-----|-------------|------:|------------:|---------:|
|     |             |       |             |          |

### Totales
- **Subtotal:**
- **Total:**

## Alcance
[Qué incluye y qué NO incluye este alcance]

## Términos y condiciones
[Pagos, plazos, propiedad intelectual, soporte]
`

// === Helpers de formato internos ===

func anyNonEmpty(values ...string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func formatMoney(v float64, currency string) string {
	cur := strings.TrimSpace(currency)
	if cur == "" {
		cur = "MXN"
	}
	return fmt.Sprintf("$%s %s", formatNumber(v), cur)
}

func formatNumber(v float64) string {
	// Dos decimales con separador de miles (estilo es-MX simple).
	abs := v
	if abs < 0 {
		abs = -abs
	}
	intPart := int64(abs)
	frac := abs - float64(intPart)
	intStr := groupThousands(intPart)
	if v < 0 {
		intStr = "-" + intStr
	}
	return fmt.Sprintf("%s.%02d", intStr, int(frac*100+0.5))
}

func groupThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteString(",")
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteString(",")
		}
	}
	return b.String()
}

func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// yamlEscape envuelve en comillas si el valor contiene caracteres que
// pueden romper el frontmatter YAML.
func yamlEscape(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, ":#\n\"'") {
		// quote y escape de comillas dobles internas.
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}
