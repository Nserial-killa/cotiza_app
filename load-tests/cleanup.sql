\set ON_ERROR_STOP on

BEGIN;
DELETE FROM solicitudes
 WHERE idempotency_key LIKE 'load:%'
    OR integracion_id = '00000000-0000-4000-8000-000000000001'::uuid;
DELETE FROM cotizaciones WHERE cotizacion_id LIKE 'load-cot-%';
DELETE FROM clientes WHERE cliente_id LIKE 'load-cli-%';
DELETE FROM sesiones WHERE usuario_id = 'load-user';
DELETE FROM integraciones_api
 WHERE integracion_id = '00000000-0000-4000-8000-000000000001'::uuid;
DELETE FROM calculadoras WHERE calculadora_id LIKE 'LOAD-CALC-%';
DELETE FROM usuarios WHERE usuario_id = 'load-user';
DELETE FROM organizaciones WHERE organizacion_id = 'load-org';
COMMIT;
