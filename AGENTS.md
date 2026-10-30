# AGENTS.md — `webtyp/server`

<!-- webtyp:base — copied from devskills skill agents-md; do not edit here -->
## Ecosystem rules — read before any change

`docs/PLAN.md` is the spec: no design judgment; anything unclear is a question in the PR, not a guess.

- Public API: typed (no `any`/`interface{}`), explicit, minimal; never export a symbol only a test uses.
- One intent, one path: the old path is deleted in the same change. No silent fallback, `TODO`, commented code or stub.
- Fail at compile time, else a loud diagnostic — never silently. Zero values deny.
- A defect in a dependency is reported, never wrapped or patched here.
- No string literals in logic: named constants. The same logic never lives in two repos: import the package the plan names; if you find an existing one the plan does not name that already does what you are about to write, say so in the PR instead of writing it.
- `cmd/*/main.go`: parse flags, inject, print/exit only. stdout = data, stderr = logs, exit codes are the contract.
- WASM code: `webtyp/fmt`, `webtyp/time`, `webtyp/json` instead of stdlib; no `map`; `dom.Element` embedded by value.
- `css.go`, `svg.go`, `js.go`, `html.go` carry `//go:build !wasm`; no `ssr.go`, no `front.go`.
- Build tags: `wasm`/`!wasm` only when the API does not exist on that platform; `browser`/`!browser` when an app's file belongs to another binary; a library carries no destination tag (the linker drops what a binary never calls). One main per folder, no destination tag: `web/browser/main.go` → `web/public/browser.wasm`, `web/server/main.go` → `web/server/server`, `web/edge/main.go` → `web/edge/edge.wasm`.
- Code and keys in English; UI text is `lang.Text`, translations live in `lang.json`. Comments and docs: English in `webtyp/*`; in `veltylabs/*` **every README, `docs/*` file, diagram and code comment is in Spanish**, written for a reader with little technical background, and each exported symbol's comment shows a short usage example.
- A translation key is reused, never duplicated as a near-identical phrase (`"not a valid integer"` + `"not a valid decimal number"` repeats `"not a valid"` three ways instead of composing it once) — every library ships to WASM/TinyGo, so each unreused phrase is dictionary rows AND binary bytes, in every consumer, forever. A token that doesn't need translating (a kind/type name like `int`) is passed as data (a variable), never as a string literal — a literal becomes a dictionary key whether or not it needed one.
- Tests cover behaviour and domain rules, never the look (CSS, colours, markup order, class strings). In `tests/` (external package), real stack with fakes only at the edge; run `gotest`.
- Docs (`README.md`, `docs/*`) updated with the API; permanent docs never link `docs/PLAN.md`.
- Before the PR: `git diff --stat main` lists every file of the plan's stages, and nothing the plan did not order is deleted.
<!-- /webtyp:base -->

## This repo

Restricciones de este repo para cualquier agente (humano o LLM) que edite código aquí.

## Ubicación de tests: todos en `tests/`

Todo archivo `_test.go` vive en la carpeta global `tests/`, en la raíz del repo,
como paquete `<módulo>_test` (caja negra, importando el paquete público). Está
prohibido esparcir archivos `_test.go` sueltos junto al código que prueban.

**Única excepción:** un test que necesita acceso a identificadores **no
exportados** del paquete que prueba (structs/funciones privadas, campos internos).
Ese test **debe** vivir junto al código, como `package <paquete>` (caja blanca) —
nunca en `tests/`, porque `tests/` es un paquete `_test` externo y no tiene acceso
a lo no exportado.

- Ejemplo ya presente: `handle_file_event_test.go` (raíz, `package server`) — usa
  `externalStrategy` y `ErrUnsupportedEvent`, no exportados.
- Cuando un subpaquete (p. ej. `server/httpd`) tenga tests de caja blanca sobre sus
  propios no-exportados (`httpRouter`, `httpContext`), esos tests viven en el
  subpaquete (`server/httpd/adapter_test.go`), no en `tests/` — la regla de "junto
  al código si necesita lo interno" aplica por paquete, no solo en la raíz del repo.
- Todo lo demás — tests de comportamiento observable desde la API pública,
  integración, blackbox — va en `tests/`, sin importar cuántos paquetes toque.

Antes de añadir un test nuevo: si compila usando solo identificadores exportados,
va en `tests/`. Si necesita un identificador no exportado, va junto al paquete que
lo define — y solo ahí.

## Seguridad y Archivos Estáticos

- Para servir un archivo individual, usa `PublicAsset(path, handler)`.
- Para servir un directorio completo como fallback, usa `PublicDir(prefix, dir)`.
- Para servir archivos con permisos, usa `Get(...).Requires(...)`.
- El acceso es UNA declaración (`model.Access`), no una combinación de banderas. El zero value es `AccessGuarded`: identidad y permiso. Una contradicción (guarded sin recurso, o recurso sin guarded) falla al arrancar, nunca en runtime. Un `Authorize` nil deniega.
- **PROHIBIDO:** El uso directo de `http.FileServer` o `http.Dir` fuera del sistema de rutas del router. Todo acceso a archivos debe estar declarado y pasar por la verja de seguridad del router.
