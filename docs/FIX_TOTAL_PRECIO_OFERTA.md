# Corrección del total de la oferta

## Causa y corrección (28 de septiembre de 2026)

En `OF-20260924-8617`, la fórmula de `PrecioConDescuento` resolvía
correctamente `10000 - (10000 * 50 / 100) = 5000`, pero ese elemento no
estaba asignado al total. Además, `precio_base` tenía la función
`MONEDA_OFERTA`: el importe `10000` terminaba guardado como divisa.

Se corrigió la configuración mediante el API del Diseñador:

- `precio_base`: función `NORMAL`.
- `PrecioConDescuento`: función `TOTAL_PRECIO_OFERTA`, resultado `MONEDA`.
- Salida requerida `TOTAL_PRECIO`: fuente `CALCULADO`, vinculada a ese
  campo calculado. No se infieren totales por etiquetas o nombres.
- Se publicó la configuración corregida para las cotizaciones nuevas.

La cotización afectada tenía una única versión, en Borrador, sin enlace
público ni aceptación. Con respaldo previo, se actualizó exclusivamente
su referencia al compilado y se regeneró su snapshot mediante el guardado
del runtime, conservando los valores capturados y restaurando `USD`.
No se modificaron compilados históricos ni cotizaciones enviadas/aceptadas.
La comparación del respaldo confirmó intactas las otras tres versiones.

## Prevención en código

El guardado de elementos y el compilador rechazan campos numéricos usados
como moneda con un mensaje que indica configurar el precio total. El
runtime tampoco sobrescribe la divisa con importes de compilados antiguos.

Estas defensas quedan en el código para el siguiente despliegue. El
contenedor actual es anterior a las migraciones existentes 0029–0032;
no se actualizó el servidor ni se aplicaron esas migraciones ajenas a esta
corrección. La corrección de configuración y del borrador ya está activa.

## Verificación

- Navegador: listado, encabezado, resumen económico, versión y vista
  previa muestran `USD 5 000,00`.
- `cotizacion_total_descuento_test.go`: descuentos de 50%, 25% y 100%
  producen 5000, 7500 y 0, consistentes entre runtime, salidas, detalle,
  listado y oferta; incluye rechazo de importes usados como moneda.
- `go test ./... -race -v`: aprobado sobre PostgreSQL temporal con todas
  las migraciones; sin carreras. Solo se omitió la prueba HTTP opcional
  `TestRutasProtegidas_RechazoRealSobreHTTP` por no configurar `API_BASE_URL`.
- `go build ./...`, `go vet ./...` y `gofmt -l .`: correctos.

No hubo cambios de frontend ni fue necesario regenerarlo. No se usó
`docker compose down -v`: los datos existentes se conservaron.
