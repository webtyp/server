---
PLAN: "fix: StartServer no llama al logger con strategyMu tomado (deadlock con la TUI de webtyp dev)"
EXECUTOR: local (executed 2026-10-12)
REVIEWER: none
---

# Plan — `webtyp.com/server`: ningún callback externo con `strategyMu` tomado

## 0. Síntoma

`webtyp dev` en `veltylabs/modules/appointment_booking` queda congelado de forma
intermitente (13:42 y 14:04 del 2026-10-12; a las 13:50 arrancó bien). El log
termina en:

```
MCP  Project loaded (build OK) — waiting for the dev server on port 8080
MCP  stage server-boot FAILED after 5s: timeout: nothing accepted a connection on port 8080 within 5s
MCP    ↳ the reason is in the SERVER section above …
```

La sección SERVER está vacía: el servidor nunca escribe ni su primera línea
(`Internal mode: …`). El diagnóstico manda a leer un error que no existe.
`item_catalog` "funciona" solo por timing.

## 1. Causa (verificada con `dlv attach` al daemon v0.6.139, server v0.2.77)

Deadlock por orden de locks entre dos goroutines:

| Goroutine | Tiene | Espera | Pila |
|---|---|---|---|
| 196 — arranque | `ServerHandler.strategyMu` (Lock) | mutex de `HeadlessTUI` | `StartServer` (management.go:45) → `h.log` → `onLog` → `HeadlessTUI.RelayLog` (`t.mu.Lock`) |
| 73 — `GET /webtyp/state` (la TUI de `webtyp dev` sondea) | mutex de `HeadlessTUI` (RLock) | `strategyMu` (RLock) | `HeadlessTUI.GetHandlerStates` → `tui.Extract` → `ServerHandler.Value` (tui.go:18) |

`StartServer` toma `strategyMu.Lock()` en management.go:33 y, **sin soltarlo**,
llama a `h.log(...)` (líneas 40, 42, 45). `h.log` ejecuta `onLog`, un callback
ajeno al paquete, que puede bloquear o volver a entrar al `ServerHandler`.

Se dispara solo cuando la TUI sondea el estado en la misma ventana en que
arranca el servidor. Por eso `start_development` por MCP (sin TUI que sondee)
siempre arranca, y `webtyp dev` a veces no.

### Mismo defecto en otros sitios del paquete

- `switch_mode.go` `CreateTemplateServer`: llama a `h.log` con el lock tomado
  (líneas 15, 19, 25, 30) **y** retorna en las ramas de error de las líneas
  21–22 y 26–27 **sin `Unlock`**: si falla `Stop()` o `ensureServerMain`, el
  lock queda tomado para siempre y el próximo `Value()`/`StartServer` cuelga.
- `server.go` `SetExternalServerMode`: `defer Unlock` y `h.log` adentro
  (líneas 291, 299…).
- `management.go` `Restart`: `h.log` bajo `RLock` (línea 107). Con un escritor
  esperando, `RWMutex` bloquea a los lectores nuevos y cierra el mismo ciclo.

Regla que se viola: **nunca invocar código ajeno (`onLog`, `strategy.Start/Stop`,
que también loguean) mientras se tiene `strategyMu`**. Se toma el lock solo
para leer o escribir los campos (`strategy`, `executionInternal`) y se suelta
antes de loguear o actuar.

## 2. Por qué los tests actuales no lo detectan

Todos los tests configuran un logger inerte (`t.Log` o append a un slice) que
nunca vuelve a entrar al handler. Ningún test reproduce lo que hace la TUI real:
leer `Value()` desde el callback de log o en paralelo con el arranque.

## 3. Etapas

| # | Etapa | Archivos | Commit |
|---|---|---|---|
| 1 | Test en rojo | `tests/log_reentrancy_test.go` (nuevo) | `test: logger re-entering the handler during StartServer must not deadlock` |
| 2 | Arreglo | `management.go`, `switch_mode.go`, `server.go` | `fix: never call the logger or the strategy while holding strategyMu` |

### Etapa 1 — `tests/log_reentrancy_test.go` (`package tests`, solo API pública)

1. `TestStartServerLoggerMayReadValue`: `server.New()`, `SetAppRootDir(t.TempDir())`
   (sin `config/routes.go` → modo interno), `SetPort` con un puerto libre,
   `SetExitChan(exit)`, y
   `SetLogger(func(...any) { _ = h.Value() })`, que es justo lo que hace la TUI.
   Lanza `go h.StartServer(&wg)` y espera hasta 3 s a que el puerto acepte
   conexiones. Hoy **cuelga**: `Value()` pide `RLock` en la misma goroutine que
   tiene `Lock`. El test falla por timeout con el mensaje
   `StartServer deadlocked: logger re-entered Value() while strategyMu was held`.
   Al final cierra `exit` y espera `wg`.
2. `TestStartServerConcurrentValue`: logger inerte; una goroutine llama a
   `h.Value()` en bucle durante el arranque. El puerto debe responder antes de 3 s.

Correr `gotest`: el test 1 debe fallar (rojo) antes de la etapa 2.

### Etapa 2 — soltar el lock antes de loguear o actuar

**`management.go` `StartServer`:** dentro del lock solo se decide y se guardan
los datos en variables locales (`isInternal`, `strategy`, el mensaje y su ruta).
Se hace `Unlock` y **después** se llama a `h.log(...)`. La lógica de decisión no
cambia.

**`management.go` `Restart`:** copiar `h.strategy` bajo `RLock`, soltar, y luego
loguear y llamar a `Restart()` sobre la copia.

**`switch_mode.go` `CreateTemplateServer`:** con el lock solo leer
`executionInternal` y `h.strategy`. Soltar. Loguear, hacer `Stop()` y
`ensureServerMain(true)` fuera del lock. Volver a tomar el lock solo para
asignar `executionInternal = false` y `h.strategy = newExternalStrategy(h)`.
Ninguna rama de error puede retornar con el lock tomado.

**`server.go` `SetExternalServerMode`:** mismo patrón: leer y escribir los campos
bajo lock, y hacer `log`, `Stop()`, `Start()` y `ensureServerMain` fuera de él.

Comentario en `strategyMu` (server.go:66): `// protects strategy and executionInternal only — never call onLog or a strategy method while holding it`.

Correr `gotest`: `vet ✅, race ✅, tests ✅`, incluidos los dos tests nuevos.

## 4. Fuera de este plan (anotado, no se toca)

`webtyp/app` `HeadlessTUI.GetHandlerStates` (headless_tui.go:209) llama a
`tui.Extract(handler)`, código de cada handler, con `t.mu` tomado. Es el mismo
antipatrón del otro lado. Con este plan el ciclo ya se rompe; endurecer `app`
(copiar los handlers bajo lock y extraer fuera) queda como plan propio en
`webtyp/app` si lo apruebas.

## 5. Entrega

`gopush` en `webtyp/server`. Después, actualizar la dependencia en `webtyp/app`
y publicar una release de `webtyp` para que `webtyp update` instale un binario
corregido. Mientras tanto, si se congela: `webtyp stop` y `webtyp dev` de nuevo
(es una carrera, normalmente el segundo intento arranca).
