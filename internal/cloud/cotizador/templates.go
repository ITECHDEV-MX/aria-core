package cotizador

import (
	"context"
	"fmt"
	"strings"
)

// Template define la estructura de una plantilla de propuesta iTechDev.
// Cuando se aplica a una quote, las secciones se upsertan (key + sort_order).
type Template struct {
	Key             string // 'itechdev_implementation_v1', etc
	Name            string // display name
	Description     string
	ProposalType    string // matches CotizadorQuoteView.ProposalType
	DefaultProduct  string
	DefaultSubtitle string
	DefaultTags     []string
	Sections        []TemplateSection
}

type TemplateSection struct {
	Key       string
	Title     string
	ContentMD string
	SortOrder int
}

// AvailableTemplates retorna todas las plantillas registradas.
func AvailableTemplates() []Template {
	return []Template{
		templateImplementationiTechDev(),
		templateCommercialiTechDev(),
		templateServiceiTechDev(),
	}
}

// GetTemplate busca por key.
func GetTemplate(key string) (*Template, bool) {
	for _, t := range AvailableTemplates() {
		if t.Key == key {
			return &t, true
		}
	}
	return nil, false
}

// ApplyTemplate aplica la plantilla a la quote: upsert de secciones + actualiza
// proposal_type/product_name/subtitle/tags si vienen en la plantilla y la quote
// no los tiene seteados (no sobreescribe valores ya editados).
func (s *Store) ApplyTemplate(ctx context.Context, quoteID, templateKey string) error {
	tmpl, ok := GetTemplate(templateKey)
	if !ok {
		return fmt.Errorf("template %q not found", templateKey)
	}
	q, err := s.GetQuote(ctx, quoteID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Actualizar header solo si campos vacíos (no piso lo editado por el usuario).
	updates := []string{}
	args := []any{quoteID}
	idx := 2
	if strings.TrimSpace(q.ProposalType) == "" || q.ProposalType == "commercial" {
		if tmpl.ProposalType != "" {
			updates = append(updates, fmt.Sprintf("proposal_type = $%d", idx))
			args = append(args, tmpl.ProposalType)
			idx++
		}
	}
	if strings.TrimSpace(q.ProductName) == "" && tmpl.DefaultProduct != "" {
		updates = append(updates, fmt.Sprintf("product_name = $%d", idx))
		args = append(args, tmpl.DefaultProduct)
		idx++
	}
	if strings.TrimSpace(q.ProductSubtitle) == "" && tmpl.DefaultSubtitle != "" {
		updates = append(updates, fmt.Sprintf("product_subtitle = $%d", idx))
		args = append(args, tmpl.DefaultSubtitle)
		idx++
	}
	if len(q.Tags) == 0 && len(tmpl.DefaultTags) > 0 {
		updates = append(updates, fmt.Sprintf("tags = $%d", idx))
		args = append(args, pqStringArray(tmpl.DefaultTags))
		idx++
	}
	if len(updates) > 0 {
		updates = append(updates, "updated_at = NOW()")
		query := fmt.Sprintf(`UPDATE cotizador_quotes SET %s WHERE id::text = $1`, strings.Join(updates, ", "))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("update header: %w", err)
		}
	}
	// Upsert secciones (no borra existentes con mismo key, solo overrides).
	for _, sec := range tmpl.Sections {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cotizador_quote_sections (quote_id, section_key, title, content_md, sort_order)
			VALUES ($1::uuid, $2, $3, $4, $5)
			ON CONFLICT (quote_id, section_key) DO UPDATE
			SET title = EXCLUDED.title, content_md = EXCLUDED.content_md,
			    sort_order = EXCLUDED.sort_order, updated_at = NOW()
		`, quoteID, sec.Key, sec.Title, sec.ContentMD, sec.SortOrder); err != nil {
			return fmt.Errorf("upsert section %q: %w", sec.Key, err)
		}
	}
	return tx.Commit()
}

// pqStringArray es un helper local para pq.Array sin importar pq aquí
// (ya está importado en quotes.go pero es de package internal).
func pqStringArray(v []string) any {
	return pqStringArrayValue(v)
}

// === Plantillas hardcoded ===

func templateImplementationiTechDev() Template {
	return Template{
		Key:             "itechdev_implementation_v1",
		Name:            "Propuesta de Implementación iTechDev",
		Description:     "Estructura completa de 13 secciones estilo PARK Salesforce. Usar para implementaciones largas (Salesforce, SAP S/4HANA, plataformas enterprise).",
		ProposalType:    "implementation",
		DefaultProduct:  "[Producto/Plataforma]",
		DefaultSubtitle: "Modernización de la operación [área]",
		DefaultTags:     []string{"Enterprise", "Pipeline", "Cotizaciones", "Visibilidad ejecutiva"},
		Sections: []TemplateSection{
			{Key: "resumen", Title: "1. Resumen Ejecutivo", SortOrder: 10, ContentMD: `[CLIENTE], como [descripción del cliente], requiere [necesidad principal] mediante una plataforma que le brinde [beneficios clave].

ITECHDEV MX, S.A. de C.V. propone la implementación de **[Producto/Plataforma]** como solución central para [contexto]. La solución habilitará [capacidades clave] bajo un modelo **Fit-to-Standard**.

### 1.1 Objetivos
- Estandarizar el proceso bajo una plataforma única y escalable
- Habilitar visibilidad en tiempo real
- Centralizar la gestión de [alcance]
- Establecer base sólida para crecer hacia capacidades avanzadas

### 1.2 Resumen de la oferta
| Inversión | Duración | Usuarios |
|---|---|---|
| **$[total] [moneda]** | [N] semanas | [N] usuarios |`},
			{Key: "perfil", Title: "2. Perfil de iTechDev", SortOrder: 20, ContentMD: `ITECHDEV MX, S.A. de C.V. es una consultora de tecnología empresarial con sede en Monterrey, Nuevo León, especializada en la implementación de plataformas Salesforce, SAP S/4HANA, Microsoft Azure y desarrollo de software a la medida.

### Cifras clave
- **+7 años** de experiencia
- **+200 proyectos** completados
- **+50 especialistas** certificados
- **100% equipo bilingüe**

### Áreas de especialización
- Plataformas Salesforce: Sales Cloud, Service Cloud, Marketing Cloud
- SAP S/4HANA: implementaciones, migraciones y desarrollo ABAP
- Microsoft Azure y Microsoft 365
- Desarrollo a la medida: web, móvil y RPA
- Integraciones empresariales y arquitecturas API-first

### Diferenciadores
- Conocimiento profundo del contexto fiscal mexicano (CFDI 4.0, SAT)
- Equipo bilingüe con documentación en español
- Tarifas competitivas vs partners de USA sin sacrificar calidad
- Metodología híbrida (cascada + ágil)
- Modelo de gobierno transparente con KPIs y reporting ejecutivo semanal`},
			{Key: "metodologia", Title: "3. Metodología de Implementación", SortOrder: 30, ContentMD: `Metodología híbrida que combina la disciplina de cascada con la flexibilidad ágil. Enfoque **Fit-to-Standard** prioriza capacidades nativas para maximizar valor a corto plazo.

### Principios rectores
- **Fit-to-Standard primero**: adoptar el estándar antes de personalizar
- **Iteraciones cortas**: entregables visibles cada 2 semanas
- **Co-creación con el cliente**: usuarios clave participan desde Discovery
- **Calidad por diseño**: testing continuo
- **Knowledge transfer**: cliente queda autosuficiente en administración básica

### Las 6 fases del proyecto
| # | Fase | Objetivo y entregables | Duración |
|---|---|---|---|
| 1 | Discovery & Blueprint | Workshops, blueprint funcional, modelo de datos | Semanas 1-2 |
| 2 | Configuración Core | Configuración base de la plataforma | Semanas 3-5 |
| 3 | Funcionalidades Específicas | Adaptaciones según alcance | Semanas 5-7 |
| 4 | Datos & Reportes | Migración, reportes y dashboards | Semanas 7-8 |
| 5 | UAT & Capacitación | Pruebas con usuarios clave, capacitación por rol | Semanas 9-10 |
| 6 | Go-Live & Hypercare | Salida a producción, soporte intensivo, transferencia | Semanas 11-12 |`},
			{Key: "alcance", Title: "4. Alcance de la Implementación", SortOrder: 40, ContentMD: `### 4.1 Discovery y Diseño (Blueprint)
- Workshops de levantamiento con stakeholders
- Análisis Fit-to-Standard
- Diseño del blueprint funcional documentado
- Modelo de datos estándar

### 4.2 Configuración Core
[Describir las capacidades core a configurar]

### 4.3 Funcionalidades específicas
[Describir el alcance funcional específico]

### 4.4 Seguridad y Modelo Organizacional
- Usuarios, perfiles y roles
- Permission Sets personalizados
- Reglas de visibilidad

### 4.5 Datos y Reportes
- Migración inicial validada
- Dashboards y KPIs operativos`},
			{Key: "limites", Title: "5. Supuestos y Límites de Alcance", SortOrder: 50, ContentMD: `| Componente | Límite considerado |
|---|---|
| Usuarios | Hasta [N] |
| Roles | Hasta [N] |
| Procesos | [N] proceso(s) |
| Reportes | Hasta [N] |
| Dashboards | Hasta [N] |
| Carga de datos | Hasta [N] registros por objeto |

Cualquier requerimiento fuera de estos límites se cotiza como alcance adicional vía orden de cambio formal.`},
			{Key: "fuera-alcance", Title: "6. Fuera de Alcance", SortOrder: 60, ContentMD: `Los siguientes elementos no forman parte del alcance:

- Personalizaciones o desarrollos a medida fuera de configuración estándar
- Integraciones con sistemas de terceros (cotizables aparte)
- Migraciones complejas (>10K registros por objeto)
- Soporte mensual continuo post go-live (Managed Services)
- Licenciamiento de plataforma (cliente lo adquiere directo)

### 6.1 Capacidades para Fase 2
- [Listar capacidades futuras]`},
			{Key: "inversion", Title: "7. Inversión", SortOrder: 70, ContentMD: `Modalidad **precio cerrado (fixed price)** por el alcance descrito.

### Desglose
| Concepto | Modalidad | Duración | Inversión |
|---|---|---|---|
| Implementación (Fases 1-5) | Precio cerrado | [N] semanas | $[monto] |
| Hypercare post Go-Live | Incluido | 2 semanas | Incluido |
| **TOTAL** | Precio cerrado | [N] semanas | **$[total] [moneda]** |

### Esquema de pago
| Pago | Monto | Momento |
|---|---|---|
| Anticipo (40%) | $[40%] | Firma de contrato y kickoff |
| Intermedio (30%) | $[30%] | Término de configuración core |
| Final (30%) | $[30%] | Go-Live exitoso |

Facturación: CFDI 4.0 con complemento de pago. Transferencia electrónica.`},
			{Key: "cronograma", Title: "8. Cronograma Detallado", SortOrder: 80, ContentMD: `| Semanas | Fase | Actividades clave |
|---|---|---|
| S 1-2 | Discovery & Blueprint | Kickoff · Workshops · Fit-to-Standard · Blueprint funcional |
| S 3-5 | Configuración Core | Configuración base · Modelos · Permisos básicos |
| S 5-7 | Funcionalidades específicas | [Listar] |
| S 7-8 | Datos & Reportes | Carga maestros · Validación · Reportes · Dashboards |
| S 9-10 | UAT & Capacitación | Pruebas · UAT · Capacitación · Sign-off |
| S 11-12 | Go-Live & Hypercare | Producción · Soporte intensivo · Transferencia |

### Hitos clave
- **Semana 2**: Blueprint funcional firmado
- **Semana 5**: Configuración core completa
- **Semana 8**: Datos migrados y reportes disponibles
- **Semana 10**: UAT firmada
- **Semana 12**: Go-Live y aceptación formal`},
			{Key: "equipo", Title: "9. Equipo de Trabajo y Gobierno", SortOrder: 90, ContentMD: `| Rol | Dedicación | Responsabilidades |
|---|---|---|
| Project Manager | 25% | Coordinación, reporting, gestión de riesgos |
| Consultor Funcional Senior | 100% | Diseño, blueprint, configuración |
| Admin / Configurador | 100% | Perfiles, permission sets, flujos |
| Consultor de Datos | 25% | Mapeo, validación y migración |
| QA / Tester | 40% | Casos de prueba, ejecución, soporte UAT |

### Modelo de gobierno
- **Comité Ejecutivo** (mensual): Sponsors, decisiones estratégicas
- **Comité de Proyecto** (semanal): PM, líderes funcionales, status report
- **Daily standup** (diario, opcional): equipo técnico
- **Reportes ejecutivos**: status semanal estandarizado
- **Gestión de riesgos**: registro formal con owner asignado`},
			{Key: "capacitacion", Title: "10. Capacitación y Documentación", SortOrder: 100, ContentMD: `### Plan de capacitación por rol
| Audiencia | Duración | Contenido |
|---|---|---|
| Usuarios finales | 4-6 horas | Uso diario de la plataforma |
| Gerentes / Supervisores | 4-6 horas | Reportes, dashboards, supervisión |
| Administradores | 4-6 horas | Mantenimiento, gestión usuarios, ajustes |

### Documentación entregable
- Blueprint funcional firmado
- Manual de configuración del sistema
- Guías de usuario por rol (en español)
- Procesos documentados con workflows
- Matriz de permisos y modelo de seguridad
- Procedimientos de respaldo
- Casos de prueba ejecutados durante UAT`},
			{Key: "testing", Title: "11. Estrategia de Testing y UAT", SortOrder: 110, ContentMD: `### Niveles de pruebas
- **Pruebas unitarias**: cada configuración probada por su consultor
- **Pruebas funcionales por módulo**: validación end-to-end por componente
- **Pruebas integradas**: flujos completos cross-módulos
- **Pruebas de regresión**: antes de cada despliegue mayor
- **UAT**: por usuarios clave del cliente con escenarios reales

### Proceso de UAT
1. Definición conjunta de casos de prueba basados en escenarios reales
2. Capacitación previa de UAT champions
3. Ejecución supervisada en ambiente de QA
4. Registro y triaje de hallazgos por severidad
5. Corrección por equipo iTechDev
6. **Sign-off formal del cliente** como condición previa al go-live

### Criterios de aceptación
- 100% casos de prueba críticos exitosos
- 0 defectos críticos abiertos al go-live
- Datos maestros validados y reconciliados
- Capacitación completada para todos los usuarios objetivo
- Documentación entregada y aceptada`},
			{Key: "entregables", Title: "12. Entregables Principales", SortOrder: 120, ContentMD: `### 12.1 Entregables funcionales
- Diseño de solución y blueprint funcional firmado
- Plataforma configurada y productiva
- Datos iniciales migrados y validados
- Reportes operativos y dashboards operando

### 12.2 Entregables documentales
- Blueprint funcional aprobado
- Manual de configuración del sistema
- Guías de usuario por rol en español
- Matriz de permisos y modelo de seguridad
- Casos de prueba UAT con sign-off
- Acta de cierre del proyecto

### 12.3 Entregables de habilitación
- Capacitación completada por rol
- Hypercare durante 2 semanas
- Transferencia formal a operación`},
			{Key: "terminos", Title: "13. Términos y Condiciones", SortOrder: 130, ContentMD: `### 13.1 Condiciones comerciales
- Forma de pago: 40% anticipo · 30% intermedio · 30% pre go-live
- Facturación: CFDI 4.0 con complemento de pago
- Transferencia electrónica
- Moneda: [moneda]
- Precios sin IVA ni retenciones
- Vigencia de cotización: 30 días naturales

### 13.2 Garantía
- 90 días naturales sobre configuraciones entregadas, contados desde el go-live
- Cubre corrección de defectos sobre alcance entregado y firmado
- Excluidas: incidencias por cambios del cliente, terceros o actualizaciones de plataforma

### 13.3 Responsabilidades del cliente
- Sponsor ejecutivo y Project Manager designados
- Disponibilidad de líderes funcionales y usuarios clave
- Adquisición oportuna de licencias
- Acceso a fuentes de datos para migración
- Validación y firma de entregables clave en plazos
- Toma de decisiones oportuna

### 13.4 Confidencialidad
Toda la información se considera confidencial. Se firma NDA antes del inicio formal del proyecto.`},
		},
	}
}

func templateCommercialiTechDev() Template {
	return Template{
		Key:             "itechdev_commercial_v1",
		Name:            "Propuesta Comercial iTechDev (corta)",
		Description:     "Estructura corta de 6 secciones estilo Mercedes Serrala. Usar para licenciamiento + setup de productos certificados (Serrala, FS², SaaS embebidos en SAP).",
		ProposalType:    "commercial",
		DefaultProduct:  "[Producto/Solución]",
		DefaultSubtitle: "[Descriptivo del producto]",
		DefaultTags:     []string{"Solución certificada", "Embedded", "S/4HANA Ready"},
		Sections: []TemplateSection{
			{Key: "antecedentes", Title: "1. Antecedentes y Contexto", SortOrder: 10, ContentMD: `Tras la sesión de demostración realizada con el equipo de [área] de [CLIENTE], donde se presentó la solución **[Producto]** y sus capacidades [contexto], se procede a formalizar la presente propuesta comercial.

ITECHDEV MX, S.A. de C.V., en alianza con **[partner si aplica]**, propone la implementación de [Producto] para [objetivo], aprovechando la inversión existente en [plataforma base] de [CLIENTE].

### Diferenciadores clave
✅ **[Diferenciador 1]** — descripción
✅ **[Diferenciador 2]** — descripción
✅ **[Diferenciador 3]** — descripción
✅ **[Diferenciador 4]** — descripción`},
			{Key: "alcance", Title: "2. Alcance de la Solución", SortOrder: 20, ContentMD: `### 2.1 Volumetría contemplada
- [Volumen principal del servicio]
- [Canal/medio de captura]
- [Integración nativa con sistema base]

### 2.2 Componentes funcionales incluidos
- **[Componente 1]** — descripción
- **[Componente 2]** — descripción
- **[Componente 3]** — descripción

### 2.3 Procesos automatizados / capacidades habilitadas
- [Capacidad 1]
- [Capacidad 2]
- [Capacidad 3]

### 2.4 Fuera de alcance
La presente propuesta cubre exclusivamente [scope]. Pueden cotizarse por separado:
- [Módulo extra 1]
- [Módulo extra 2]
- Licenciamiento de plataforma base (provisto por el cliente)`},
			{Key: "plan", Title: "3. Plan de Implementación", SortOrder: 30, ContentMD: `[Producto] es una solución [tipo] lista para [target system]. La implementación se enfoca en **instalación, configuración y adopción** de las funcionalidades estándar.

**Duración estimada total: [N] meses calendario** a partir del kickoff oficial.

| # | Fase | Actividades principales | Duración |
|---|---|---|---|
| 1 | Kickoff & Setup | Reunión arranque, conformación de equipos, instalación, validación prerrequisitos | [N] semanas |
| 2 | Configuración | [Específico al producto] | [N] semanas |
| 3 | Pruebas Integrales | Escenarios end-to-end, ajustes finos | [N] semanas |
| 4 | UAT & Capacitación | Pruebas con usuarios clave, capacitación | [N] semanas |
| 5 | Go-Live & Hypercare | Producción, soporte intensivo, transferencia | [N] semanas |`},
			{Key: "inversion", Title: "4. Inversión", SortOrder: 40, ContentMD: `La inversión se compone de [estructura: suscripción anual + implementación / único cargo / etc].

| Concepto | Modalidad | Monto |
|---|---|---|
| [Concepto 1] | [Recurrente/Único] | $[monto] |
| [Concepto 2] | [Recurrente/Único] | $[monto] |
| **TOTAL [PERIODO]** | | **$[total] [moneda]** |

### 4.1 Condiciones comerciales
- Moneda: [USD/MXN]
- Sin IVA ni retenciones aplicables
- Vigencia de propuesta: 30 días naturales

### 4.2 Esquema de pago
- [Esquema específico del producto]`},
			{Key: "responsabilidades", Title: "5. Responsabilidades del Cliente", SortOrder: 50, ContentMD: `Para asegurar el cumplimiento del cronograma propuesto, [CLIENTE] deberá disponer de los siguientes recursos:

- **Sponsor ejecutivo** del proyecto
- **Project Manager** por parte del cliente como contraparte
- **Líder de proceso de negocio** disponible para validaciones y workshops
- **Equipo de TI** para accesos, conectividad e integración
- **Acceso a ambientes** de desarrollo, QA y productivo
- **Datos maestros** requeridos para configuración
- **Disponibilidad de usuarios clave** para sesiones de UAT y capacitación`},
			{Key: "siguientes-pasos", Title: "6. Próximos Pasos", SortOrder: 60, ContentMD: `1. Confirmación formal de aceptación de la presente propuesta
2. Firma del contrato marco y NDA correspondientes
3. Designación de los equipos de trabajo de ambas partes
4. **Kickoff oficial del proyecto**`},
		},
	}
}

func templateServiceiTechDev() Template {
	return Template{
		Key:             "itechdev_service_v1",
		Name:            "Propuesta de Servicio iTechDev (custom dev)",
		Description:     "Estructura de 7 secciones estilo Mercedes RPA Goods Receipts. Usar para desarrollos a la medida con stack iTechDev (RPA, ABAP, Fiori, custom).",
		ProposalType:    "service",
		DefaultProduct:  "[Servicio/Solución a la medida]",
		DefaultSubtitle: "Solución ITechDev — [stack tecnológico]",
		DefaultTags:     []string{"Desarrollo a la medida", "Integración SAP S/4HANA"},
		Sections: []TemplateSection{
			{Key: "antecedentes", Title: "1. Antecedentes y Contexto", SortOrder: 10, ContentMD: `[CLIENTE] ha solicitado a ITECHDEV MX, S.A. de C.V., la cotización de un servicio de [objetivo principal], con el objetivo de optimizar [proceso actual], reducir [problemas existentes] y asegurar [beneficios buscados].

La presente propuesta describe una **solución a la medida** desarrollada por iTechDev, basada en tecnologías [stack] y totalmente integrada con [sistema cliente].

### Diferenciadores clave de la solución iTechDev

✅ **Solución a la Medida** — Diseñada específicamente para [proceso del cliente]. Sin licenciamiento recurrente — la propiedad intelectual queda en el cliente.

✅ **Stack Tecnológico Estándar** — Construida sobre [tecnologías estándar]. Sin dependencias propietarias.

✅ **Implementación Ágil** — Entrega en **[N] meses** gracias al alcance acotado y al equipo multidisciplinario.

✅ **Experiencia Comprobada** — +7 años de experiencia. 200+ proyectos completados. 50+ especialistas certificados.`},
			{Key: "alcance", Title: "2. Alcance Funcional", SortOrder: 20, ContentMD: `### 2.1 Objetivo del servicio
[Descripción precisa del objetivo del servicio, incluyendo entradas, transformaciones y salidas].

### 2.2 Flujo del proceso automatizado

**PASO 1 — [Nombre]**
[Descripción del paso 1].

**PASO 2 — [Nombre]**
[Descripción del paso 2].

**PASO 3 — [Nombre]**
[Descripción del paso 3].

### 2.3 Funcionalidades incluidas
- [Funcionalidad 1]
- [Funcionalidad 2]
- [Funcionalidad 3]
- Bitácora completa de auditoría y trazabilidad

### 2.4 Fuera de alcance
- [Item fuera 1]
- [Item fuera 2]
- Licenciamiento de [herramientas terceros] — el cliente provee
- Soporte y mantenimiento post go-live (cotizable aparte)`},
			{Key: "arquitectura", Title: "3. Arquitectura Técnica", SortOrder: 30, ContentMD: `La solución se compone de **[N] capas** claramente diferenciadas, integradas a través de servicios estándar.

| # | Capa | Componentes |
|---|---|---|
| 01 | **External Sources** | [Descripción] |
| 02 | **Processing Layer** | [Descripción] |
| 03 | **Core (Custom Build)** | [Descripción] |
| 04 | **Presentation Layer** | [Descripción] |

### 3.1 Stack tecnológico
- **[Tecnología 1]**: [propósito]
- **[Tecnología 2]**: [propósito]
- **Persistencia**: [tipo]
- **Comunicación**: [protocolos]
- **Frontend**: [tecnología]`},
			{Key: "plan", Title: "4. Plan de Implementación", SortOrder: 40, ContentMD: `**Duración total: [N] meses ([N] semanas)** a partir del kickoff oficial.

| # | Fase | Actividades principales | Duración |
|---|---|---|---|
| 1 | Kickoff & Análisis | Arranque, validación prerrequisitos, revisión de inputs reales | 1 semana |
| 2 | Desarrollo | Construcción del [componente principal] + lógica custom | [N] semanas |
| 3 | Aplicación / UI | Desarrollo del [frontend] | 1 semana |
| 4 | Pruebas & UAT | Pruebas integrales end-to-end, UAT con usuarios clave | [N] semanas |
| 5 | Go-Live & Hypercare | Despliegue producción, soporte intensivo y transferencia | [N] semanas |

### 4.1 Equipo asignado
| Rol | Esfuerzo (hrs) | Responsabilidades |
|---|---|---|
| Consultor [tech 1] | [N] hrs | [responsabilidades] |
| Consultor [tech 2] Senior | [N] hrs | [responsabilidades] |
| Consultor [tech 3] Senior | [N] hrs | [responsabilidades] |
| Project Manager | [N] hrs | Gestión de proyecto, comunicación cliente, control avance |
| **TOTAL** | **[N] hrs** | Equipo multidisciplinario iTechDev |`},
			{Key: "inversion", Title: "5. Inversión", SortOrder: 50, ContentMD: `Modalidad **precio cerrado (fixed price)** por el alcance descrito en la sección 2. Pesos mexicanos sin IVA.

| Concepto | Modalidad | Monto |
|---|---|---|
| Desarrollo solución completa: [resumen stack] ([N] hrs) | Precio cerrado | $[monto] |
| **TOTAL DEL PROYECTO** | Pago único | **$[total] [moneda]** |

### 5.1 Condiciones comerciales
- Moneda: [moneda]
- Sin IVA ni retenciones
- Vigencia: 30 días naturales
- Modalidad: precio cerrado por alcance descrito
- Cualquier requerimiento fuera de alcance: orden de cambio formal
- **Garantía: 30 días naturales post go-live** para corrección de defectos`},
			{Key: "premisas", Title: "6. Premisas y Responsabilidades del Cliente", SortOrder: 60, ContentMD: `### 6.1 Premisas técnicas
- [CLIENTE] **provee [recurso técnico clave]** con licencias suficientes para la operación
- [CLIENTE] provee [otros recursos]
- iTechDev contará con accesos a los ambientes [dev/QA/prod]
- **Disponibilidad de [datos/inputs] reales desde el inicio** del proyecto

### 6.2 Recursos del cliente
- Sponsor ejecutivo del proyecto
- Project Manager por parte del cliente como contraparte
- Líder funcional de **[área 1]** disponible para validaciones, definición de reglas de negocio y workshops
- Líder funcional de **[área 2]** para validar el flujo
- Equipo de TI para gestión de accesos y conectividad
- Disponibilidad de usuarios clave para sesiones de UAT y capacitación`},
			{Key: "siguientes-pasos", Title: "7. Próximos Pasos", SortOrder: 70, ContentMD: `1. Confirmación formal de aceptación de la presente propuesta
2. Firma del contrato marco y NDA correspondientes
3. Designación de los equipos de trabajo de ambas partes
4. **Validación de prerrequisitos técnicos**
5. Kickoff oficial del proyecto`},
		},
	}
}
