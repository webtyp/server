---
PLAN: "feat: Context.Decode acepta formularios HTML (x-www-form-urlencoded), no solo JSON"
EXECUTOR: jules
REVIEWER: none
STATUS: running
SESSION: 7890483313307634891
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — que un `<form>` HTML nativo pueda hablarle al servidor

## 1. El problema, reproducible en un comando

`httpd.Context.Decode` está clavado a JSON (`httpd/adapter.go:72`):

```go
func (c *httpContext) Decode(into model.Decodable) error {
	return json.Decode(c.Body(), into)
}
```

Cualquier handler escrito contra `router.Context` (todos: `webtyp.com/auth`,
los módulos de dominio, etc.) solo entiende JSON. Pero un `<form>` HTML
enviado por el navegador manda `application/x-www-form-urlencoded`. Contra un
servidor real, con el mismo dato y el mismo endpoint:

```
POST /session  Content-Type: application/x-www-form-urlencoded  code=12345678-5   → 400
POST /session  Content-Type: application/json  {"code":"12345678-5"}              → 302
```

Eso bloquea una propiedad que el ecosistema quiere tener: una página
renderizada en el servidor debe **funcionar antes de que cargue el wasm**. Hoy
se puede renderizar el formulario (`form.SetSSR(true)` emite `method`/`action`),
se ve perfecto, y al enviarlo devuelve 400. Es una fachada.

## 2. El cambio

`Decode` elige el decodificador según el `Content-Type` de la petición:

- `application/x-www-form-urlencoded` → decodifica el cuerpo como formulario.
- **cualquier otro caso, incluido `Content-Type` ausente o vacío → JSON**,
  exactamente como hoy. Esto no es una preferencia: hay clientes que postean
  JSON sin declarar el tipo, y romperlos sería una regresión silenciosa.

Comparar el tipo **por prefijo**, no por igualdad: el navegador manda
`application/x-www-form-urlencoded; charset=UTF-8`. Cortar en el primer `;` y
recortar espacios antes de comparar.

`webtyp/server` es backend puro (`net/http` ya está importado), así que
`net/url` y `strings` de la stdlib son válidos aquí. **No** aplica la
restricción de "sin stdlib" del código agnóstico/wasm.

## 3. El lector de formularios

`model.Decodable` se llena vía `DecodeFields(r model.FieldReader)`. Hay que
implementar un `FieldReader` sobre `url.Values`. La interfaz completa, que
hay que satisfacer entera (`webtyp.com/model/codec.go:41`):

```go
type FieldReader interface {
	String(name string) (string, bool)
	Int(name string) (int64, bool)
	Float(name string) (float64, bool)
	Bool(name string) (bool, bool)
	Bytes(name string) ([]byte, bool)
	Object(name string, into Decodable) bool
	Array(name string) (ArrayReader, bool)
	Raw(name string) (string, bool)
}
```

Semántica exacta de cada método sobre `url.Values` (un formulario HTML es
plano: solo pares nombre→texto):

| Método | Comportamiento |
|---|---|
| `String` | el valor tal cual; `false` si la clave no está presente |
| `Raw` | idéntico a `String` |
| `Int` | parsear el texto como entero; si no parsea → `(0, false)` |
| `Float` | parsear como flotante; si no parsea → `(0, false)` |
| `Bool` | `true` para `"true"`, `"on"`, `"1"` (un checkbox HTML marcado manda `on`); `false` para `"false"`, `"off"`, `"0"`, `""`; cualquier otra cosa → `(false, false)` |
| `Bytes` | los bytes del texto; `false` si la clave no está |
| `Object` | **siempre `false`** — un formulario plano no anida |
| `Array` | **siempre `(nil, false)`** — ídem |

Clave ausente ⇒ el segundo retorno es `false` en todos los casos. Eso importa:
los `DecodeFields` generados hacen `if v, ok := r.String("x"); ok { ... }`, así
que un `false` deja el campo en su valor cero en vez de pisarlo.

Usar `url.ParseQuery` sobre el cuerpo. Si `ParseQuery` falla, devolver ese
error desde `Decode` (un cuerpo mal formado es un 400 legítimo, que es lo que
el handler ya hace con el error).

## 4. Archivos

- `httpd/adapter.go`: modificar `Decode` (línea 72).
- `httpd/formdecode.go` (**nuevo**): el `FieldReader` sobre `url.Values` y el
  helper que decide por `Content-Type`. Va en su propio archivo, no dentro de
  `adapter.go`, que ya es el archivo más grande del paquete.
- `httpd/formdecode_test.go` (**nuevo**): los tests de §5.

No tocar `Encode`: la respuesta sigue siendo JSON. Un formulario nativo
navega a la respuesta (el handler ya responde 302 + `Location` en el caso de
login), así que no hace falta negociar el tipo de salida en este plan.

## 5. Tests obligatorios

En `httpd/formdecode_test.go`, contra un modelo de prueba local que implemente
`model.Decodable` con un campo string, uno int y uno bool:

1. **Formulario se decodifica**: `Content-Type: application/x-www-form-urlencoded`
   con `code=abc&qty=3&active=on` llena los tres campos.
2. **El charset no rompe la detección**:
   `application/x-www-form-urlencoded; charset=UTF-8` se comporta igual que el
   caso 1. (Es lo que manda el navegador de verdad.)
3. **JSON sigue funcionando**: `Content-Type: application/json` con
   `{"code":"abc"}` decodifica como siempre.
4. **Sin `Content-Type` ⇒ JSON**: mismo cuerpo JSON, sin cabecera, decodifica
   igual. Protege la regresión descrita en §2.
5. **Clave ausente no pisa**: un formulario con solo `code=abc` deja `qty` en
   0 y `active` en false, y `Int`/`Bool` devuelven `false` como segundo valor.
6. **Checkbox**: `active=on` → `true`; `active=` (vacío) → `false`; un valor
   basura (`active=banana`) → el segundo retorno es `false`.
7. **Cuerpo mal formado**: un cuerpo que `url.ParseQuery` rechaza hace que
   `Decode` devuelva error (no un panic, no un silencio).

## 6. Criterios de aceptación

1. `go build ./...`, `go vet ./...` y `gotest ./...` en verde.
2. `grep -n "json.Decode" httpd/adapter.go` → el `Decode` ya no llama a
   `json.Decode` directamente; la elección vive en el helper nuevo.
3. `grep -rn "x-www-form-urlencoded" httpd/` → aparece **una sola vez** como
   constante nombrada, nunca como literal repetido en la lógica.
4. Los siete tests de §5 pasan.
5. Ningún cambio de firma pública: `Decode(into model.Decodable) error` sigue
   igual. Este plan no agrega API nueva al paquete — solo amplía qué cuerpos
   entiende la que ya existe.

| Etapa | Archivos | Acción |
|---|---|---|
| 1 | `httpd/formdecode.go` | `FieldReader` sobre `url.Values` + selección por Content-Type |
| 2 | `httpd/adapter.go` | `Decode` delega en el helper |
| 3 | `httpd/formdecode_test.go` | Los siete tests de §5 |
