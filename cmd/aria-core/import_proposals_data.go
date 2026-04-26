package main

import (
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
)

func mustDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// proposalsToImport retorna las 3 propuestas históricas extraídas de los docx.
func proposalsToImport() []proposalImport {
	return []proposalImport{
		propPARKSalesforce(),
		propMercedesSerrala(),
		propMercedesGoodsReceipts(),
	}
}

// === PROPUESTA 1: PARK Salesforce Sales Cloud ===
func propPARKSalesforce() proposalImport {
	return proposalImport{
		Folio:           "COT-PARK-2026-001",
		ProposalType:    "implementation",
		ProductName:     "Salesforce Sales Cloud Enterprise",
		ProductSubtitle: "Modernización de la operación comercial",
		Tags:            []string{"CRM Enterprise", "Pipeline", "Cotizaciones", "Visibilidad ejecutiva"},
		LeadName:        "Lilibeth López",
		LeadCompany:     "PARK Desarrolladora Inmobiliaria",
		LeadEmail:       "veronica.lilopez@prk.com.mx",
		PreparedForArea: "Desarrolladora Inmobiliaria",
		PreparedForContactName:  "Lilibeth López",
		PreparedForContactEmail: "veronica.lilopez@prk.com.mx",
		IssueDate:       mustDate("2026-04-28"),
		ValidDays:       30,
		Currency:        "MXN",
		Items: []cotizador.CreateQuoteItemParams{
			{SKU: "SF-IMPL-FIXED", Description: "Implementación Sales Cloud Enterprise (Fases 1-5, 10 semanas)", Qty: 1, UnitPrice: 1075000},
		},
		Justification: "Precio cerrado por alcance descrito en secciones 4 y 5. 25 usuarios, hasta 5 roles, hasta 100 productos/unidades, hasta 10,000 registros por objeto. Hypercare 2 semanas incluido. No incluye licenciamiento Salesforce (cliente lo adquiere directo).",
		Terms:         "40% anticipo a firma de contrato y kickoff.\n30% al término de configuración core (semana 8).\n30% pre go-live (semana 12).\nFacturación: CFDI 4.0 con complemento de pago. Transferencia electrónica. Vigencia: 30 días naturales.",
		RFPText:       "PARK requiere modernizar y estandarizar su operación comercial mediante una plataforma CRM con visibilidad ejecutiva del pipeline y escalabilidad para crecimiento del portafolio inmobiliario. Necesidades clave: gestión de leads/cuentas/contactos/oportunidades, portafolio inmobiliario, cotizaciones, web-to-lead.",
		Sections: []sectionImport{
			{Key: "resumen", Title: "1. Resumen Ejecutivo", SortOrder: 10, ContentMD: `PARK, como desarrolladora inmobiliaria en crecimiento, requiere modernizar y estandarizar su operación comercial mediante una plataforma CRM que le brinde visibilidad ejecutiva del pipeline, control sobre el proceso de ventas y la escalabilidad necesaria para el crecimiento de su portafolio inmobiliario.

ITECHDEV MX, S.A. de C.V. propone la implementación de **Salesforce Sales Cloud Enterprise** como plataforma central para la gestión comercial end-to-end. La solución habilitará la administración estructurada de leads, cuentas, contactos y oportunidades, junto con la gestión del portafolio inmobiliario y la generación de cotizaciones, todo bajo un modelo **Fit-to-Standard** que aprovecha al máximo las capacidades nativas de Salesforce.

### 1.1 Objetivos
- Estandarizar el proceso comercial bajo una plataforma única y escalable.
- Habilitar visibilidad en tiempo real del pipeline y forecasting comercial.
- Centralizar la gestión del portafolio inmobiliario y cotizaciones comerciales.
- Capturar leads desde canales digitales mediante Web-to-Lead.
- Establecer base sólida para crecer hacia capacidades avanzadas (Service Cloud, Marketing Cloud, CPQ) en fases futuras.

### 1.2 Resumen de la oferta
| Inversión | Duración | Usuarios |
|---|---|---|
| **$1,075,000 MXN** | 12 semanas | 25 usuarios |

*Precios en MXN sin IVA. No incluye licenciamiento Salesforce.*`},
			{Key: "perfil", Title: "2. Perfil de iTechDev", SortOrder: 20, ContentMD: `ITECHDEV MX, S.A. de C.V. es una consultora de tecnología empresarial con sede en Monterrey, Nuevo León, especializada en la implementación de plataformas Salesforce, SAP S/4HANA, Microsoft Azure y desarrollo de software a la medida para empresas mid-market y enterprise en México.

### Cifras clave
- **+7 años** de experiencia
- **+200 proyectos** completados
- **+50 especialistas** certificados
- **100% equipo bilingüe**

### Áreas de especialización
- Plataformas Salesforce: Sales Cloud, Service Cloud, Marketing Cloud, Account Engagement
- SAP S/4HANA: implementaciones, migraciones y desarrollo ABAP
- Microsoft Azure y Microsoft 365
- Desarrollo a la medida: web, móvil y RPA
- Integraciones empresariales y arquitecturas API-first

### Diferenciadores como partner
- Conocimiento profundo del contexto fiscal y normativo mexicano (CFDI 4.0, SAT)
- Equipo bilingüe con documentación y capacitación en español
- Tarifas competitivas vs partners de USA sin sacrificar calidad
- Metodología híbrida (cascada + ágil) probada en +200 proyectos
- Modelo de gobierno transparente con KPIs medibles y reporting ejecutivo semanal`},
			{Key: "metodologia", Title: "3. Metodología de Implementación", SortOrder: 30, ContentMD: `Metodología híbrida que combina la disciplina de cascada con la flexibilidad ágil, alineada al Salesforce Implementation Lifecycle. Enfoque **Fit-to-Standard** prioriza capacidades nativas para maximizar valor a corto plazo.

### Principios rectores
- **Fit-to-Standard primero**: adoptar el estándar antes de personalizar
- **Iteraciones cortas**: entregables visibles cada 2 semanas
- **Co-creación con el cliente**: usuarios clave participan desde Discovery
- **Calidad por diseño**: testing continuo, no solo al final
- **Knowledge transfer**: cliente queda autosuficiente en administración básica

### Las 6 fases del proyecto
| # | Fase | Objetivo y entregables | Duración |
|---|---|---|---|
| 1 | Discovery & Blueprint | Workshops, blueprint funcional, modelo de datos | Semanas 1-2 |
| 2 | Configuración Core | Sales Cloud: Accounts, Leads, Contacts, Opportunities, Web-to-Lead | Semanas 3-5 |
| 3 | Productos & Cotizaciones | Portafolio inmobiliario, Price Books, Quotes, plantillas | Semanas 5-7 |
| 4 | Datos & Reportes | Migración de datos maestros, reportes y dashboards | Semanas 7-8 |
| 5 | UAT & Capacitación | Pruebas con usuarios clave, capacitación por rol | Semanas 9-10 |
| 6 | Go-Live & Hypercare | Salida a producción, soporte intensivo, transferencia | Semanas 11-12 |`},
			{Key: "alcance", Title: "4. Alcance de la Implementación", SortOrder: 40, ContentMD: `### 4.1 Discovery y Diseño (Blueprint)
- Workshops de levantamiento con stakeholders
- Análisis Fit-to-Standard
- Diseño del blueprint funcional documentado
- Modelo de datos estándar (Accounts, Contacts, Leads, Opportunities, Products)

### 4.2 Gestión Comercial Core (Sales Cloud)
Configuración estándar end-to-end:
- Cuentas, Prospectos, Contactos, Oportunidades
- Proceso de ventas y etapas comerciales
- Pipeline y cartera de oportunidades
- Web-to-Lead para captación digital

### 4.3 Portafolio Inmobiliario y Cotizaciones
- Configuración del portafolio de productos / unidades inmobiliarias
- Parametrización de inventario comercializable
- Asociación a oportunidades comerciales
- Price Books estándar
- Quotes y plantilla de cotización

> **Supuesto:** Funcionalidades estándar; no contempla lógica avanzada de inventario en tiempo real.

### 4.4 Seguridad y Modelo Organizacional
- Usuarios, perfiles y funciones (directivos, gerentes, asesores)
- Estructura de roles jerárquica
- Permission Sets personalizados
- Reglas de visibilidad

### 4.5–4.8 Actividades, Automatización, Carga de Datos, Reportes
Configuración estándar de tareas/eventos, automatizaciones declarativas (Flow Builder), carga inicial de datos maestros validada post-carga, dashboards comerciales y KPIs de pipeline.`},
			{Key: "limites", Title: "5. Supuestos y Límites de Alcance", SortOrder: 50, ContentMD: `| Componente | Límite considerado |
|---|---|
| Usuarios | Hasta 25 |
| Roles (Funciones) | Hasta 5 |
| Perfiles / Permission Sets | Hasta 5 |
| Proceso comercial | 1 proceso |
| Etapas de Oportunidad | Hasta 10 |
| Web-to-Lead | 1 formulario |
| Assignment Rules | Hasta 5 |
| Notificaciones / Alertas | Hasta 10 |
| Automatizaciones básicas | Hasta 5 |
| Productos / Unidades | Hasta 100 registros |
| Price Books | Hasta 2 |
| Cotizaciones (Quotes) | 1 plantilla estándar |
| Reportes | Hasta 10 |
| Dashboards | Hasta 3 |
| Carga de datos | Hasta 10,000 registros por objeto |

Cualquier requerimiento que exceda estos límites se cotiza como alcance adicional vía orden de cambio formal.`},
			{Key: "fuera-alcance", Title: "6. Fuera de Alcance", SortOrder: 60, ContentMD: `Los siguientes elementos no forman parte del alcance y pueden cotizarse por separado:

- Personalizaciones o desarrollos a medida (Apex, LWC, triggers)
- Integraciones con sistemas de terceros (ERP, legacy, marketing)
- Reglas avanzadas de pricing, descuentos por volumen
- Gestión avanzada de inventario en tiempo real
- Salesforce CPQ
- Migraciones complejas (>10K registros por objeto)
- Service Cloud, Marketing Cloud, Experience Cloud
- Soporte mensual continuo post go-live (Managed Services)
- **Licenciamiento Salesforce** (lo adquiere el cliente directo)

### 6.1 Capacidades para Fase 2
- Service Cloud
- Account Engagement (Pardot)
- Experience Cloud
- Salesforce CPQ
- Integraciones contables / facturación electrónica
- Aplicaciones móviles para fuerza de ventas`},
			{Key: "inversion", Title: "7. Inversión", SortOrder: 70, ContentMD: `Modalidad **precio cerrado (fixed price)** por el alcance de las secciones 4 y 5. Pesos mexicanos sin IVA.

### Desglose
| Concepto | Modalidad | Duración | Inversión |
|---|---|---|---|
| Implementación Sales Cloud Enterprise (Fases 1-5) | Precio cerrado | 10 semanas | $1,075,000 |
| Hypercare post Go-Live | Incluido | 2 semanas | Incluido |
| **TOTAL** | Precio cerrado | 12 semanas | **$1,075,000 MXN** |

### Esquema de pago
| Pago | Monto | Momento |
|---|---|---|
| Anticipo (40%) | $430,000 MXN | Firma de contrato y kickoff |
| Intermedio (30%) | $322,500 MXN | Término de configuración core (semana 8) |
| Final (30%) | $322,500 MXN | Go-Live exitoso (semana 12) |

Facturación: CFDI 4.0 con complemento de pago. Transferencia electrónica.`},
			{Key: "cronograma", Title: "8. Cronograma Detallado", SortOrder: 80, ContentMD: `| Semanas | Fase | Actividades clave |
|---|---|---|
| S 1-2 | Discovery & Blueprint | Kickoff · Workshops · Fit-to-Standard · Blueprint funcional |
| S 3-5 | Configuración Core CRM | Accounts/Leads/Contacts/Opportunities · Proceso · Web-to-Lead |
| S 5-7 | Productos / Quotes / Seguridad | Catálogo · Price Books · Quotes · Modelo seguridad |
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
| Consultor Funcional Sr. Salesforce | 100% | Diseño, blueprint, configuración Sales Cloud |
| Salesforce Admin | 100% | Perfiles, permission sets, flujos declarativos |
| Consultor de Migración de Datos | 25% | Mapeo, validación y migración de datos maestros |
| QA / Tester | 40% | Casos de prueba, ejecución, soporte UAT |

### Modelo de gobierno
- **Comité Ejecutivo** (mensual): Sponsors, decisiones estratégicas
- **Comité de Proyecto** (semanal): PM, líderes funcionales, status report
- **Daily standup** (diario, opcional): equipo técnico
- **Reportes ejecutivos**: status semanal estandarizado
- **Gestión de riesgos**: registro formal con owner asignado
- **Control de cambios**: orden de cambio formal con impacto evaluado`},
			{Key: "capacitacion", Title: "10. Capacitación y Documentación", SortOrder: 100, ContentMD: `### Plan de capacitación por rol
| Audiencia | Duración | Contenido |
|---|---|---|
| Vendedores / Asesores | 4-6 horas | Uso diario: leads, oportunidades, actividades, cotizaciones |
| Gerentes Comerciales | 4-6 horas | Reportes, dashboards, forecasting, supervisión pipeline |
| Equipo de Servicio | 4-6 horas | Actividades de seguimiento, comunicación con clientes |
| Administrador PARK | 4-6 horas | Mantenimiento, gestión usuarios, ajustes declarativos |

### Documentación entregable
- Blueprint funcional firmado
- Manual de configuración del sistema
- Guías de usuario por rol (en español)
- Procesos documentados con workflows
- Matriz de permisos y modelo de seguridad
- Procedimientos de respaldo
- Casos de prueba ejecutados durante UAT
- Bitácora de configuraciones`},
			{Key: "testing", Title: "11. Estrategia de Testing y UAT", SortOrder: 110, ContentMD: `### Niveles de pruebas
- **Pruebas unitarias**: cada configuración probada por su consultor
- **Pruebas funcionales por módulo**: validación end-to-end por componente
- **Pruebas integradas**: flujos completos cross-módulos
- **Pruebas de regresión**: antes de cada despliegue mayor
- **UAT**: por usuarios clave de PARK con escenarios reales

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
- Plataforma Salesforce Sales Cloud configurada y productiva
- Datos iniciales migrados y validados
- Reportes operativos y dashboards comerciales operando
- Web-to-Lead activo para captación digital
- Plantilla de cotización configurada

### 12.2 Entregables documentales
- Blueprint funcional aprobado
- Manual de configuración del sistema
- Guías de usuario por rol en español
- Matriz de permisos y modelo de seguridad
- Procedimientos de respaldo y recuperación
- Casos de prueba UAT con sign-off
- Acta de cierre del proyecto

### 12.3 Entregables de habilitación
- Capacitación completada por rol
- Hypercare durante 2 semanas
- Transferencia formal a operación
- Plan de evolución sugerido para Fase 2`},
			{Key: "terminos", Title: "13. Términos y Condiciones", SortOrder: 130, ContentMD: `### 13.1 Condiciones comerciales
- Forma de pago: 40% anticipo · 30% semana 8 · 30% pre go-live
- Facturación: CFDI 4.0 con complemento de pago
- Transferencia electrónica
- Moneda: Pesos mexicanos (MXN)
- Precios sin IVA ni retenciones
- Vigencia de cotización: 30 días naturales

### 13.2 Garantía
- 90 días naturales sobre configuraciones entregadas, contados desde el go-live
- Cubre corrección de defectos sobre alcance entregado y firmado
- Excluidas: incidencias por cambios del cliente, terceros o actualizaciones de plataforma Salesforce

### 13.3 Exclusiones
- Licenciamiento Salesforce (cliente lo adquiere directo)
- Integraciones con sistemas externos
- Soporte mensual continuo post-proyecto (Managed Services)
- Personalizaciones de Fase 2

### 13.4 Responsabilidades del cliente
- Sponsor ejecutivo y Project Manager designados
- Disponibilidad de líderes funcionales y usuarios clave
- Adquisición oportuna de licencias Salesforce
- Acceso a fuentes de datos para migración
- Validación y firma de entregables clave en plazos
- Toma de decisiones oportuna

### 13.5 Confidencialidad
Toda la información se considera confidencial. Se firma NDA antes del inicio formal del proyecto.`},
		},
	}
}

// === PROPUESTA 2: Mercedes-Benz × Serrala FS² AccountsPayable ===
func propMercedesSerrala() proposalImport {
	return proposalImport{
		Folio:           "ITD-2026-MBM-FS2AP-001",
		ProposalType:    "commercial",
		ProductName:     "Serrala FS² AccountsPayable",
		ProductSubtitle: "Automatización de Cuentas por Pagar",
		Tags:            []string{"Solución certificada SAP", "Fully Embedded", "S/4HANA Ready"},
		LeadName:        "Dirección de Finanzas — Cuentas por Pagar",
		LeadCompany:     "Mercedes-Benz México",
		PreparedForArea: "Dirección de Finanzas - Cuentas por Pagar",
		IssueDate:       mustDate("2026-04-25"),
		ValidDays:       30,
		Currency:        "USD",
		Items: []cotizador.CreateQuoteItemParams{
			{SKU: "FS2AP-SUB", Description: "Suscripción de Software FS² AccountsPayable (hasta 20,000 facturas/año)", Qty: 1, UnitPrice: 30000},
			{SKU: "FS2AP-IMPL", Description: "Implementación, configuración, integración SAP y go-live", Qty: 1, UnitPrice: 90000},
		},
		Justification: "Suscripción anual recurrente $30K USD + implementación pago único $90K USD. Año 2 en adelante: $30K USD anual.",
		Terms:         "Suscripción anual: pago anticipado a la firma del contrato.\nImplementación: 40% a la firma, 30% al término de la fase de configuración, 30% contra go-live exitoso.\nMoneda: USD. Sin IVA ni retenciones. Vigencia: 30 días naturales.",
		RFPText:       "Mercedes-Benz México busca automatizar el procesamiento de hasta 20,000 facturas anuales del área de Cuentas por Pagar, aprovechando la inversión existente en SAP. Tras sesión de demostración con el equipo de CxP donde se presentó FS² AccountsPayable y sus capacidades end-to-end de Procure-to-Pay, se solicita formalizar propuesta comercial.",
		Sections: []sectionImport{
			{Key: "antecedentes", Title: "1. Antecedentes y Contexto", SortOrder: 10, ContentMD: `Tras la sesión de demostración realizada con el equipo de Cuentas por Pagar de Mercedes-Benz México, donde se presentó la solución **Serrala FS² AccountsPayable** y sus capacidades end-to-end de automatización del proceso Procure-to-Pay, se procede a formalizar la presente propuesta comercial.

ITECHDEV MX, S.A. de C.V., en alianza con **Serrala**, propone la implementación de FS² AccountsPayable para automatizar el procesamiento de hasta **20,000 facturas anuales**, aprovechando la inversión existente en SAP de Mercedes-Benz México.

### Diferenciadores clave de FS² AccountsPayable

✅ **Certificación SAP** — Solución certificada por SAP, *fully embedded* en namespace propio de Serrala. No requiere migración ni infraestructura paralela.

✅ **S/4HANA Ready** — Lista para SAP S/4HANA sin esfuerzo adicional de migración. Aprovecha la inversión actual en SAP del cliente.

✅ **Experiencia Global** — +30 años de experiencia. 3,000+ clientes en más de 100 países, incluyendo 25% de las Fortune Global 100.

✅ **Sector Automotriz** — Base instalada en empresas del sector automotriz e industrial: BMW, Renault, ABB, Honeywell, Whirlpool.`},
			{Key: "alcance", Title: "2. Alcance de la Solución", SortOrder: 20, ContentMD: `### 2.1 Volumetría contemplada
- Procesamiento de hasta **20,000 facturas anuales**
- Captura multicanal: papel, PDF por correo, EDI, XML / e-Invoice, portal de proveedores y captura manual
- Integración nativa con SAP de Mercedes-Benz México para posteo automatizado

### 2.2 Componentes funcionales incluidos
- **Invoice Gateway** — captura unificada multicanal
- **Smart Extraction Engine (OCR)** — reconocimiento inteligente de facturas estructuradas y no estructuradas
- **Rules Engine** — validación contra órdenes de compra y datos maestros
- **Workflow de aprobaciones y excepciones** — configurable por reglas de negocio
- **Posting Engine** — posteo automatizado al ERP SAP
- **Infocenter** — archivo digital y repositorio documental centralizado
- **My Action List** — bandeja de tareas web y móvil
- **Reporting & Dashboards** — KPIs y visibilidad en tiempo real
- **RPA aplicada al ciclo completo de facturas**

### 2.3 Procesos automatizados
- Captura desde múltiples canales de entrada
- Validación contra órdenes de compra y datos maestros
- Routing inteligente y aprobaciones por flujos configurables
- Manejo de excepciones priorizado por reglas de negocio
- Posteo automatizado en SAP
- Monitoreo y reporting ejecutivo

### 2.4 Fuera de alcance
La presente propuesta cubre **exclusivamente** el módulo FS² AccountsPayable. Pueden cotizarse por separado:
- FS² Payments (módulo de Payments Factory)
- Supplier Self-Service Portal extendido
- Supplier Evaluation & Vendor Scoring
- Hardware, infraestructura cloud y licencias SAP adicionales`},
			{Key: "plan", Title: "3. Plan de Implementación", SortOrder: 30, ContentMD: `FS² AccountsPayable es una solución certificada por SAP, *fully embedded* en el namespace propio de Serrala y lista para SAP S/4HANA. Al tratarse de una solución pre-construida, la implementación se enfoca en la **instalación, configuración y adopción** de las funcionalidades estándar.

**Duración estimada total: 3 a 4 meses calendario** a partir del kickoff oficial.

| # | Fase | Actividades principales | Duración |
|---|---|---|---|
| 1 | Kickoff & Setup | Reunión de arranque, equipos, instalación FS² en SAP, validación prerrequisitos | 2 semanas |
| 2 | Configuración Reglas de Negocio | Matriz de aprobaciones, validación contra OC, OCR, workflows excepciones, catálogos | 4-5 semanas |
| 3 | Pruebas Integrales | Escenarios end-to-end, captura multicanal, posteo SAP, ajustes finos | 3 semanas |
| 4 | UAT & Capacitación | Pruebas con usuarios clave, capacitación CxP y administradores | 2-3 semanas |
| 5 | Go-Live & Hypercare | Producción, soporte intensivo, estabilización, transferencia | 2 semanas |

> **Nota:** Por tratarse de solución certificada SAP fully embedded, **no se requiere migración ni desarrollo a la medida** — se aprovecha la inversión SAP existente.`},
			{Key: "inversion", Title: "4. Inversión", SortOrder: 40, ContentMD: `La inversión se compone de dos conceptos: una **suscripción anual de software** y un **cargo único por implementación**. Precios en USD sin IVA ni retenciones.

| Concepto | Modalidad | Monto (USD) |
|---|---|---|
| Suscripción FS² AccountsPayable (hasta 20K facturas/año) | Anual recurrente | $30,000 |
| Implementación, configuración, integración SAP y go-live | Pago único | $90,000 |
| **TOTAL AÑO 1** | | **$120,000 USD** |
| Año 2 en adelante (recurrente) | Anual | $30,000 USD |

### 4.1 Condiciones comerciales
- Moneda: USD
- Sin IVA ni retenciones aplicables
- Vigencia de propuesta: 30 días naturales
- Suscripción anual contempla actualizaciones y soporte conforme contrato Serrala

### 4.2 Esquema de pago sugerido
- **Suscripción anual**: pago anticipado a la firma del contrato
- **Implementación**: 40% a la firma, 30% al término de configuración, 30% contra go-live`},
			{Key: "responsabilidades", Title: "5. Responsabilidades del Cliente", SortOrder: 50, ContentMD: `Para asegurar el cumplimiento del cronograma propuesto, Mercedes-Benz México deberá disponer de los siguientes recursos:

- **Sponsor ejecutivo** del proyecto
- **Project Manager** por parte del cliente como contraparte
- **Líder de proceso de negocio** disponible para validaciones y workshops
- **Equipo de TI / SAP Basis** para accesos, conectividad e integración con SAP
- **Acceso a ambientes** de desarrollo, QA y productivo de SAP
- **Datos maestros** de proveedores y catálogos requeridos para configuración
- **Disponibilidad de usuarios clave** para sesiones de UAT y capacitación`},
			{Key: "siguientes-pasos", Title: "6. Próximos Pasos", SortOrder: 60, ContentMD: `1. Confirmación formal de aceptación de la presente propuesta
2. Firma del contrato marco y NDA correspondientes
3. Designación de los equipos de trabajo de ambas partes
4. **Kickoff oficial del proyecto**`},
		},
	}
}

// === PROPUESTA 3: Mercedes-Benz × Goods Receipts RPA + ABAP + Fiori ===
func propMercedesGoodsReceipts() proposalImport {
	return proposalImport{
		Folio:           "ITD-2026-MBM-GRAUTO-001",
		ProposalType:    "service",
		ProductName:     "Automatización de Goods Receipts en SAP",
		ProductSubtitle: "Solución ITechDev — RPA + ABAP + Fiori",
		Tags:            []string{"Desarrollo a la medida", "Integración SAP S/4HANA", "UiPath"},
		LeadName:        "Operaciones SAP MM - Cuentas por Pagar",
		LeadCompany:     "Mercedes-Benz México",
		PreparedForArea: "Operaciones / SAP MM / Cuentas por Pagar",
		IssueDate:       mustDate("2026-04-25"),
		ValidDays:       30,
		Currency:        "MXN",
		Items: []cotizador.CreateQuoteItemParams{
			{SKU: "GRAUTO-FIXED", Description: "Desarrollo solución completa: RPA UiPath + ABAP + SAP MM + Fiori GR Monitor (660 hrs)", Qty: 1, UnitPrice: 1250000},
		},
		Justification: "Precio cerrado fixed price por alcance descrito en sección 2. Total 660 hrs (RPA 200 + ABAP 200 + SAP MM 200 + PM 60). Sin licenciamiento recurrente, propiedad intelectual queda en el cliente.",
		Terms:         "Pago único a entrega.\nPrecio cerrado (fixed price).\nGarantía: 30 días naturales post go-live para corrección de defectos sobre alcance entregado.\nVigencia: 30 días naturales.\nCualquier requerimiento fuera de alcance se cotiza por separado vía orden de cambio.",
		RFPText:       "Mercedes-Benz México solicita servicio de automatización para generación masiva de Goods Receipts (GR) en SAP FIORI. Objetivo: optimizar proceso actual, reducir errores manuales, asegurar correcta vinculación entre Purchase Orders aprobadas y generación de GRs en el sistema. Alcance: facturas XML recibidas vía correo electrónico de fleteros y proveedores, validación contra órdenes de compra aprobadas, registro automático en SAP S/4HANA.",
		Sections: []sectionImport{
			{Key: "antecedentes", Title: "1. Antecedentes y Contexto", SortOrder: 10, ContentMD: `Mercedes-Benz México ha solicitado a ITECHDEV MX, S.A. de C.V., la cotización de un servicio de automatización para la generación masiva de **Goods Receipts (GR) en SAP FIORI**, con el objetivo de optimizar el proceso actual, reducir errores manuales y asegurar la correcta vinculación entre las Purchase Orders aprobadas y la generación de GRs dentro del sistema.

La presente propuesta describe una **solución a la medida** desarrollada por iTechDev, basada en tecnologías RPA (UiPath), desarrollo ABAP y una capa de presentación en SAP Fiori, totalmente integrada con el ERP SAP S/4HANA del cliente.

### Diferenciadores clave de la solución iTechDev

✅ **Solución a la Medida** — Diseñada específicamente para el proceso de Goods Receipts de Mercedes-Benz México. Sin licenciamiento recurrente — la propiedad intelectual queda en el cliente.

✅ **Stack Tecnológico Estándar** — Construida sobre UiPath, ABAP nativo, BAPIs estándar SAP y Fiori UX. Sin dependencias propietarias de terceros.

✅ **Implementación Ágil** — Entrega en **2 meses** gracias al alcance acotado y al equipo multidisciplinario: RPA, ABAP, SAP MM y PM.

✅ **Experiencia Comprobada** — +7 años de experiencia. 200+ proyectos completados. 50+ especialistas certificados en SAP, RPA y desarrollo enterprise.`},
			{Key: "alcance", Title: "2. Alcance Funcional", SortOrder: 20, ContentMD: `### 2.1 Objetivo del servicio
Automatizar la generación masiva de Goods Receipts (GR) en SAP FIORI a partir de **facturas XML recibidas vía correo electrónico** de fleteros y proveedores, validándolas contra órdenes de compra aprobadas en SAP y registrándolas automáticamente en el ERP.

### 2.2 Flujo del proceso automatizado

**PASO 1 — Captura Inteligente**
El robot iTech (RPA UiPath) monitorea de forma continua la bandeja de correo configurada, detecta los mensajes con facturas XML adjuntas y extrae los datos clave: proveedor, número de factura, importe y referencias a la orden de compra.

**PASO 2 — Control y Supervisión (Fiori)**
Los datos extraídos se validan contra las órdenes de compra y reglas de negocio en SAP. Si la información es correcta, el flujo continúa hacia la aprobación automática a través del Monitor de Gestión MB en Fiori. Si se detecta una inconsistencia, se genera una alerta automática al proveedor.

**PASO 3 — Registro Final en SAP**
Una vez aprobado, el sistema crea automáticamente el Goods Receipt en SAP utilizando la BAPI estándar **BAPI_GOODSMVT_CREATE**, generando el Material Document correspondiente. La contabilidad queda actualizada y la factura lista para el proceso de pago.

### 2.3 Funcionalidades incluidas
- Robot RPA en UiPath para monitoreo continuo de bandeja y extracción de XML
- Lectura e interpretación de campos clave: proveedor, número, importe, referencias de pedido
- Validación automatizada contra órdenes de compra y reglas SAP
- **Tabla Z personalizada** como repositorio intermedio (staging area) para trazabilidad
- Lógica de negocio ABAP custom para matching de PO/Contract
- Generación automática de GR mediante BAPI_GOODSMVT_CREATE
- **Aplicación Fiori (iTech Fiori GR Monitor)** para supervisión, monitoreo y override manual
- Sistema de alertas y notificaciones automáticas a proveedores en caso de inconsistencias
- Bitácora completa de auditoría y trazabilidad de cada GR generado

### 2.4 Fuera de alcance
- Automatización del proceso completo de Cuentas por Pagar (más allá del GR)
- Integración con sistemas externos al SAP (TMS, WMS, otros ERPs)
- Licenciamiento de UiPath — el cliente provee ambiente y licencias
- Ajustes a procesos ajenos a la generación de GR
- Soporte y mantenimiento post go-live (cotizable bajo horas/mes)`},
			{Key: "arquitectura", Title: "3. Arquitectura Técnica", SortOrder: 30, ContentMD: `La solución se compone de **cuatro capas** claramente diferenciadas, integradas a través de servicios estándar SAP y protocolos de comunicación seguros.

| # | Capa | Componentes |
|---|---|---|
| 01 | **External Sources** | Bandeja de correo de proveedores (Email Invoices) y Supplier Portal. Captura de XML adjuntos vía API/Upload. Punto de entrada: iTech RPA Service. |
| 02 | **Processing Layer (RPA)** | Validation Engine en UiPath. Servicio OData/HTTPS para comunicación con SAP. Componente de Alert Notification para notificación a proveedores. |
| 03 | **SAP S/4HANA Core (Custom Build)** | Z-Table como GR Staging Area. Lógica de negocio ABAP para matching contra PO/Contract. Llamada a BAPI_GOODSMVT_CREATE para generar el Material Document / GR. |
| 04 | **Presentation Layer (Fiori UX)** | Aplicación Fiori 'iTech Fiori GR Monitor' para usuarios de operaciones MB. Permite monitoreo en tiempo real, supervisión de excepciones y override manual. |

### 3.1 Stack tecnológico
- **Robotic Process Automation**: UiPath (ambiente provisto por el cliente)
- **Backend SAP**: ABAP en SAP S/4HANA, BAPI estándar BAPI_GOODSMVT_CREATE
- **Persistencia**: Z-Table custom como staging area para trazabilidad
- **Comunicación RPA-SAP**: servicios OData / HTTPS estándar
- **Frontend**: SAP Fiori (UI5) custom desarrollado por iTechDev`},
			{Key: "plan", Title: "4. Plan de Implementación", SortOrder: 40, ContentMD: `**Duración total: 2 meses (8 semanas)** a partir del kickoff oficial.

| # | Fase | Actividades principales | Duración |
|---|---|---|---|
| 1 | Kickoff & Análisis | Arranque, validación prerrequisitos UiPath/SAP, revisión XML reales, reglas de validación | 1 semana |
| 2 | Desarrollo RPA & ABAP | Robot UiPath, tabla Z, lógica ABAP de matching y BAPI. Trabajo en paralelo equipos RPA y ABAP | 4 semanas |
| 3 | Aplicación Fiori | iTech Fiori GR Monitor para supervisión, override y reporting | 1 semana |
| 4 | Pruebas & UAT | Pruebas integrales end-to-end, UAT con usuarios clave, capacitación | 1.5 semanas |
| 5 | Go-Live & Hypercare | Despliegue producción, soporte intensivo y transferencia | 0.5 semanas |

### 4.1 Equipo asignado
| Rol | Esfuerzo (hrs) | Responsabilidades |
|---|---|---|
| Consultor RPA / UiPath | 200 hrs | Desarrollo del robot, lectura correos, extracción XML, integración SAP |
| Consultor ABAP Senior | 200 hrs | Tabla Z, lógica matching, llamada BAPI, aplicación Fiori GR Monitor |
| Consultor SAP MM Senior | 200 hrs | Reglas de negocio MM, validación PO, configuración funcional, soporte UAT |
| Project Manager | 60 hrs | Gestión de proyecto, comunicación cliente, control avance y entregables |
| **TOTAL** | **660 hrs** | Equipo multidisciplinario iTechDev |`},
			{Key: "inversion", Title: "5. Inversión", SortOrder: 50, ContentMD: `Modalidad **precio cerrado (fixed price)** por el alcance descrito en la sección 2. Pesos mexicanos sin IVA.

| Concepto | Modalidad | Monto (MXN) |
|---|---|---|
| Desarrollo solución completa: RPA UiPath, ABAP, SAP MM, Fiori GR Monitor, pruebas y go-live (660 hrs) | Precio cerrado | $1,250,000 |
| **TOTAL DEL PROYECTO** | Pago único | **$1,250,000 MXN** |

### 5.1 Condiciones comerciales
- Moneda: Pesos mexicanos (MXN)
- Sin IVA ni retenciones
- Vigencia: 30 días naturales
- Modalidad: precio cerrado por alcance descrito
- Cualquier requerimiento fuera de alcance: orden de cambio formal
- **Garantía: 30 días naturales post go-live** para corrección de defectos`},
			{Key: "premisas", Title: "6. Premisas y Responsabilidades del Cliente", SortOrder: 60, ContentMD: `### 6.1 Premisas técnicas
- Mercedes-Benz México **provee un ambiente UiPath funcional** con licencias suficientes para la operación del robot
- Mercedes-Benz México provee una **cuenta de correo institucional** dedicada al monitoreo de las facturas XML del fletero
- iTechDev contará con accesos a los ambientes de desarrollo, QA y productivo de SAP
- **Disponibilidad de XML reales de muestra desde el inicio** del proyecto para construir las reglas de extracción

### 6.2 Recursos del cliente
- Sponsor ejecutivo del proyecto
- Project Manager por parte del cliente como contraparte
- Líder funcional de **SAP MM** disponible para validaciones, definición de reglas de negocio y workshops
- Líder funcional de **Cuentas por Pagar** para validar el flujo posterior al GR
- Equipo de TI / SAP Basis para gestión de accesos y conectividad
- Equipo de TI responsable del ambiente UiPath
- Disponibilidad de usuarios clave para sesiones de UAT y capacitación`},
			{Key: "siguientes-pasos", Title: "7. Próximos Pasos", SortOrder: 70, ContentMD: `1. Confirmación formal de aceptación de la presente propuesta
2. Firma del contrato marco y NDA correspondientes
3. Designación de los equipos de trabajo de ambas partes
4. **Validación de prerrequisitos técnicos** (UiPath, accesos SAP)
5. Kickoff oficial del proyecto`},
		},
	}
}
