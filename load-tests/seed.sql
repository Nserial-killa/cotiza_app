\set ON_ERROR_STOP on

\if :{?load_email}
\else
  \echo 'Falta la variable psql load_email.'
  \quit
\endif
\if :{?load_pin}
\else
  \echo 'Falta la variable psql load_pin.'
  \quit
\endif
\if :{?load_api_key}
\else
  \echo 'Falta la variable psql load_api_key.'
  \quit
\endif

BEGIN;

-- El prefijo load-* mantiene la semilla aislada de datos normales. Repetir
-- este script reemplaza solo el conjunto de carga y también limpia las
-- solicitudes generadas por ejecuciones anteriores de k6.
DELETE FROM solicitudes
 WHERE idempotency_key LIKE 'load:%'
    OR integracion_id = '00000000-0000-4000-8000-000000000001'::uuid;
DELETE FROM cotizaciones WHERE cotizacion_id LIKE 'load-cot-%';
DELETE FROM clientes WHERE cliente_id LIKE 'load-cli-%';
DELETE FROM sesiones WHERE usuario_id = 'load-user';

INSERT INTO organizaciones (organizacion_id, nombre, razon_social, estado)
VALUES ('load-org', 'Organización de carga', 'Exceltec QA Performance', 'Activo')
ON CONFLICT (organizacion_id) DO UPDATE
SET nombre = EXCLUDED.nombre, razon_social = EXCLUDED.razon_social, estado = EXCLUDED.estado;

INSERT INTO usuarios
  (usuario_id, organizacion_id, nombre, correo, pin_hash, rol, estado, puede_ver_gestor)
VALUES
  ('load-user', 'load-org', 'Usuario de carga', lower(:'load_email'),
   crypt(:'load_pin', gen_salt('bf', 12)), 'Administrador', 'Activo', true)
ON CONFLICT (usuario_id) DO UPDATE
SET organizacion_id = EXCLUDED.organizacion_id,
    nombre = EXCLUDED.nombre,
    correo = EXCLUDED.correo,
    pin_hash = EXCLUDED.pin_hash,
    rol = EXCLUDED.rol,
    estado = EXCLUDED.estado,
    puede_ver_gestor = EXCLUDED.puede_ver_gestor;

INSERT INTO calculadoras
  (calculadora_id, nombre_calculadora, linea_negocio, version_actual, estado)
SELECT format('LOAD-CALC-%s', lpad(serie::text, 2, '0')),
       format('Cotizador de carga %s', serie),
       CASE WHEN serie % 2 = 0 THEN 'Servicios' ELSE 'Tecnología' END,
       '1', 'Publicado'
  FROM generate_series(1, 5) AS serie
ON CONFLICT (calculadora_id) DO UPDATE
SET nombre_calculadora = EXCLUDED.nombre_calculadora,
    linea_negocio = EXCLUDED.linea_negocio,
    version_actual = EXCLUDED.version_actual,
    estado = EXCLUDED.estado;

INSERT INTO integraciones_api
  (integracion_id, nombre, api_key_hash, estado, creado_por)
VALUES
  ('00000000-0000-4000-8000-000000000001'::uuid, 'Bitrix carga QA',
   crypt(:'load_api_key', gen_salt('bf', 12)), 'Activo', 'load-user')
ON CONFLICT (integracion_id) DO UPDATE
SET nombre = EXCLUDED.nombre,
    api_key_hash = EXCLUDED.api_key_hash,
    estado = EXCLUDED.estado,
    creado_por = EXCLUDED.creado_por,
    ultima_uso = NULL;

INSERT INTO clientes
  (cliente_id, organizacion_id, origen, crm_tipo, crm_id, tipo_persona,
   nombre_comercial, razon_social, industria, pais, provincia, ciudad,
   estado, usuario_creador_id, fecha_creacion, fecha_actualizacion)
SELECT format('load-cli-%s', lpad(serie::text, 6, '0')),
       'load-org', 'COTIZA', 'BITRIX24', format('LOAD-CRM-%s', serie), 'Jurídica',
       format('Cliente de carga %s', serie), format('Razón social de carga %s S.A.', serie),
       (ARRAY['Tecnología','Servicios','Industria','Comercio'])[((serie - 1) % 4) + 1],
       'Costa Rica', (ARRAY['San José','Heredia','Alajuela','Cartago'])[((serie - 1) % 4) + 1],
       format('Ciudad %s', ((serie - 1) % 20) + 1),
       'Activo', 'load-user',
       now() - make_interval(days => serie % 900),
       now() - make_interval(days => serie % 120)
  FROM generate_series(1, 2000) AS serie;

WITH datos AS (
  SELECT serie,
         format('load-cot-%s', lpad(serie::text, 6, '0')) AS cotizacion_id,
         format('LOAD-CALC-%s', lpad((((serie - 1) % 5) + 1)::text, 2, '0')) AS calculadora_id,
         format('load-cli-%s', lpad((((serie - 1) % 2000) + 1)::text, 6, '0')) AS cliente_id,
         (ARRAY['Borrador','Revisión Comercial','Enviada al Cliente','Vista por el Cliente',
                'Cambios solicitados','Aceptada','Ganada','Perdida','Vencida','Cancelada'])
           [((serie - 1) % 10) + 1] AS estado,
         now() - make_interval(days => serie % 730, hours => serie % 24) AS fecha
    FROM generate_series(1, 5000) AS serie
)
INSERT INTO cotizaciones
  (cotizacion_id, calculadora_id, cliente_id, organizacion_id, codigo_oferta,
   tipo_propuesta, estado, version_actual, version_aceptada,
   fecha_creacion, fecha_actualizacion)
SELECT cotizacion_id, calculadora_id, cliente_id, 'load-org',
       format('LOAD-OF-%s', lpad(serie::text, 6, '0')),
       (ARRAY['Consultoría','Proyecto nuevo','Soporte'])[((serie - 1) % 3) + 1],
       estado, 1,
       CASE WHEN estado IN ('Aceptada','Ganada') THEN 1 ELSE NULL END,
       fecha, fecha + make_interval(days => serie % 30)
  FROM datos;

WITH datos AS (
  SELECT serie,
         format('load-cot-%s', lpad(serie::text, 6, '0')) AS cotizacion_id,
         (ARRAY['Borrador','Revisión Comercial','Enviada al Cliente','Vista por el Cliente',
                'Cambios solicitados','Aceptada','Ganada','Perdida','Vencida','Cancelada'])
           [((serie - 1) % 10) + 1] AS estado,
         (1000 + (serie % 250) * 137.50)::numeric(14,2) AS total_precio,
         now() - make_interval(days => serie % 730, hours => serie % 24) AS fecha
    FROM generate_series(1, 5000) AS serie
)
INSERT INTO cotizacion_versiones
  (cotizacion_id, numero_version, nombre_version, estado, moneda,
   total_precio, total_costo, total_ganancia, margen_total,
   fecha_creacion, fecha_actualizacion)
SELECT cotizacion_id, 1, 'Versión de carga', estado, 'US$',
       total_precio, total_precio * 0.72, total_precio * 0.28, 28.00,
       fecha, fecha + make_interval(days => serie % 30)
  FROM datos;

INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion)
SELECT format('load-cot-%s', lpad(serie::text, 6, '0')), 'load-user', 'Vendedor'
  FROM generate_series(1, 5000) AS serie;

WITH datos AS (
  SELECT serie,
         (ARRAY['Nueva','En revisión','Descartada','Convertida'])[((serie - 1) % 4) + 1] AS estado,
         format('LOAD-CALC-%s', lpad((((serie - 1) % 5) + 1)::text, 2, '0')) AS calculadora_id,
         format('load-cli-%s', lpad((((serie - 1) % 2000) + 1)::text, 6, '0')) AS cliente_id,
         now() - make_interval(days => serie % 180, hours => serie % 24) AS fecha
    FROM generate_series(1, 500) AS serie
)
INSERT INTO solicitudes
  (origen, integracion_id, cliente_id, cliente_nombre, cliente_razon_social,
   contacto_nombre, contacto_correo, calculadora_id, descripcion, titulo,
   crm_id, fecha_requerida, prioridad, vendedor_id, estado,
   cotizacion_id_generada, idempotency_key, fecha_creacion, fecha_actualizacion)
SELECT 'API_EXTERNA', '00000000-0000-4000-8000-000000000001'::uuid,
       cliente_id, format('Cliente solicitud %s', serie), format('Cliente solicitud %s S.A.', serie),
       format('Contacto %s', serie), format('contacto%s@example.test', serie), calculadora_id,
       'Solicitud sembrada para pruebas de carga.', format('Oportunidad de carga %s', serie),
       format('LOAD-DEAL-%s', serie), current_date + (serie % 60),
       (ARRAY['Baja','Media','Alta','Urgente'])[((serie - 1) % 4) + 1],
       'load-user', estado,
       CASE WHEN estado = 'Convertida'
            THEN format('load-cot-%s', lpad(serie::text, 6, '0')) ELSE NULL END,
       format('load:seed:%s', serie), fecha, fecha
  FROM datos;

ANALYZE clientes;
ANALYZE cotizaciones;
ANALYZE cotizacion_versiones;
ANALYZE cotizacion_usuarios;
ANALYZE solicitudes;

COMMIT;

SELECT
  (SELECT COUNT(*) FROM clientes WHERE cliente_id LIKE 'load-cli-%') AS clientes,
  (SELECT COUNT(*) FROM cotizaciones WHERE cotizacion_id LIKE 'load-cot-%') AS cotizaciones,
  (SELECT COUNT(*) FROM solicitudes WHERE idempotency_key LIKE 'load:seed:%') AS solicitudes;
