# CLAUDE_AGENT_NOTES

Notas para coordinación entre agentes que trabajan en branches paralelas
sobre `aria-core`. Cada agente debe agregar acá los TODOs cross-cutting que
otro agente o reviewer humano necesita aplicar al merge.

## Personal cockpit `/dashboard/me` (agente: a275e0b8)

### Sidebar entry pendiente

Cuando el sidebar layout esté disponible para edición segura, agregar
en `internal/cloud/dashboard/layout.templ` la siguiente entrada después
de `Inicio` y antes de `Guía`:

```templ
@SidebarLink("/dashboard/me", "🎯", "Mi actividad", "me", activeTab)
```

El `activeTab` value `"me"` ya viene seteado por
`handlePersonalCockpit` en el `Layout(...)`. No requiere cambios extra.

Si encontrás conflict con otra branch al merge, descartá esta línea y
abrí un follow-up commit dedicado al sidebar — el cockpit ya es
accesible directamente vía URL `/dashboard/me`.

### Wiring del servicio backend

El cockpit espera un `dashboard.PersonalCockpitService` inyectado en
`MountConfig.PersonalCockpit`. La implementación contra Postgres
(queries sobre `aria_sessions`, `aria_observations`, `aria_skills`,
`cotizador_*`) aún no existe — el handler degrada a vista vacía con
un Notice. Para habilitar el cockpit en producción:

1. Crear paquete `internal/cloud/personalcockpit/` con un `Service`
   que implemente la interfaz.
2. En `internal/cloud/cloudserver/cloudserver.go` agregar:
   - Campo `personalCockpit dashboard.PersonalCockpitService` en `CloudServer`.
   - `WithPersonalCockpit(...)` Option.
   - `GetUID: func(r *http.Request) string { ... }` que retorne `claims.UID`
     desde `dashboardClaimsFromRequest(r)` en el `MountConfig`.
   - `PersonalCockpit: s.personalCockpit` en el `MountConfig`.
3. En `cmd/aria-core/cloud.go` (o el wiring del binario): instanciar
   el servicio con la `*sql.DB` y pasarlo vía `WithPersonalCockpit`.

Sin esos pasos, `/dashboard/me` muestra una vista degradada con
"Cockpit personal aún no configurado en el servidor."
