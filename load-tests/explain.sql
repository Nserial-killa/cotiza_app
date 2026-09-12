\set ON_ERROR_STOP on
\pset pager off

\echo '=== Listado de cotizaciones filtrado ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT c.cotizacion_id, c.version_actual, cv.nombre_version, c.version_aceptada,
       c.codigo_oferta, cl.nombre_comercial,
       COALESCE(cl.razon_social, cl.nombre_comercial),
       calc.nombre_calculadora, c.calculadora_id, c.tipo_propuesta, c.estado,
       COALESCE(cv.total_precio, 0), COALESCE(cv.moneda, 'US$'),
       STRING_AGG(DISTINCT CASE WHEN cu.funcion = 'Vendedor' THEN u.nombre END, ', '),
       STRING_AGG(DISTINCT CASE WHEN cu.funcion = 'Analista' THEN u.nombre END, ', '),
       c.fecha_actualizacion
  FROM cotizaciones c
  JOIN calculadoras calc ON calc.calculadora_id = c.calculadora_id
  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
  LEFT JOIN cotizacion_versiones cv
    ON cv.cotizacion_id = c.cotizacion_id AND cv.numero_version = c.version_actual
  LEFT JOIN cotizacion_usuarios cu ON cu.cotizacion_id = c.cotizacion_id
  LEFT JOIN usuarios u ON u.usuario_id = cu.usuario_id
 WHERE c.estado = 'Borrador'
   AND c.calculadora_id = 'LOAD-CALC-01'
   AND EXISTS (
       SELECT 1 FROM cotizacion_usuarios cu2
        WHERE cu2.cotizacion_id = c.cotizacion_id
          AND cu2.usuario_id = 'load-user')
   AND c.fecha_creacion::date >= DATE '2025-01-01'
 GROUP BY c.cotizacion_id, c.version_actual, cv.nombre_version, c.version_aceptada,
          c.codigo_oferta, cl.nombre_comercial, cl.razon_social,
          calc.nombre_calculadora, c.calculadora_id, c.tipo_propuesta,
          c.estado, cv.total_precio, cv.moneda, c.fecha_actualizacion
 ORDER BY c.fecha_actualizacion DESC;

\echo '=== Dashboard: agregaciones ==='
EXPLAIN (ANALYZE, BUFFERS)
WITH historico AS (
  SELECT c.cotizacion_id, c.calculadora_id, c.cliente_id, c.codigo_oferta,
         c.estado, c.fecha_creacion, c.fecha_actualizacion, c.version_actual,
         COUNT(*) OVER (
           PARTITION BY c.cliente_id
           ORDER BY c.fecha_creacion, c.cotizacion_id
           ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
         ) AS cotizaciones_previas
    FROM cotizaciones c
), base AS (
  SELECT h.*, cv.total_precio, cv.moneda, cv.margen_total,
         calc.nombre_calculadora,
         cl.nombre_comercial AS cliente,
         COALESCE(cl.razon_social, cl.nombre_comercial) AS empresa,
         vendedor.usuario_id AS vendedor_id, vendedor.nombre AS vendedor,
         CASE WHEN h.cliente_id IS NOT NULL AND h.cotizaciones_previas > 0
              THEN 'Cliente existente' ELSE 'Prospecto' END AS tipo_cliente
    FROM historico h
    JOIN cotizacion_versiones cv
      ON cv.cotizacion_id = h.cotizacion_id AND cv.numero_version = h.version_actual
    JOIN calculadoras calc ON calc.calculadora_id = h.calculadora_id
    LEFT JOIN clientes cl ON cl.cliente_id = h.cliente_id
    LEFT JOIN LATERAL (
      SELECT MIN(cu.usuario_id) AS usuario_id, MIN(u.nombre) AS nombre
        FROM cotizacion_usuarios cu
        JOIN usuarios u ON u.usuario_id = cu.usuario_id
       WHERE cu.cotizacion_id = h.cotizacion_id AND cu.funcion = 'Vendedor'
    ) vendedor ON true
)
SELECT COUNT(*)::int,
       COUNT(*) FILTER (WHERE estado NOT IN
         ('Aceptada','Ganada','Perdida','Cancelada','Vencida'))::int,
       COALESCE(SUM(total_precio), 0), AVG(total_precio), AVG(margen_total)
  FROM base;

\echo '=== Dashboard: detalle completo devuelto por el API ==='
EXPLAIN (ANALYZE, BUFFERS)
WITH historico AS (
  SELECT c.cotizacion_id, c.calculadora_id, c.cliente_id, c.codigo_oferta,
         c.estado, c.fecha_creacion, c.fecha_actualizacion, c.version_actual,
         COUNT(*) OVER (
           PARTITION BY c.cliente_id
           ORDER BY c.fecha_creacion, c.cotizacion_id
           ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
         ) AS cotizaciones_previas
    FROM cotizaciones c
), base AS (
  SELECT h.*, cv.total_precio, cv.moneda, cv.margen_total,
         calc.nombre_calculadora,
         cl.nombre_comercial AS cliente,
         COALESCE(cl.razon_social, cl.nombre_comercial) AS empresa,
         vendedor.usuario_id AS vendedor_id, vendedor.nombre AS vendedor,
         CASE WHEN h.cliente_id IS NOT NULL AND h.cotizaciones_previas > 0
              THEN 'Cliente existente' ELSE 'Prospecto' END AS tipo_cliente
    FROM historico h
    JOIN cotizacion_versiones cv
      ON cv.cotizacion_id = h.cotizacion_id AND cv.numero_version = h.version_actual
    JOIN calculadoras calc ON calc.calculadora_id = h.calculadora_id
    LEFT JOIN clientes cl ON cl.cliente_id = h.cliente_id
    LEFT JOIN LATERAL (
      SELECT MIN(cu.usuario_id) AS usuario_id, MIN(u.nombre) AS nombre
        FROM cotizacion_usuarios cu
        JOIN usuarios u ON u.usuario_id = cu.usuario_id
       WHERE cu.cotizacion_id = h.cotizacion_id AND cu.funcion = 'Vendedor'
    ) vendedor ON true
)
SELECT cotizacion_id, codigo_oferta, cliente, empresa, calculadora_id,
       nombre_calculadora, estado, total_precio, moneda, vendedor_id,
       vendedor, tipo_cliente, fecha_creacion, fecha_actualizacion,
       margen_total
  FROM base
 ORDER BY fecha_creacion, cotizacion_id;

\echo '=== Reporte filtrado (la misma consulta alimenta JSON y CSV) ==='
EXPLAIN (ANALYZE, BUFFERS)
WITH historico AS (
  SELECT c.cotizacion_id, c.calculadora_id, c.cliente_id, c.codigo_oferta,
         c.estado, c.fecha_creacion, c.fecha_actualizacion, c.version_actual,
         COUNT(*) OVER (
           PARTITION BY c.cliente_id
           ORDER BY c.fecha_creacion, c.cotizacion_id
           ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
         ) AS cotizaciones_previas
    FROM cotizaciones c
), base AS (
  SELECT h.*, cv.total_precio, cv.moneda, cv.margen_total,
         calc.nombre_calculadora,
         cl.nombre_comercial AS cliente,
         COALESCE(cl.razon_social, cl.nombre_comercial) AS empresa,
         vendedor.nombre AS vendedor
    FROM historico h
    JOIN cotizacion_versiones cv
      ON cv.cotizacion_id = h.cotizacion_id AND cv.numero_version = h.version_actual
    JOIN calculadoras calc ON calc.calculadora_id = h.calculadora_id
    LEFT JOIN clientes cl ON cl.cliente_id = h.cliente_id
    LEFT JOIN LATERAL (
      SELECT MIN(u.nombre) AS nombre
        FROM cotizacion_usuarios cu
        JOIN usuarios u ON u.usuario_id = cu.usuario_id
       WHERE cu.cotizacion_id = h.cotizacion_id AND cu.funcion = 'Vendedor'
    ) vendedor ON true
)
SELECT codigo_oferta, cliente, empresa, nombre_calculadora, estado,
       total_precio, moneda, vendedor, fecha_creacion, margen_total
  FROM base
 WHERE calculadora_id = 'LOAD-CALC-01'
   AND fecha_creacion >= DATE '2025-01-01'
   AND fecha_creacion < DATE '2027-01-01'
 ORDER BY fecha_creacion DESC, cotizacion_id DESC;

\echo '=== Autenticación API key: acceso de base de datos ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT integracion_id::text, api_key_hash
  FROM integraciones_api
 WHERE estado = 'Activo';
