-- ============================================================
-- COTIZA — Datos de DEMO para pruebas (Sprint 1)
-- ============================================================
-- IMPORTANTE: esto es SOLO para desarrollo y demos con el jefe.
-- No es data real de Exceltec. Cuando se migren los usuarios
-- reales (migration-python/migrate_sheets_to_postgres.py), este
-- archivo debería revisarse — probablemente eliminarlo o
-- comentarlo antes de entregar con datos reales de producción.
--
-- Se aplica SOLO la primera vez que se crea el volumen de
-- Postgres (mismo mecanismo que 0001_init_schema.sql). Si tu
-- volumen ya tiene datos (ya corriste el proyecto antes), esto
-- NO se aplica solo — hay que recrear el volumen:
--   docker compose down -v
--   docker compose up --build
--
-- Credenciales de prueba después de aplicar este seed:
--   Administrador — demo.admin@exceltecgroup.com     / PIN: 1234
--   Vendedor      — demo.vendedor@exceltecgroup.com  / PIN: 1234
-- ============================================================

INSERT INTO usuarios (usuario_id, nombre, correo, pin_hash, rol, estado, puede_ver_gestor)
VALUES
  ('usr-demo-admin', 'Administrador Demo', 'demo.admin@exceltecgroup.com',
   crypt('1234', gen_salt('bf')), 'Administrador', 'Activo', true),

  ('usr-ivan-villa', 'Ivan Villalobos', 'ivan.villalobos@exceltecgroup.com',
   crypt('1', gen_salt('bf')), 'Administrador', 'Activo', true),
   
ON CONFLICT (usuario_id) DO NOTHING;

