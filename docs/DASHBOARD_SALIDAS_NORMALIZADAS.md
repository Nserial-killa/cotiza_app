# Dashboard sobre salidas normalizadas

El Dashboard usa `cotizacion_salidas` como única fuente de importes, moneda y margen. No consulta `snapshot_json`, `cotizacion_valores` ni las columnas cacheadas de `cotizacion_versiones`.

## Endpoints

Los cinco endpoints protegidos aceptan los mismos filtros: `cotizador_id` (alias documental de `calculadora_id`), `vendedor_id`, `tipo_cliente`, `fecha_desde` y `fecha_hasta`. Las fechas usan formato `AAAA-MM-DD`, inclusivo en ambos extremos.

- `GET /api/dashboard/resumen`
- `GET /api/dashboard/tendencia`
- `GET /api/dashboard/estados`
- `GET /api/dashboard/segmentacion-clientes`
- `GET /api/dashboard/cotizadores`

`GET /api/dashboard` se conserva temporalmente como alias de `resumen` para compatibilidad, pero el frontend ya no lo consume.

## Moneda y versión aceptada

No existe conversión cambiaria en esta versión. Por eso ningún endpoint devuelve una suma monetaria escalar: los importes se expresan como arreglos `{moneda, monto}`. El frontend presenta un selector cuando hay moneda disponible y nunca suma grupos diferentes.

El monto aceptado y el monto ganado se leen de `TOTAL_PRECIO` en la versión indicada por `cotizaciones.version_aceptada`. Crear una versión posterior no altera estos indicadores.

## Ciclo y permisos

El inicio del ciclo es `cotizaciones.fecha_creacion`, porque el historial legado no tiene un evento inicial uniforme. El cierre es el primer evento de `cotizacion_historial` que cambia a Aceptada, Ganada, Perdida, Vencida o Cancelada.

La consulta filtra el promedio de `MARGEN_TOTAL` antes de devolverlo. Los roles sin `roles.puede_ver_price` no reciben la propiedad `margen_promedio` en ninguno de los cinco endpoints.
