# Auditoría de QA — Tanda 1

Auditoría transversal de las pruebas de Cotiza. El sistema llegó a 154 pruebas
creciendo feature por feature, sin una revisión que cruzara todos los handlers
contra las mismas dimensiones. Este documento es el resultado de esa revisión:
qué estaba cubierto, qué no, qué se agregó y qué quedó deliberadamente afuera.

**Alcance de esta tanda:** 9 dimensiones (Unit, Integration, API/Endpoint,
Auth & Authorization, Business Rules, Error Handling, Database Integrity,
Transactions & Rollback, Audit Trail).
**Fuera de alcance (Tandas 2 y 3):** Performance/Load, concurrencia a fondo
(acá solo se corre con `-race`), Idempotency.

## Cómo correr la suite

```bash
docker compose up -d postgres
cd api-go
export DATABASE_URL="postgres://cotiza_admin:changeme@localhost:5432/cotiza?sslmode=disable"
go test ./... -race -v
```

Dos aclaraciones que importan para leer los resultados:

- `setupTestDB` (helpers_test.go) hace `t.Skip` si `DATABASE_URL` no está
  seteada. Sin Postgres arriba, `go test ./...` "pasa" sin probar nada.
- Las pruebas de `cmd/server` y las unitarias puras agregadas en esta tanda
  **no** necesitan base: corren siempre.

## Punto de partida (antes de esta tanda)

154 pruebas, todas de integración contra Postgres real, distribuidas así:

| Handler | Pruebas | Archivo |
|---|---:|---|
| cotizaciones.go | 21 | cotizaciones_test.go |
| usuarios.go | 20 | usuarios_test.go |
| catalogos.go | 15 | catalogos_test.go |
| solicitudes.go | 14 | solicitudes_test.go |
| reglas.go | 13 | reglas_test.go |
| auth.go | 10 | auth_test.go |
| plantillas.go (+estructura/vinculaciones/estilo) | 8 | plantillas_test.go |
| integraciones.go | 8 | integraciones_test.go |
| reportes.go | 6 | reportes_test.go |
| solicitudes_externas.go | 6 | solicitudes_externas_test.go |
| enlaces_publicos.go | 5 | enlaces_publicos_test.go |
| middleware (sesión + api-key) | 10 | middleware/*_test.go |
| compilador.go | 4 | compilador_test.go |
| cotizador_runtime.go | 4 | cotizador_runtime_test.go |
| cotizador_tabs.go | 4 | cotizador_tabs_test.go |
| dashboard.go | 4 | dashboard_test.go |
| calculadoras.go | 2 | calculadoras_test.go |
| **clientes.go** | **0** | — |
| **health.go** | **0** | — |

## Matriz handler × dimensión (estado inicial)

`✅` cubierto · `◐` parcial · `❌` sin cobertura · `—` no aplica

| Handler | 1 Unit | 2 Integr. | 3 API | 4 Auth | 5 Reglas | 6 Errores | 7 BD | 8 Tx | 9 Audit |
|---|---|---|---|---|---|---|---|---|---|
| auth.go | ❌ | ✅ | ✅ | ✅ | ✅ | ◐ | ❌ | — | — |
| usuarios.go | ❌ | ✅ | ✅ | ✅ | ✅ | ◐ | ◐ | — | — |
| clientes.go | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | — | — |
| integraciones.go | ❌ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ | — | — |
| solicitudes.go | ❌ | ✅ | ✅ | ❌ | ◐ | ❌ | ❌ | ❌ | — |
| solicitudes_externas.go | ❌ | ✅ | ✅ | ✅ | ✅ | ◐ | ❌ | — | — |
| cotizaciones.go | ❌ | ✅ | ✅ | ❌ | ◐ | ❌ | ❌ | ❌ | ◐ |
| cotizador_runtime.go | ❌ | ✅ | ◐ | ❌ | ◐ | ❌ | ❌ | ❌ | ✅ |
| cotizador_tabs.go | ❌ | ✅ | ◐ | ❌ | ◐ | ❌ | ❌ | ❌ | — |
| compilador.go | ❌ | ✅ | ◐ | ❌ | ◐ | ❌ | ◐ | ❌ | — |
| enlaces_publicos.go | ❌ | ✅ | ✅ | ◐ | ✅ | ❌ | ❌ | ❌ | ◐ |
| catalogos.go | ❌ | ✅ | ✅ | ❌ | ✅ | ❌ | ❌ | ❌ | — |
| reglas.go | ❌ | ✅ | ✅ | ❌ | ✅ | ◐ | ✅ | — | — |
| plantillas.go | ❌ | ✅ | ◐ | ❌ | ✅ | ❌ | ❌ | ❌ | — |
| plantilla_estructura.go | ❌ | ✅ | ◐ | ❌ | ◐ | ❌ | ❌ | ❌ | — |
| plantilla_vinculaciones.go | ❌ | ✅ | ◐ | ❌ | ◐ | ❌ | ❌ | — | — |
| plantilla_estilo.go | ❌ | ◐ | ◐ | ❌ | ❌ | ❌ | ❌ | — | — |
| reportes.go | ❌ | ✅ | ✅ | ❌ | ✅ | ◐ | — | — | — |
| dashboard.go | ❌ | ✅ | ◐ | ❌ | ✅ | ❌ | — | — | — |
| calculadoras.go | ❌ | ✅ | ✅ | ❌ | ✅ | — | — | — | — |
| health.go | ❌ | ❌ | ❌ | — | — | — | ❌ | — | — |

Lectura rápida: las columnas 2 (Integration), 3 (API) y 5 (Business Rules)
estaban razonablemente atendidas. Las columnas 1 (Unit), 4 (Auth a nivel de
ruta), 7 (BD) y 8 (Transactions) estaban prácticamente vacías en todo el
proyecto, y son justamente las que esta tanda ataca.

## Hallazgos por dimensión

### 1. Unit — casi inexistente por diseño accidental

Los handlers reciben `*pgxpool.Pool` directo, sin interfaz intermedia, así que
todo terminó siendo integración. Pero hay bastante **lógica pura** que no
necesita base y no tenía una sola prueba: `formatoMontoCSV`,
`validarFechaReporte`, `textoReporte` (reportes.go), `versionOpcional`
(cotizador_runtime.go), `normalizarIDs`, `enteroFlexible.UnmarshalJSON`
(catalogos.go), `generarUsuarioID`, `comoViolacionUnica`, `patronCorreoValido`
(usuarios.go), `generarToken` (auth.go), `tokenDesdeHeader`
(middleware/auth.go), `normalizarOpcionEstilo` (plantilla_estilo.go),
`validarOrdenCompleto` y `decodificarOrden` (plantilla_estructura.go),
`normalizarConfiguracionElemento` (cotizador_tabs.go), `fechaLocalCotiza` y los
mapas de estados (cotizaciones.go).

### 2. Integration — la dimensión más fuerte

Es lo que el proyecto venía haciendo. No se reescribió nada acá.

### 3. API/Endpoint — buena en el camino feliz, floja en el borde

Casi todos los endpoints tienen prueba de entrada válida. Lo que faltaba son
los 404 por id inexistente (integraciones.Editar, solicitudes.Detalle/
CambiarEstado/Convertir, plantillas.Detalle/Editar/Publicar, las tres bajas de
catálogos, secciones y bloques) y los 400 por parámetros de filtro inválidos.

### 4. Auth & Authorization — el hueco más grave

Dos hallazgos:

1. **Ningún test verificaba el wiring del router.** El árbol de rutas se arma
   dentro de `func main()`, que no es invocable desde un test, así que las 66
   rutas protegidas no tenían ninguna prueba de rechazo sin credenciales a
   nivel de ruta. Los tests de handler inyectan el actor a mano con `conActor`
   y llaman al handler directo: el middleware nunca participa. Una ruta
   registrada por error fuera del `r.Group` protegido no la detectaba nada.
2. **Endpoints sin gate de rol que quizá deberían tenerlo:** `GET /api/usuarios`
   y `GET /api/roles` permiten que un Vendedor liste correos y roles de todos.
   No es un bug confirmado —puede ser deliberado— pero no está documentado ni
   probado en ningún sentido. Ver "Pendientes de decisión" abajo.

### 5. Business Rules — reglas centrales sin prueba

Las que más pesan, todas sin cobertura antes de esta tanda:

- **El compilado se fija en la primera apertura del runtime y no cambia si
  alguien recompila después** — es la regla central del Sprint 4.
- Razón social separada del nombre comercial (feature del sprint anterior),
  tanto al crear cotización como al convertir una solicitud.
- `cotizaciones.estado` se sincroniza solo si la versión que cambia es
  `version_actual`.
- Ganada usa la `version_aceptada` del body y valida que exista.
- `puede_editar` en falso para estados terminales.
- Una solicitud **Descartada** no se puede convertir (la Convertida sí estaba).
- El compilador no compila si la validación falla.
- Cliente inactivo y cotizador inexistente rechazados al crear cotización.

### 6. Error Handling — dos bugs reales encontrados

Solo `login` tenía prueba de JSON malformado. Al probar entradas basura
aparecieron **dos bugs de verdad** (ver "Bugs encontrados").

### 7. Database Integrity — 26 CHECK y ~60 FK, 2 con prueba

Solo `reglas.severidad` y `reglas.momento` tenían prueba de que la BD rechaza
el valor inválido. Sin cobertura: los CHECK de estado de `cotizaciones`,
`cotizacion_versiones`, `plantillas`, `solicitudes`, `integraciones_api`,
`cotizadores_compilados`, los rangos (`columna BETWEEN 0 AND 3`, `orden >= 0`,
`version > 0`), los UNIQUE (incluido **`uq_cotizadores_compilados_activa`**, el
índice parcial que garantiza una sola versión ACTIVA por cotizador — la regla
se probaba por el handler, nunca a nivel de esquema) y las cadenas
`ON DELETE CASCADE` (plantilla → secciones → bloques → vinculaciones;
cotización → versiones/valores/historial; catálogo → valores → relaciones).

### 8. Transactions & Rollback — cero pruebas

`grep -l Rollback internal/handlers/*_test.go` no devolvía nada, con ~10
operaciones multi-tabla en producción: alta de cotización, nueva versión,
cambio de estado, conversión de solicitud, compilación, guardado de valores
del runtime, reemplazo de relaciones de catálogos, alta/edición de plantillas y
baja de tabs. Ninguna tenía prueba de que un fallo a mitad no dejara datos a
medias.

### 9. Audit Trail — el evento más nuevo sin cubrir

De los 6 tipos de `accion` que se escriben en `cotizacion_historial`, faltaba
`ENLACE_GENERADO` por completo (agregado el sprint pasado), y `CAMBIO_ESTADO`
estaba probado filtrando por `estado_nuevo` sin comprobar nunca la columna
`accion`. Tampoco había nada que verificara el `usuario_id` del historial: como
los tests llaman a los handlers sin middleware y algunos leen el actor de forma
tolerante (`usuarioID, _ := ...`), el rastro podía quedar en NULL sin que nadie
lo notara.

## Bugs encontrados y corregidos

La auditoría reveló **tres** bugs reales (dos de la dimensión 6 y uno que salió
de correr la suite con `-race`, como pedía la tanda). Como indica el
procedimiento, se documentan acá y se corrigieron aparte del trabajo de
pruebas, con el cambio mínimo y reusando lo que ya existía en el paquete.

### BUG-1 — `GET /api/cotizaciones/{id}?version=abc` devolvía 500

`versionSolicitada` entraba al SQL como `$2::int` sin validarse, así que el
error lo levantaba Postgres y salía como 500 genérico ("No fue posible
consultar la cotización.") en vez de un 400 explicando el problema.

**Arreglo** (`internal/handlers/cotizaciones.go`, `Detalle`): validar con
`versionOpcional`, el mismo helper que ya usaba el motor de ejecución, y
responder 400 antes de tocar la base.

### BUG-2 — `GET /api/cotizaciones?fecha_desde=basura` devolvía 500

Mismo patrón: `fecha_desde` entraba como `$5::date` sin validación previa.
`reportes.go` ya hacía esto bien con `validarFechaReporte`, devolviendo 400.

**Arreglo** (`internal/handlers/cotizaciones.go`, `Listar`): validar con
`validarFechaReporte(fechaDesde, "fecha_desde")` y responder 400.

Ambos comportamientos quedaron fijados por prueba (dimensión 6), así que si
alguien vuelve a meter un parámetro sin validar, el test lo marca.

### BUG-3 — el hash de bcrypt se comía el presupuesto de la base

Lo destapó el requisito de correr con `-race`: cuatro pruebas que ya existían
(`TestUsuariosCrear_AparecceEnElListado`, `TestUsuariosCrear_CorreoDuplicado`,
`TestUsuariosEditar_VendedorCambiaSoloSuPropioPin`,
`TestIntegracionesEditar_VendedorNoPuedeRevocar`) fallaban con
`context deadline exceeded` bajo el race detector y pasaban sin él.

No era un problema de las pruebas. `bcrypt` con costo 12 es trabajo de CPU de
cientos de milisegundos, **no acepta `context` y no se puede interrumpir**, y
en cuatro handlers corría *dentro* del `context.WithTimeout(..., 5s)` que
después se usaba para escribir en la base. Cuando el hash se llevaba el
presupuesto, a la operación de base no le quedaba margen y una petición
perfectamente válida terminaba en 500:

- `usuarios.Crear` — el INSERT del usuario.
- `usuarios.Editar` — el UPDATE del PIN y la relectura.
- `integraciones.Crear` — el INSERT de la integración.
- `auth.Login` — el sello de `ultimo_acceso` y el INSERT en `sesiones`, o sea
  **un login con credenciales correctas devolvía "No fue posible iniciar la
  sesión."**

El race detector solo lo hacía visible: en producción, la misma condición se
da en una máquina cargada, con CPU limitada (contenedor con `cpus` acotado) o
si algún día se sube el costo de bcrypt. Que el síntoma fuera un 500 en el
alta de usuarios y en el login lo vuelve serio.

**Arreglo** (`usuarios.go`, `integraciones.go`, `auth.go`): las operaciones de
base que van *después* del hash arrancan con su propio
`context.WithTimeout(r.Context(), 5*time.Second)`. El hash sigue siendo
igual de costoso —esa lentitud es deliberada y es lo que protege los PIN—
pero ya no se descuenta del tiempo reservado para escribir. No se cambió
ningún costo de bcrypt ni ninguna regla de negocio.

## Pendientes de decisión (no se cubrieron por diseño, no por olvido)

1. **`GET /api/usuarios` y `GET /api/roles` sin gate de rol — RESUELTO en
   Tanda 2.** Ambos exigen ahora rol Administrador, reusando el
   `obtenerSesion` que ya usaban `Crear`/`Editar` (`usuarios.go`); un
   Vendedor recibe 403 (`TestUsuariosListar_VendedorRecibe403`,
   `TestRoles_VendedorRecibe403`). Dos pantallas fuera de "Usuarios y
   Permisos" dependían de `GET /api/usuarios` para poblar un `<select>` de
   responsable sin ser Administrador: el filtro de Cotizaciones/Reportes/
   Dashboard (`cargarUsuariosActivos` en `cotiza_scripts.html`) y los campos
   Vendedor/Analista/Líder de Solicitudes (`cotiza_solicitudes.html`). Se
   agregó `GET /api/usuarios/activos` (sin gate de rol, cualquier sesión)
   devolviendo solo `usuario_id` + `nombre` de usuarios Activos —
   `TestUsuariosActivos_CualquierSesionPuedeListar` confirma que no expone
   correo/rol/estado y que un Vendedor puede llamarlo. El botón "Usuarios y
   Permisos" del sidebar no estaba oculto para no-admins (se confirmó
   revisando `cambiarSeccion`/el nav): se agregó el ocultamiento en
   `iniciarApp` como refuerzo cosmético, el 403 del backend es lo que
   realmente protege el dato.
2. **Permisos por rol a nivel de endpoint en general.** Sigue pendiente del
   Sprint 3 (ver CLAUDE.md): hoy los gates son ad hoc por handler
   (`integraciones.go`, `usuarios.go`) y no hay una capa común. La auditoría
   cubre lo que existe; no inventa la capa que falta.
3. **`plantillas.Publicar` es idempotente y no valida estilo ni
   vinculaciones.** Re-publicar una plantilla ya Publicada no da 409 y publicar
   sin estilo tampoco falla. Puede ser intencional; se documenta sin tocarlo
   (la idempotencia es materia de la Tanda 3).
4. **Inconsistencia menor en `clientes.Editar`:** `razon_social: ""` guarda
   cadena vacía mientras `Crear` normaliza a NULL. Se dejó una prueba que fija
   el comportamiento actual para que el día que se decida unificarlo, el cambio
   sea visible.
5. **Transacciones sin costura de fallo alcanzable.** Algunas transacciones
   validan todo antes del `BeginTx`, así que no hay forma de provocar un fallo
   a mitad sin modificar código de producción. Se listan en la sección de
   estado final; forzarlas con hacks (cerrar el pool, romper el esquema en
   caliente) daría pruebas frágiles y no se hizo.

## Estado final por dimensión

**154 → 278 pruebas** (124 funciones nuevas). Ninguna prueba existente se borró
ni se reescribió.

Corrida de cierre, sobre un stack recreado de cero
(`docker compose down -v && docker compose up --build`):

```
go test ./... -race -v
ok  cotiza/api/cmd/server         1.05s
ok  cotiza/api/internal/config    1.02s
ok  cotiza/api/internal/handlers  127.60s
ok  cotiza/api/internal/middleware 1.77s

602 casos PASS · 0 FAIL · 1 SKIP · 0 carreras de datos detectadas
```

El único SKIP es deliberado: `TestRutasProtegidas_RechazoRealSobreHTTP` se
saltea si no está `API_BASE_URL`, para no volver la suite dependiente de un
servidor corriendo. Corrida aparte contra el stack levantado, confirma los
**401 reales de las 66 rutas protegidas**:

```
API_BASE_URL=http://localhost:8080 go test ./cmd/server/ -run RechazoReal -v
66 subtests PASS
```

`go build ./...`, `go vet ./...` y `gofmt -l .` quedan limpios.

| # | Dimensión | Nuevas | Archivo | Estado |
|---|---|---:|---|---|
| 1 | Unit | 27 | `handlers/unitarias_test.go`, `middleware/unitarias_test.go`, `config/config_test.go` (4) | ✅ 20 funciones puras cubiertas |
| 2 | Integration | — | (ya existía) | ✅ sin cambios |
| 3 | API/Endpoint | 24 | `clientes_test.go` (22), `health_test.go` (2) | ✅ los dos handlers sin pruebas quedaron cubiertos |
| 4 | Auth & Authorization | 5 | `cmd/server/rutas_protegidas_test.go` | ✅ wiring de las 66 rutas protegidas |
| 5 | Business Rules | 17 | `reglas_negocio_test.go` | ✅ las 15 reglas pedidas |
| 6 | Error Handling | 9 (148 casos) | `errores_entrada_test.go` | ✅ 32 endpoints × 3 clases de entrada + 25 casos de 404 |
| 7 | Database Integrity | 21 | `integridad_bd_test.go` | ✅ 24/26 CHECK, 7/7 UNIQUE, 13 FK, 3 cadenas CASCADE |
| 8 | Transactions & Rollback | 8 | `transacciones_test.go` | ◐ 5 con rollback real, 3 documentadas como validación previa |
| 9 | Audit Trail | 9 | `auditoria_historial_test.go` | ✅ los 6 tipos de evento + no-duplicación |

### Detalle de lo que quedó cubierto

**Dimensión 4 (Auth).** `cmd/server/rutas_protegidas_test.go` parsea `main.go`
con `go/ast`, clasifica los 70 endpoints registrados y exige que toda ruta bajo
`/api` esté dentro del grupo con `RequiereSesion`, salvo una whitelist de 3
rutas públicas y 1 de api-key, cada una con su justificación escrita en el
código. Se eligió el análisis estático porque el router se arma dentro de
`main()` y no hay forma de instanciarlo desde un test sin refactorizar
producción, algo que esta tanda no debía hacer. La garantía se completa con las
10 pruebas de middleware que ya existían: **"el middleware rechaza" + "todas
las rutas pasan por el middleware"**. Incluye dos salvaguardas: una prueba que
audita al propio walker contra un conteo por fuerza bruta (para que no pase en
falso si alguien registra rutas dentro de un `if`), y otra que falla si la
whitelist queda con rutas fantasma. Hay además un smoke test que verifica los
401 reales sobre HTTP, que se activa con `API_BASE_URL=http://localhost:8080`
y se saltea por defecto para no volver la suite dependiente del servidor.

**Dimensión 1 (Unit).** Es la novedad estructural: hasta ahora, sin Postgres,
`go test ./...` no probaba nada. Las nuevas pruebas unitarias, las de `config`
y la auditoría de rutas **corren sin base de datos** (verificado con
`env -u DATABASE_URL`, 0 skips). Entre ellas hay dos que atan invariantes que
CLAUDE.md pedía mantener a mano: que `estadosCotizacionValidos` coincida con el
CHECK de `0007_cotizaciones_shell.sql` (leyendo el `.sql`), y que el costo del
`hashSenuelo` de `auth.go` siga siendo 12 — si baja, la defensa contra
enumeración de correos por temporización muere en silencio.

**Dimensión 7 (BD).** Cada restricción se prueba con el SQLSTATE exacto
(`23514` CHECK, `23503` FK, `23505` UNIQUE, `23502` NOT NULL) y con su caso
válido, para que la prueba no pase por la razón equivocada. Lo más valioso:
`uq_cotizadores_compilados_activa`, el índice parcial que garantiza una sola
versión ACTIVA por cotizador, ahora está probado **a nivel de esquema** y no
solo por el camino del handler.

**Dimensión 8 (Transactions).** 5 pruebas ejercitan rollback real, provocando
el fallo después de que la transacción ya escribió: relaciones de catálogos
(valor padre ajeno a mitad del loop), alta de plantilla con `crear_desde`
inexistente, y alta/conversión con un actor de sesión inexistente que viola la
FK de `cotizacion_usuarios` cuando la cotización y su versión ya se
insertaron. La conversión de solicitud es la más importante: confirma que un
fallo no deja la solicitud marcada `Convertida` sin cotización detrás, que
sería un estado irrecuperable.

### Lo que NO se cubrió, y por qué

1. **Rollback de `plantillas.Editar`, `cotizador_runtime.GuardarValores` y
   `cotizador_tabs.EliminarTab`.** No tienen costura de fallo alcanzable: toda
   la validación ocurre antes del `BeginTx`, y las escrituras posteriores no
   pueden fallar con datos válidos (`plantilla_tipos_propuesta.tipo_propuesta`
   es TEXT sin CHECK ni FK, y `normalizarIDs` deduplica). Probarlas requeriría
   inyectar un punto de fallo en producción. Se dejaron 3 pruebas que
   documentan explícitamente que la validación es previa —lo cual también es
   información útil— en vez de forzar el rollback con hacks (cerrar el pool,
   romper el esquema en caliente) que darían pruebas frágiles.
2. **Rollback de `compilador.Compilar` y de `CrearVersion`/`CambiarEstado`.**
   Quedaron fuera por tiempo. `Compilar` sí tiene costura plausible (el
   `UPDATE calculadoras` posterior al INSERT del compilado): es el próximo
   candidato natural.
3. **Orden cronológico estricto del historial.** `insertarHistorial` usa
   `now()`, que en Postgres es el instante de inicio de la transacción, así que
   dos eventos consecutivos pueden compartir timestamp. Se afirma lo
   determinista (que los eventos existen y que la lista viene ordenada
   descendente) en vez de una secuencia que dependería del reloj.
4. **Los CHECK que el esquema no tenía — RESUELTO en Tanda 2
   (`0014_checks_faltantes.sql`).** `clientes.estado`, `usuarios.estado`,
   `calculadoras.estado`, `crm_conexiones.estado` y `catalogos.alcance` no
   tenían CHECK (confirmado consultando `pg_constraint`). Antes de escribir la
   migración se revisó con `SELECT DISTINCT` que los datos sembrados no
   tuvieran valores fuera de lo esperado (clientes/usuarios: solo 'Activo';
   calculadoras/crm_conexiones/catalogos: tablas vacías en este ambiente) y se
   confirmó el conjunto válido de cada columna leyendo el código que la
   escribe, no solo la muestra de datos: `clientes.estado`/`usuarios.estado`
   → Activo/Inactivo; `calculadoras.estado` → Activo/Inactivo/Borrador/
   Publicado (Publicado lo escribe `compilador.Compilar`); `crm_conexiones`
   → Activo/Inactivo (tabla sembrada en 0001 sin ningún handler que la use
   todavía); `catalogos.alcance` → GLOBAL/COTIZADOR (nullable, restringido
   por el `<select>` del frontend aunque el comentario original de 0001
   sugería un valor libre). Como recordatorio, `integraciones_api.estado` ya
   tenía su CHECK propio desde 0011 y no forma parte de esta migración.

### Hallazgos adicionales para el equipo (no se tocó nada)

- **El comentario del historial no se muestra.** `consultarHistorial`
  (cotizaciones.go) devuelve `accion`, `nombre_usuario`, `fecha` y
  `estado_nuevo`: el `comentario` que escribe el usuario al cambiar de estado y
  el `estado_anterior` se guardan pero nunca llegan a la pantalla de Actividad.
  El rastro está completo en la tabla, incompleto en la UI.
- **No hay validación del grafo de estados.** `Borrador → Ganada` se acepta sin
  pasar por "Enviada al Cliente"; el CHECK limita el conjunto de estados, no
  las transiciones. Además, `estado: "Ganada"` sin `version_aceptada` en el
  cuerpo deja la cotización Ganada sin versión aceptada, mientras `Aceptada` sí
  la fija el servidor.
- **FK sin `ON DELETE`:** `solicitudes.cotizacion_id_generada` y
  `cotizaciones.cliente_id`. La segunda es deliberada y quedó fijada por
  prueba (es la contraparte de esquema del guard de desactivación de clientes);
  la primera implica que una cotización nacida de una solicitud no se puede
  borrar mientras la solicitud la siga apuntando.
- **Los handlers leen el actor de forma tolerante** (`usuarioID, _ := ...`).
  Con el middleware puesto siempre hay actor, pero si una ruta quedara fuera
  del grupo protegido, en vez de un 401 se obtendría un 500 por violación de
  FK (o un rastro de auditoría sin autor). La prueba de wiring de la dimensión
  4 es lo que cierra ese riesgo.

## Próximos pasos sugeridos (Tandas 2 y 3)

- Rollback de `compilador.Compilar` (costura ya identificada).
- ~~Decidir el gate de rol de `GET /api/usuarios` y `/api/roles`, y
  probarlo.~~ Resuelto en Tanda 2 (ver "Pendientes de decisión" arriba).
- ~~Evaluar la migración de CHECK para las 5 columnas de estado sin
  restricción.~~ Resuelto en Tanda 2 (`0014_checks_faltantes.sql`).
- Extraer el armado del router a una función testeable, para poder verificar
  los 401 endpoint por endpoint en proceso, sin depender del análisis estático
  ni de un servidor levantado.
