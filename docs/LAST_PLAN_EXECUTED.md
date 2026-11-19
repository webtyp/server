---
PLAN: "feat: enforce strict CSP by default, load csp.json hashes and mount CSP violation report endpoint"
EXECUTOR: jules
REVIEWER: none
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit, push and open the pull request. Never ask whether to proceed or finalize — nobody answers, and a session that waits ends without your work.

# Plan: `server/httpd` — CSP estricta siempre activa y endpoint de reportes `csp-violation`

Master: `SECURITY_OBSERVABILITY_MASTER_PLAN.md` (D3, O3b-9).
**Prerrequisitos:** `router/security` (O3b-7) y `sitec` (O3b-8).

## Tools

```bash
export GOPRIVATE=webtyp.com/*,github.com/veltylabs/*
export GOFLAGS=-mod=mod
go install webtyp.com/devflow/cmd/gotest@latest
go install webtyp.com/lang/cmd/langc@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Ecosystem search

- Buscado en `~/Dev/Project/webtyp` (`server/httpd`, `router/security`, `securityevent`, `sitec`).
- Hoy nadie instala `router/security.Policy.Middleware()` por defecto en `server/httpd`, y no existe endpoint para recibir reportes CSP del navegador (`ContentBlocked`).
- `webtyp.com/securityevent` (`v0.0.2`) ya define `securityevent.ContentBlocked` (`malicious_content_blocked`) y `httpd.Config.Report` ya está conectado desde `appkit`.
- **Decisión:** instalar `router/security.Policy` siempre activo en `server/httpd`, leer `csp.json` desde `PublicDir`, y montar la ruta pública `POST /_csp-report` que reporta `securityevent.ContentBlocked`.

## Design gate

1. **Prior art:**
   - [MDN — CSP `report-to`](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/report-to): el navegador envía un `POST` con `Content-Type: application/reports+json` (`[{"type":"csp-violation","body":{"blockedURL":"...","effectiveDirective":"..."}}]`) a la ruta declarada en `Reporting-Endpoints`.
   - [W3C Reporting API](https://www.w3.org/TR/reporting-1/) + formato legado `application/csp-report` (`{"csp-report":{"blocked-uri":"...","violated-directive":"..."}}`): aceptar ambos cubre Chromium, Firefox y Safari.
   - [Helmet.js](https://helmetjs.github.io/) / [Django SecurityMiddleware](https://docs.djangoproject.com/en/stable/ref/middleware/#module-django.middleware.security): las cabeceras de seguridad se aplican a toda respuesta desde la capa del servidor, no por ruta.
2. **Novice-name test:**
   - `httpd.CSPReportPath = "/_csp-report"` — ruta reservada del framework (junto a `HealthPath = "/health"`).
   - `httpd.Config.SecurityPolicy security.Policy` — opcional; su valor cero es la política endurecida (las apps solo agregan excepciones explícitas con `.AllowImages(...)`, etc., nunca pueden desactivar la CSP).
3. **Complexity ledger:**
   - Conceptos a aprender: `0` (una app estándar no configura nada; las cabeceras y el endpoint funcionan automáticamente)
   - Archivos a tocar en apps: `0`
   - Líneas en el call site: `0`
   - Formas de hacer lo mismo: `0` (un único middleware `router/security.Policy` aplicado globalmente).
4. **Where it belongs:** `server/httpd` es el transporte HTTP que sirve `PublicDir` y posee `Config.Report`. Leer `csp.json` y montar `/_csp-report` aquí garantiza que toda app webtyp reciba protección y telemetría CSP sin escribir una sola línea.
5. **What does this change delete?** Elimina el estado inseguro de respuestas servidas sin cabeceras `Content-Security-Policy` ni límite 413.

## APIs used

| Symbol | Exact signature | Tag | File |
|---|---|---|---|
| `security.Policy` | `type Policy struct { ... }` | *(O3b-7)* | `webtyp.com/router/security` |
| `(security.Policy).ScriptHashes` | `func (p Policy) ScriptHashes(hashes ...string) Policy` | *(O3b-7)* | `webtyp.com/router/security` |
| `(security.Policy).ReportEndpoint` | `func (p Policy) ReportEndpoint(path string) Policy` | *(O3b-7)* | `webtyp.com/router/security` |
| `(security.Policy).WithReporter` | `func (p Policy) WithReporter(r securityevent.Reporter) Policy` | *(O3b-7)* | `webtyp.com/router/security` |
| `(security.Policy).Middleware` | `func (p Policy) Middleware() router.Middleware` | *(O3b-7)* | `webtyp.com/router/security` |
| `securityevent.ContentBlocked` | `const ContentBlocked Kind` | `v0.0.2` | `webtyp.com/securityevent` |
| `(securityevent.Reporter).Report` | `func (r Reporter) Report(e Event) error` | `v0.0.2` | `webtyp.com/securityevent` |

If a signature in the published tag differs from this table, stop and name the symbol in the PR; never adapt it by guessing.

## Reglas de entrega y anti-footguns

- `server/httpd` es código de servidor (`!wasm`): stdlib `encoding/json`, `io`, `net/http`, `os` son legítimas aquí.
- El endpoint `/_csp-report` debe limitar la lectura del cuerpo (p. ej. 64 KiB con `io.LimitReader`) y responder `204 No Content` incluso ante JSON malformado (sin filtrar detalles internos).
- `PublicDir` puede regenerar `csp.json` durante desarrollo (`webtyp dev`): leer `csp.json` con comprobación de `ModTime` (o en caliente cuando cambia) para no servir hashes obsoletos tras un hot-reload.
- Hacer commit después de cada etapa. No usar `git reset`, `git restore`, `git checkout -- <file>` ni `git stash`.

---

## Etapa 1: Diagrama y pruebas (TDD)

1. Crear `docs/diagrams/CSP_ENFORCEMENT.md` (contexto, `flowchart TD`, walkthrough numerado):
   - Toda respuesta pasa por `security.Policy` (con hashes de `PublicDir/csp.json`, `ReportEndpoint("/_csp-report")` y `WithReporter(s.config.Report)`).
   - `POST /_csp-report` acepta `application/reports+json` (array) o `application/csp-report` (objeto) → por cada violación llama `s.config.Report.Report(securityevent.Event{Kind: securityevent.ContentBlocked, IP: ..., Resource: blockedURL, Reason: securityevent.Reason(effectiveDirective)})` → responde `204 No Content`.
   - Cuerpo inválido o vacío → responde `204 No Content` sin publicar evento.
2. Crear `httpd/csp_report_test.go` (y registrar en `docs/TESTS.md` bajo `| Test | Rule |`):
   - `TestHTTPD_AlwaysEmitsStrictCSP`: toda respuesta (ruta API y archivo en `PublicDir`) lleva `Content-Security-Policy` con `require-trusted-types-for 'script'`, `report-to csp` y `Reporting-Endpoints: csp="/_csp-report"`.
   - `TestHTTPD_LoadsScriptHashesFromCSPJSON`: si `PublicDir/csp.json` tiene `{"script_hashes":["sha384-xyz"]}`, la CSP emitida contiene `'sha384-xyz' 'strict-dynamic' 'wasm-unsafe-eval'`.
   - `TestHTTPD_CSPReportEndpoint_ReportsV1`: `POST /_csp-report` con `application/reports+json` publica `securityevent.ContentBlocked` con `Resource` y `Reason` exactos y responde `204`.
   - `TestHTTPD_CSPReportEndpoint_LegacyFormat`: `POST /_csp-report` con `application/csp-report` publica `securityevent.ContentBlocked` y responde `204`.
   - `TestHTTPD_CSPReportEndpoint_MalformedBody`: `POST /_csp-report` con JSON roto responde `204` y publica 0 eventos.

## Etapa 2: Implementar `httpd/csp.go` y cableado en `httpd/httpd.go` y `httpd/static.go`

1. En `httpd/httpd.go`:
   - Agregar `const CSPReportPath = "/_csp-report"`.
   - Agregar campo `SecurityPolicy security.Policy` a `Config`.
   - En `base()`: montar `POST /_csp-report` en `s.mux` (público, manejado por `s.handleCSPReport`).
2. Crear `httpd/csp.go`:
   - Lector cacheado por `ModTime` de `filepath.Join(s.config.PublicDir, "csp.json")` → `[]string` de `script_hashes`.
   - `func (s *Server) effectiveSecurityPolicy() security.Policy`: parte de `s.config.SecurityPolicy`, aplica `.ReportEndpoint(CSPReportPath)`, añade `.ScriptHashes(hashes...)` si existen, y `.WithReporter(s.config.Report)` si el reporter está inicializado.
   - `func (s *Server) handleCSPReport(w http.ResponseWriter, r *http.Request)`:
     - Acepta solo `POST` (si no, `405`).
     - Lee hasta `64 << 10` bytes.
     - Intenta decodificar `[]struct{ Type string; Body struct{ BlockedURL, EffectiveDirective string } }` (Reporting API v1, filtrando `type == "csp-violation"`).
     - Si no coincide, intenta decodificar `struct{ CSPReport struct{ BlockedURI, ViolatedDirective string } \`json:"csp-report"\` }` (formato legado).
     - Reporta cada violación con `securityevent.Event{Kind: securityevent.ContentBlocked, IP: clientIP(r), Resource: blocked, Reason: securityevent.Reason(directive)}` (truncando longitudes a máx. 256 chars para evitar abuso de logs).
     - Responde `http.StatusNoContent`.
3. En `httpd/static.go` (`wrapWithGlobalBatteries`):
   - Aplicar `s.effectiveSecurityPolicy().Middleware()` envolviendo `h` para que TODA respuesta (API, estáticos y 404) reciba las cabeceras de seguridad y el tope 413.

## Etapa 3: Traducciones, verificación y documentación

1. Ejecutar `langc sync .` si se tocaron textos traducibles.
2. Actualizar `README.md` (≤ 150 líneas, 4 secciones obligatorias), `docs/ARCHITECTURE.md` (4 secciones) y `docs/TESTS.md`.
3. Ejecutar `gotest`.

## Tabla de etapas

| Etapa | Archivos | Qué |
|---|---|---|
| 1 | `docs/diagrams/CSP_ENFORCEMENT.md`, `httpd/csp_report_test.go`, `docs/TESTS.md` | Diagrama y pruebas TDD |
| 2 | `httpd/csp.go`, `httpd/httpd.go`, `httpd/static.go` | Política CSP siempre activa, lector `csp.json` y endpoint `/_csp-report` |
| 3 | `README.md`, `docs/ARCHITECTURE.md`, `docs/TESTS.md` | Documentación y `gotest` verde |
