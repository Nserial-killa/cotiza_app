# Evidencia CP-01 … CP-15 — Calculadora ISA Custom (Ronda F2)

Casos de aceptación de §10 de `docs/Definicion_Calculadora_ISA_Custom_Cotiza-1.pdf`,
ejecutados de forma automática por
`api-go/internal/handlers/isa_custom_integracion_test.go`
(`TestISACustom_CasosAceptacionCP01aCP15`).

## Cómo se obtuvo

| Paso | Comando | Resultado |
|---|---|---|
| Base recién levantada | `docker compose down -v && docker compose up --build` | `{"status":"ok","database":"ok"}` |
| Sembrador ISA | `echo 1234 \| go run ./cmd/seed-isa-custom` | `completo=true; compilado=true`, **0 incidencias**, salida 0 |
| Sembrador otra vez (idempotencia) | ídem | `completo=true`, 0 incidencias |
| Suite completa | `go test ./... -race -v -count=1` | 4 paquetes `ok`, 0 `FAIL`, 0 `DATA RACE` |
| Verificación | `go build ./... && go vet ./... && gofmt -l .` | sin salida |

La prueba corre el **mismo sembrador** (`internal/isacustom`) por HTTP real
(`httptest.Server` + `middleware.RequiereSesion`) contra Postgres, con un
prefijo único por corrida. Los importes esperados se **calculan** en
`esperadoISA` a partir de `internal/isacustom/caso.json` (dataset §11 y
cargos configurables del sembrador), con el mismo redondeo a 2 decimales por
Campo Calculado; además se afirman los literales del documento (100.00,
80.00, 142.86, 114.29, 125.00, 357.14, 250.00).

Dataset de las tres opciones (§11 + escenarios del sembrador):

| Opción | Diferencias con §11 | TOTAL_INICIAL | TOTAL_MENSUAL |
|---|---|---:|---:|
| Recomendada ★ | — | 800.00 | 317.15 |
| Solo Chat | CONV_2500, Telefonía = No | 800.00 | 397.14 |
| Multicanal | CONV_5000, MIN_2500, Teams = Sí | 800.00 | 1070.00 |

## Resultados

| Caso | Resultado | Evidencia |
|---|---|---|
| CP-01 Crear calculadora y compilar | **Pasa** | Sembrador sin incidencias y compilado; runtime con las 6 secciones en orden `01_CONFIG … 06_COMERCIAL`, contenedor de 3 columnas en `03_CHAT`/`05_IMPL` y 2 en el resto, cada campo en su sección y en el orden declarado; 3 opciones; `opciones_cotizacion_id` = componente de alcance COTIZACION. |
| CP-02 Seleccionar catálogos | **Pasa** | `CONVERSACIONES_MES` muestra "1.000", guarda `CONV_1000`, catálogo completo (6 valores). Opción 1: COSTO_CHAT = 100.00 (1000 × 0.10), COSTO_VOZ = 80.00 (1000 × 0.080), PRECIO_CHAT = 142.86 (100 / 0.70), PRECIO_VOZ = 114.29 (80 / 0.70). |
| CP-03 Telefonía Sí → No | **Pasa** | Con MINUTOS_EXTRA = 100 (CONSUMO_EXTRA_VOZ = 12.00) TOTAL_MENSUAL = 329.15. Tras Telefonía = No en la opción 1: COSTO_VOZ = PRECIO_VOZ = CONSUMO_EXTRA_VOZ = 0; MINUTOS_MES/MINUTOS_EXTRA ocultos **solo** en esa opción (siguen visibles en Multicanal, cuyo PRECIO_VOZ sigue en 285.71); MINUTOS_EXTRA persistido "0"; TOTAL_MENSUAL = 182.86 = 329.15 − 114.29 (PRECIO_VOZ) − 12.00 (extra voz) − 20.00 (CARGO_TELEFONIA). Ver observación 1. |
| CP-04 Telefonía No → Sí | **Pasa** | MINUTOS_MES y MINUTOS_EXTRA reaparecen; MINUTOS_MES conserva `MIN_1000` → COSTO_VOZ = 80.00, PRECIO_VOZ = 114.29; CONSUMO_EXTRA_VOZ = 0 (el extra oculto quedó en 0); TOTAL_MENSUAL vuelve a 317.15. |
| CP-05 Integración No | **Pasa** | CANTIDAD_INTEGRACIONES oculta y persistida "0"; PRECIO_INTEGRACIONES = 0; TOTAL_MENSUAL 317.15 → 292.15 (−25 = PRECIO_INTEGRACION_UNIT); PRECIO_IMPLEMENTACION 800 → 700. Restaurado a Sí/1. |
| CP-06 Margen 20 % → 30 % | **Pasa** | M20 → PRECIO_CHAT = 125.00 (100 / 0.80); M30 → 142.86 (100 / 0.70). Con 20/30 daría −5.26/−3.45. |
| CP-07 Guardar y reabrir | **Pasa** | GET nuevo devuelve los 33 valores de entrada de cada una de las 3 opciones tal como se guardaron y todos los cálculos esperados; R01 sigue aplicada en Solo Chat; Recomendada conservada. Salidas desde la recomendada (AT-06): total_precio = 317.15, total_costo = 180.00, margen_total = 43.24, moneda USD. Recomendar Multicanal → total_precio = 1070.00; restaurado → 317.15. `snapshot_json` guarda `CONVERSACIONES_MES` de las 3 opciones. |
| CP-08 Duplicar escenario | **Pasa** | DUPLICAR Solo Chat crea una opción con los 33 valores copiados: COSTO_CHAT = 250.00, PRECIO_CHAT = 357.14, PRECIO_VOZ = 0. Editar la copia a CONV_300 → copia COSTO_CHAT = 30.00; el original sigue en 250.00 / `CONV_2500`. |
| CP-09 Tres escenarios | **Pasa** | Tabla de la plantilla con columnas `Concepto, Implementación, Mensualidad, Recomendada` y 3 filas: Recomendada 800.00 / 317.15 / true · Solo Chat 800.00 / 397.14 / false · Multicanal 800.00 / 1070.00 / false. |
| CP-10 Sin recomendada | **Pasa** | Cotización nueva (3 opciones, ninguna recomendada): el guardado responde HTTP 400 «… necesita una opción efectiva. Marque una opción como recomendada antes de guardar.»; administrar opciones devuelve la advertencia «ninguna está marcada como recomendada». Tras RECOMENDAR, guarda y total_precio = 317.15. |
| CP-11 Una sola opción | **Pasa** | Al eliminar 2 de 3 opciones, la restante queda `es_recomendada = true` sola (R09); guardar Solo Chat → total_precio = 397.14; eliminar la última → HTTP 409. |
| CP-12 Vista previa | **Pasa** | Ningún bloque conserva `[TOKEN]`; TIPO_AGENTE = "Servicio al Cliente", IDIOMA = "Español", CONVERSACIONES_MES = "1.000", MINUTOS_MES = "1.000"; texto «…implementar 1 agente(s)… orientado(s) a Servicio al Cliente…» (antes el token salía como `SERVICIO`, el código); tabla con 3 filas reales. |
| CP-13 Generar oferta | **Pasa** | Enlace público y vista previa resuelven la misma plantilla (comparación estructural completa, CTZ-TEC-004). Cabecera con empresa, código de oferta y fecha; las 10 secciones; Inversión de la recomendada (§9.5): 800.00 / 317.15 / 1117.15; condicionales: WhatsApp, Chat Web y Telefonía visibles, Microsoft Teams oculto, bloque de integraciones visible. Ver observación 2. |
| CP-14 Modificar catálogo | **Pasa** | Etiqueta de CONV_1000 → "1.000 conversaciones": COSTO_CHAT sigue en 100.00 y la oferta muestra la etiqueta nueva. valor_calculo 1000 → 1500: COSTO_CHAT = 150.00, PRECIO_CHAT = 214.29. Restaurado → 100.00. |
| CP-15 Validar miles/moneda | **Pasa** (alcance API) | "1.000"/"2.500" viajan como etiqueta en runtime y oferta; todos los montos de tabla e inversión con ≤ 2 decimales; moneda USD en cotización, oferta y salida `MONEDA`. El formato visual final (Intl `es-CR` en `formatoMoneda`) no lo cubre la prueba automática; se revisó a mano en Chromium: «USD 357,14», «USD 1 197,14». |

## Pruebas adicionales de la Ronda F2

`api-go/internal/handlers/alcance_cotizador_test.go` (todas pasan):

- Fórmula avanzada, operando Simple y Caja de Valor leyendo campos de otra
  sección del mismo cotizador; un ciclo que cruza secciones se rechaza; un
  campo de otro cotizador sigue rechazándose.
- La contención sigue local: un componente padre de otra sección se rechaza.
- `nombre_interno` único por cotizador al guardar; un duplicado heredado
  (insertado por SQL, como datos previos) hace que Validar/Publicar falle con
  un error que nombra a ambos elementos.
- Asociar una Sección Adicional con un `nombre_interno` repetido se rechaza y
  no deja asociación a medias.
- `alcance_opciones`: LOCAL por defecto; COTIZACION rechaza hijos, rechaza
  pasar a COTIZACION con hijos y solo admite uno por cotizador (al guardar y
  al publicar).

Motor de Ejecución (manual, Chromium headless sobre el frontend real): el
selector de opción aparece arriba de las secciones; en Solo Chat la sección
`04_VOZ` no muestra campos (R01 por opción); cambiar MARGEN_CHAT a M20 en Solo
Chat recalcula solo esa opción (PRECIO_CHAT 312,50; TOTAL_MENSUAL 352,50) y la
Recomendada sigue en 142,86 / 317,15; sin errores de JavaScript.

## Observaciones

1. **CP-03, "TOTAL_MENSUAL baja exactamente en PRECIO_VOZ".** Con las
   fórmulas del documento no es así: `PRECIO_CANALES` también incluye
   `SI(USA_TELEFONIA; CARGO_TELEFONIA; 0)`, y `CONSUMO_EXTRA_VOZ` también
   depende de Telefonía. La caída real es PRECIO_VOZ + CONSUMO_EXTRA_VOZ +
   CARGO_TELEFONIA, y eso es lo que afirma la prueba. Si el negocio espera que
   el cargo del canal no dependa de Telefonía, hay que cambiar la fórmula de
   PRECIO_CANALES, no el motor.
2. **Contacto de la portada.** Se agregó la fuente base `contacto` (contacto
   principal del cliente), pero el API todavía no permite crear contactos de
   cliente, así que en el caso sembrado ese bloque sale vacío.
3. **Campos ocultos en modo COTIZACION.** Un Campo numérico oculto se
   persiste en "0" (igual que en el modo global); una selección de catálogo,
   Lista de Precios o Tabla oculta se **conserva** y el motor la cuenta como 0
   en esa opción. La primera versión la borraba a `null`: al volver
   Telefonía a Sí, `MARGEN_TOTAL` (salida requerida) no resolvía y el guardado
   se rechazaba, con el campo todavía oculto en pantalla — CP-04 falló así
   antes de la corrección.
4. **Pendiente fuera de alcance:** el modo global (sin Opciones de Propuesta)
   sigue borrando a `null` un catálogo oculto, así que podría trabarse de la
   misma forma cuando hay salidas requeridas. No se tocó para dejar intacto
   el comportamiento previo.
