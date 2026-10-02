# Decisión: guardar borradores incompletos y exigir el precio al avanzar

**Estado:** aplicada en el código, **pendiente de confirmar con el jefe**.
Relaja una regla del Anexo Técnico (§7, guardado atómico de salidas), así
que hay que validarla antes de darla por definitiva.

## Problema

Un cotizador con precio (`funcion_campo = TOTAL_PRECIO_OFERTA` o la salida
`TOTAL_PRECIO` mapeada) convierte esa salida en **requerida**. Hasta ahora,
cada guardado del Motor de Ejecución calculaba las salidas dentro de la misma
transacción que los valores (`persistirSalidasSnapshot`), y si una salida
requerida no se podía resolver revertía **todo** el guardado con HTTP 400.

En la práctica: mientras la persona llenaba el cotizador campo por campo, cada
autoguardado fallaba ("No se guardaron los últimos cambios: ...") hasta que
estuvieran completos **todos** los operandos del precio, y lo escrito se
perdía. Con un calculado `SubTotal + Impuesto`, escribir solo el SubTotal daba
400. Además, los mensajes mostraban códigos internos (`CTZ-ELE-...`,
`TOTAL_PRECIO`, `MONEDA`) y un cotizador publicado sin precio desaparecía de
"Nueva cotización" sin que nadie avisara por qué.

## Decisión

1. **Un Borrador siempre se puede guardar**, esté completo o no.
2. **El precio y los campos obligatorios se exigen al avanzar** la cotización,
   no en cada guardado.

## Qué se relajó (y qué no)

Solo para versiones en estado **Borrador**, y solo para el caso
**"fuente vacía o incompleta"**:

| Situación en un Borrador | Antes | Ahora |
|---|---|---|
| Salida requerida sin valor porque falta un dato de entrada | 400, no se guarda nada | 200, se guarda; la salida va en `pendientes` |
| Campo marcado como obligatorio en el Diseñador, vacío | 400 | 200, va en `pendientes` |
| Valor incompatible con el tipo (texto en un campo de moneda) | 400 | **400 (sin cambios)** |
| División entre cero u otro error de cálculo con los datos completos | 400 | **400 (sin cambios)** |
| Referencia circular entre salidas | 400 | **400 (sin cambios)** |
| Reglas `BLOQUEAR_GUARDADO` / `CAMPO_REQUERIDO` del motor de Reglas | 400 | **400 (sin cambios)** |

Fuera de Borrador (por ejemplo "Revisión Comercial" o "Cambios solicitados",
que también son editables) **no se relaja nada**: lo incompleto sigue siendo
error, ahora con mensaje legible.

Cómo se distingue "falta un dato" de "el cálculo falla": `diagnosticarFuente`
(`api-go/internal/handlers/salidas_pendientes.go`) recorre la cadena de
cálculo de la fuente. Si encuentra algún dato de entrada vacío del que
depende, es un pendiente; si todos los datos están y aun así no se puede
calcular, es un error. Ante la duda se bloquea, como antes.

### Sin valores viejos

Cuando una salida que ya estaba resuelta deja de estarlo (se borra el
Impuesto), su fila de `cotizacion_salidas` se elimina y las columnas de caché
de `cotizacion_versiones` (`total_precio`, etc.) y de `cotizacion_opciones`
vuelven a 0. Nunca queda circulando un precio anterior en el Gestor o el
Dashboard. Las salidas que sí se pueden resolver se siguen persistiendo y el
`snapshot_json` se sigue generando en cada guardado.

## Dónde se exige

Se rechaza con **409** y un mensaje que lista lo que falta
(`pendientes_avance.go`, `verificarVersionCompleta`):

- `POST /api/cotizaciones/{id}/estado` hacia **Revisión Comercial**,
  **Enviada al Cliente**, **Aceptada** o **Ganada**.
- `POST /api/cotizaciones/{id}/enlace` (generar el enlace público).

Pasar a **Cancelada**, **Perdida** o **Vencida** nunca lo exige: un borrador
incompleto se tiene que poder descartar. Los cambios de estado que dispara el
cliente desde el enlace público no se tocaron.

Una cotización sin ninguna estructura compilada (datos migrados de antes del
Motor de Ejecución) no tiene salidas que revisar y no se bloquea. Hoy no puede
nacer una cotización así: crearla exige un cotizador publicado con precio.

## Cambios visibles

- La respuesta del guardado (`POST /api/cotizador/runtime/{id}/valores` y
  `.../opciones`) trae `pendientes` (mensajes legibles) y `precio_pendiente`
  (etiquetas de lo que falta para el Precio total). `GET` del runtime trae los
  `pendientes` del último guardado.
- El Motor de Ejecución muestra "Guardado. Faltan datos para el precio: ..."
  en vez del error.
- El Gestor muestra "Precio pendiente: complete «...»" junto a $0,00
  (`GET /api/cotizaciones` → `precio_pendiente` / `precio_sin_calcular`).
- Validar y Publicar advierten (sin impedir publicar) cuando el cotizador no
  tiene precio y por eso no aparecerá en "Nueva cotización".
- Los mensajes nombran los campos por su etiqueta y las salidas por su nombre
  ("Precio total"), nunca por su código.

## A confirmar con el jefe

- Que relajar §7 solo para Borrador y solo para "fuente vacía/incompleta" es
  aceptable.
- Si "Cambios solicitados" (la cotización volvió al vendedor) debería
  comportarse como Borrador. Hoy no: solo Borrador relaja.
