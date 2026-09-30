-- ============================================================
-- COTIZA — Migración 0033: alcance propio por rol.
--
-- Las 6 banderas de roles existen desde 0001 (puede_crear,
-- puede_editar_borrador, puede_crear_version, puede_ver_price,
-- puede_aprobar, puede_parametrizar). Esta agrega la séptima:
-- alcance_propio = true limita el listado y el Gestor de Cotizaciones
-- a las cotizaciones donde el usuario de la sesión es el responsable.
-- Qué significa "responsable" para cada rol (Vendedor vs. Consultor)
-- está documentado en internal/handlers/permisos.go.
--
-- Acotado a Cotizaciones a propósito: Dashboard, Reportes y Clientes
-- NO aplican este filtro.
-- ============================================================

ALTER TABLE roles
    ADD COLUMN alcance_propio BOOLEAN NOT NULL DEFAULT false;

UPDATE roles SET alcance_propio = true WHERE rol IN ('Vendedor', 'Consultor');
