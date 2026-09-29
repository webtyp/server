---
PLAN: "feat(httpd): httpStreamer.Done from the request context"
EXECUTOR: jules
REVIEWER: none
---

> Este plan se despacha con el flujo CodeJob. Ver skill: agents-workflow.
> Orden de la ola: 1. `webtyp/router` v0.2.0 (`Streamer.Done`) → **2. `webtyp/server` (este)** →
> 3. `webtyp/sse`. **Espera el tag v0.2.0 de `webtyp.com/router`**: sin él este repo no compila contra la
> interfaz nueva.

# Plan — `httpd` implementa `Streamer.Done()`

## 0. Contexto

`webtyp.com/router` v0.2.0 agrega a `router.Streamer`:
```go
Done() <-chan struct{} // se cierra cuando el cliente se desconecta o el servidor cierra la conexión
```
El único `Streamer` de producción es `httpStreamer` (`httpd/adapter.go:162`), que envuelve un
`*httpContext` con `r *http.Request`. Por qué importa: `webtyp/sse` necesita esa señal para que un
stream termine cuando la pestaña se cierra (hoy la gorutina queda viva hasta el próximo mensaje al canal).

## Etapas

1. `go get webtyp.com/router@v0.2.0` + `go mod tidy`.
2. `httpd/adapter.go`:
   ```go
   // Done is closed when the client goes away or the server shuts down: it is
   // the request's own context, which net/http cancels in both cases.
   func (s *httpStreamer) Done() <-chan struct{} { return s.r.Context().Done() }
   ```
   y `var _ router.Streamer = (*httpStreamer)(nil)` junto al tipo.
3. Test en `httpd/adapter_test.go` (o el archivo de tests de streams que ya exista), con **servidor real**
   (`httptest.NewServer` sobre el router de `httpd`) y un cliente real:
   - Ruta `r.Stream("/s", h).Public()` cuyo `h` escribe una línea, hace `Flush()`, y luego
     `<-st.Done()`; al salir cierra un canal `returned`.
   - El cliente hace `GET /s` con un `context.WithCancel`, lee la primera línea, cancela.
   - Aserción: `returned` se cierra en menos de 2 s (`select` con `time.After`), y `ts.Close()` retorna.
   Antes de este cambio el test no compila (falta `Done`): ese es su estado rojo aceptable.

## Criterios de aceptación

```bash
gotest     # verde, incluido el test nuevo
```
