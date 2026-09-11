# Auditoría de performance y carga

## Alcance y criterio de aceptación

Esta tanda mide los flujos de mayor uso con k6: autenticación y listado de cotizaciones, dashboard, reporte JSON/CSV y recepción de solicitudes externas. Cotiza es un sistema interno para decenas de usuarios concurrentes; 10 y 50 VUs representan operación esperada y 100 VUs es una prueba de estrés, no una meta de producción.

Los scripts exigen menos de 1 % de errores y estos p95:

- login: 1.500 ms;
- listado de cotizaciones y solicitud externa: 500 ms;
- dashboard y reportes: 1.500 ms.

Cada VU reutiliza su sesión después del primer login. Los flujos de lectura esperan un segundo entre iteraciones; el externo simula una ráfaga con 200 ms entre solicitudes.

## Instalación y ejecución

La [documentación oficial de k6](https://grafana.com/docs/k6/latest/set-up/install-k6/) ofrece instalación nativa y con Docker. En macOS:

```bash
brew install k6
# Alternativa usada en esta auditoría:
docker pull grafana/k6:latest
```

Con los contenedores de Cotiza activos, cree el conjunto aislado de carga. Las credenciales son exclusivamente de QA y no deben versionarse:

```bash
LOAD_EMAIL=qa.load@example.test \
LOAD_PIN='PIN_DE_PRUEBA' \
LOAD_API_KEY='CLAVE_DE_PRUEBA' \
./load-tests/seed.sh
```

La semilla es repetible y crea 2.000 clientes, 5.000 cotizaciones con versión y vendedor, y 500 solicitudes variadas. Solo reemplaza filas con prefijos `load-*`, `LOAD-*` o claves de idempotencia `load:*`.

Ejecute todos los escenarios de forma incremental:

```bash
LOAD_EMAIL=qa.load@example.test \
LOAD_PIN='PIN_DE_PRUEBA' \
LOAD_API_KEY='CLAVE_DE_PRUEBA' \
LOAD_LEVELS='10 50 100' DURATION=30s \
./load-tests/run-docker.sh
```

Los resúmenes JSON quedan localmente en `load-tests/results/` y no se versionan. Un código de salida distinto de cero significa que al menos un umbral fue superado; el runner conserva los resultados y continúa con los demás niveles. Para revisar planes o retirar los datos:

```bash
./load-tests/explain.sh
./load-tests/cleanup.sh
```

## Entorno medido

Medición local del 10 de septiembre de 2026 sobre `3beb544`, Docker Desktop en ARM64, con 11 CPU y 8,32 GB asignados. Se usaron k6 2.2.0, PostgreSQL 16.15 y contenedores de API y base de datos en el mismo equipo. Los números sirven como línea base comparativa; no equivalen a capacidad garantizada de producción.

## Resultados

Todas las respuestas HTTP y todos los checks fueron correctos: **0 % de errores** en cada escenario y nivel. Las latencias están en milisegundos. `req/s` excluye el login inicial y, para reportes, representa ciclos completos (un JSON y un CSV por ciclo).

| Endpoint | VUs | p50 | p95 | p99 | req/s | Umbral |
|---|---:|---:|---:|---:|---:|---|
| Cotizaciones filtradas | 10 | 7,83 | 22,92 | 24,68 | 9,78 | Cumple |
| Cotizaciones filtradas | 50 | 8,06 | 22,37 | 57,73 | 47,03 | Cumple |
| Cotizaciones filtradas | 100 | 12,59 | 28,34 | 38,44 | 89,02 | Cumple |
| Dashboard | 10 | 68,23 | 181,30 | 243,26 | 9,08 | Cumple |
| Dashboard | 50 | 77,17 | 141,38 | 371,75 | 42,92 | Cumple |
| Dashboard | 100 | 1.483,20 | 1.760,58 | 2.300,07 | 35,42 | No cumple |
| Reporte JSON | 10 | 30,72 | 38,83 | 41,66 | 9,37 | Cumple |
| Reporte CSV | 10 | 22,77 | 29,98 | 33,34 | 9,37 | Cumple |
| Reporte JSON | 50 | 23,14 | 55,85 | 85,88 | 45,19 | Cumple |
| Reporte CSV | 50 | 22,20 | 63,85 | 86,06 | 45,19 | Cumple |
| Reporte JSON | 100 | 25,48 | 105,40 | 187,13 | 84,35 | Cumple |
| Reporte CSV | 100 | 25,10 | 142,56 | 210,98 | 84,35 | Cumple |
| Solicitud externa | 10 | 296,42 | 316,85 | 323,90 | 20,05 | Cumple |
| Solicitud externa | 50 | 1.236,34 | 2.610,50 | 2.695,69 | 32,43 | No cumple |
| Solicitud externa | 100 | 2.745,05 | 2.777,52 | 2.786,25 | 33,77 | No cumple |

El login inicial obtuvo p50/p95/p99 de 320,13/331,33/331,50 ms con 10 VUs; 1.450,50/1.486,69/1.493,88 ms con 50; y 2.880,33/2.892,70/2.897,71 ms con 100. Cumple hasta 50 VUs y rebasa el umbral en estrés. El hash bcrypt protege el PIN y hace que un pico simultáneo de autenticaciones sea deliberadamente costoso; una sesión normal evita repetir ese costo en cada lectura.

Como control de variabilidad, los logins incluidos en los otros escenarios dieron p95 entre 1.420 y 1.509 ms con 50 VUs; la corrida del dashboard quedó 9,49 ms sobre el límite. Se considera una frontera de capacidad, no un fallo del endpoint del dashboard, pero debe vigilarse si se esperan 50 inicios de sesión exactamente simultáneos.

## Análisis SQL y cuellos de botella

`EXPLAIN (ANALYZE, BUFFERS)` con el mismo volumen mostró:

| Consulta representativa | Filas | Tiempo BD |
|---|---:|---:|
| Listado filtrado de cotizaciones | 434 | 9,35 ms |
| Agregaciones del dashboard | 1 | 4,65 ms |
| Detalle del dashboard | 5.004 | 43,16 ms |
| Reporte filtrado | 868 | 5,93 ms |
| Lectura de integración activa | 1 | 0,05 ms |

El listado usa `BitmapAnd` sobre los índices existentes de estado y cotizador, además de las llaves de versión y asignación. Los recorridos secuenciales del dashboard son apropiados porque este solicita el conjunto completo. El reporte recorre 5.004 filas en menos de 6 ms; a este volumen un índice adicional no sería selectivo ni resolvería una latencia observada. Por ello **no se agregó ninguna migración de índices**.

El endpoint más lento bajo estrés es el dashboard. Cada respuesta sin filtros contiene unas 5.004 cotizaciones y mide aproximadamente 2,44 MB: a 50 VUs transfirió ~105 MB/s. PostgreSQL termina el detalle en ~43 ms, por lo que la degradación a 100 VUs está en serialización y transferencia del resultado completo, no en un índice faltante. Si el volumen crece, la mejora respaldada por estos datos sería paginar o limitar `cotizaciones_recientes`, manteniendo los KPIs agregados; requiere acordar el contrato del frontend y no se aplicó en esta tanda.

La solicitud externa alcanza una meseta de ~33 solicitudes/s. La consulta de integración tarda ~0,05 ms; el costo dominante es `bcrypt.CompareHashAndPassword` en cada petición (~297 ms ya con 10 VUs). Es una propiedad del diseño actual de claves y no un N+1 SQL. Optimizarlo de forma segura requeriría identificar la integración mediante un prefijo no secreto y verificar una sola clave, o introducir una caché con invalidación inmediata al revocar. Ambos cambian el diseño de autenticación, por lo que se documentan para una tanda específica y no se aplican como arreglo apresurado.

## Interpretación futura

Compare siempre el mismo volumen, duración, hardware y versión antes/después. Un p95 excedido con 0 % de errores indica saturación o colas; errores por encima de 1 % indican pérdida de capacidad o fallos funcionales. Para el uso real actual, los endpoints internos de negocio cumplen hasta 50 VUs, con el login en el borde del umbral; una ráfaga externa sostenida y 100 VUs identifican límites, no cargas habituales.
