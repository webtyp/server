# PLAN — `/` con sesión debe llevar al shell (`/app/`)

## Problema

Un proyecto con páginas públicas **y** aplicación WASM tiene dos documentos
(`sitec`): la página pública en `/` (sin bootstrap wasm) y el shell en `/app/`.
`httpd/shell.go` ya cierra una mitad del reparto: `/app/` **sin** sesión →
`302 /`. La otra mitad falta: `/` **con** sesión se sirve igual, como un
archivo estático más, y **nunca consulta `Authn`**.

Síntoma real (mjosefa-cms, 2026-09-26): con `DEV_AUTOLOGIN` puesto, el navegador
abre `http://localhost:8080/`, recibe la pantalla de login estática (sin un solo
`<script>`) y ahí se queda. El autologin de `webtyp.com/auth` vive en el
middleware `Authn`, así que no se ejecuta nunca en esa petición:

```
curl -D - http://localhost:8080/app/  → 200 + Set-Cookie: session=…   (autologin OK)
curl -D - http://localhost:8080/      → 200, sin cookie                (Authn nunca corre)
```

El mismo hueco lo sufre un usuario real con sesión vigente: abre `/` (el
marcador, la URL que abre el daemon) y ve el formulario de login otra vez.

## Auditoría de tests — por qué la suite pasa con el bug

- `tests/shell_gate_test.go` → `TestShellIsClosedWithoutSession/"the public page
  stays public"` solo prueba `/` **anónimo**. Nadie prueba `/` con sesión.
- `webtyp.com/auth` prueba `DEV_AUTOLOGIN` contra el middleware en una ruta del
  router, nunca contra la página de aterrizaje que sirve el fallback estático de
  `httpd` — que es justo por donde entra el navegador.

Sonda (overlay, sin tocar el repo) — falla por el síntoma exacto:

```
GET / with session: status=200 Location="", want 302 /app/
```

## Decisión

Regla simétrica a `denyShellWithoutSession`, con las mismas tres condiciones de
activación y ninguna configuración nueva:

| Petición resuelve a | `Authn` configurado | ¿existe `<PublicDir>/app/index.html`? | Identidad | Respuesta |
|---|---|---|---|---|
| `<PublicDir>/app/index.html` | sí | — | no | `302 /` (ya existe) |
| `<PublicDir>/index.html` | sí | sí | **sí** | **`302 /app/`** (nuevo) |
| cualquier otro caso | | | | se sirve el archivo |

- Solo el `index.html` **raíz** de `PublicDir` (el que `shellFallbackPath`
  ya declara como aterrizaje sin sesión). Otras páginas públicas no se tocan.
- Sin shell en disco no hay adónde ir: `/` se sirve normal (sitio estático puro).
- Sin `Authn` no hay sesión que consultar: comportamiento actual intacto.
- Sin bucle posible: `/` y `/app/` consultan el mismo `Authn` con la misma
  cookie (`Path=/`).
- Costo: una resolución de sesión (en caché) por `GET /` solo en proyectos con
  shell + `Authn`.

**Fuera de alcance, a propósito:** un proyecto cuya `/` sea una landing de
marketing y que quiera mostrarla también a usuarios con sesión. Hoy ese caso no
existe en el ecosistema, y `shell.go` ya fija `/` como el aterrizaje sin sesión
cuando hay shell; si aparece, se resuelve moviendo el login fuera de `/` en
`sitec`, no con un flag aquí.

## Design gate

Sin símbolos exportados nuevos ni cambios de firma. Una constante privada nueva
(`shellPath = "/app/"`) junto a `shellFallbackPath`, con el mismo comentario de
acoplamiento con `sitec.ShellPath` / `auth.PathAfterLogin`.

## Etapas

| # | Qué | Archivo |
|---|---|---|
| 1 | **Prueba roja — ya escrita.** En `TestShellIsClosedWithoutSession`: (a) `"a session on the public page goes to the shell"` — `handlerFor(t, dir, true, "user-1")`, `GET /` → `302`, `Location: /app/`; (b) `"without a shell the root page stays for a session"` — `PublicDir` solo con `index.html`, `GET /` con sesión → `200`. Correr `gotest`: (a) debe fallar con `status=200`. | `tests/shell_gate_test.go` |
| 2 | Agregar `shellPath = "/app/"` y `isPublicLanding(absDir, fullPath) bool` (`index.html` raíz **y** existe `app/index.html`). Generalizar `denyShellWithoutSession` → `routeBySession(w, r, absDir, fullPath) bool`, que aplica las dos filas de la tabla. | `httpd/shell.go` |
| 3 | Cambiar la única llamada a `s.denyShellWithoutSession(...)` por `s.routeBySession(...)`. | `httpd/static.go` |
| 4 | `gotest` completo en verde → `gopush 'fix(httpd): / con sesión redirige al shell /app/'`. | — |
| 5 | Consumidor: en `veltylabs/mjosefa-cms`, `go get webtyp.com/server@<nuevo>`, `gotest`, reiniciar `webtyp dev` y verificar en el navegador que con `DEV_AUTOLOGIN` abrir `/` termina en `/app/` con sesión. | `mjosefa-cms/go.mod` |
