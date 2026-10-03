-- ============================================================
-- COTIZA — Migración 0034: visibilidad de módulos por rol.
--
-- Estas banderas separan el acceso de lectura a módulos completos de
-- los permisos de operación existentes. Ocultar un botón en el frontend
-- no es seguridad: los endpoints exigen las mismas banderas.
-- ============================================================

ALTER TABLE roles
    ADD COLUMN puede_ver_dashboard BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN puede_ver_administracion BOOLEAN NOT NULL DEFAULT false;

UPDATE roles
   SET puede_ver_dashboard = rol IN ('Administrador', 'Gerente Comercial', 'Solo Consulta'),
       puede_ver_administracion = rol = 'Administrador';

-- El auditor ve la información económica completa, igual que un
-- Administrador, pero conserva todas las banderas de mutación en false.
UPDATE roles SET puede_ver_price = true WHERE rol = 'Solo Consulta';

UPDATE roles
   SET descripcion = CASE rol
       WHEN 'Administrador' THEN 'Acceso completo al sistema Cotiza.'
       WHEN 'Gerente Comercial' THEN 'Acceso operativo completo, sin módulos administrativos.'
       WHEN 'Vendedor' THEN 'Opera únicamente las cotizaciones y solicitudes donde figura como vendedor; sin Dashboard.'
       WHEN 'Consultor' THEN 'Opera únicamente las cotizaciones y solicitudes donde figura como analista; sin Dashboard.'
       WHEN 'Solo Consulta' THEN 'Auditoría global de la operación, estrictamente en modo de solo lectura.'
       ELSE descripcion
   END;
