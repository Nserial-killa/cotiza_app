INSERT INTO clientes (cliente_id, nombre_comercial, razon_social, estado)
VALUES ('cli-demo-001', 'Cliente de Prueba', 'Cliente de Prueba S.A.', 'Activo')
ON CONFLICT (cliente_id) DO NOTHING;

INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado)
VALUES ('DEMO-COTIZADOR', 'Cotizador de Prueba', 'Activo')
ON CONFLICT (calculadora_id) DO NOTHING;

INSERT INTO cotizaciones (cotizacion_id, calculadora_id, cliente_id, codigo_oferta, tipo_propuesta, estado)
VALUES
  ('cot-demo-001', 'DEMO-COTIZADOR', 'cli-demo-001', 'OF-DEMO-001', 'Proyecto nuevo', 'Borrador'),
  ('cot-demo-002', 'DEMO-COTIZADOR', 'cli-demo-001', 'OF-DEMO-002', 'Consultoría',    'Enviada al Cliente'),
  ('cot-demo-003', 'DEMO-COTIZADOR', 'cli-demo-001', 'OF-DEMO-003', 'Soporte',        'Aceptada')
ON CONFLICT (cotizacion_id) DO NOTHING;

UPDATE cotizaciones SET version_aceptada = 1 WHERE cotizacion_id = 'cot-demo-003';

INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion)
VALUES
  ('cot-demo-001', 'usr-demo-vendedor', 'Vendedor'),
  ('cot-demo-002', 'usr-ivan-villa',    'Vendedor'),
  ('cot-demo-002', 'usr-demo-admin',    'Analista'),
  ('cot-demo-003', 'usr-demo-vendedor', 'Vendedor')
ON CONFLICT DO NOTHING;

INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, nombre_version, estado, moneda, total_precio)
VALUES
  ('cot-demo-001', 1, 'Versión inicial', 'Borrador',           'US$', 0),
  ('cot-demo-002', 1, 'Versión inicial', 'Enviada al Cliente', 'US$', 1500.00),
  ('cot-demo-003', 1, 'Versión inicial', 'Aceptada',           'US$', 2300.00)
ON CONFLICT (cotizacion_id, numero_version) DO NOTHING;

UPDATE cotizacion_versiones
SET fecha_aceptacion = now(), aceptada_por = 'usr-demo-vendedor'
WHERE cotizacion_id = 'cot-demo-003' AND numero_version = 1;