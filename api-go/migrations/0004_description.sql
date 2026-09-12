-- ============================================================
-- COTIZA — Migración 0004: agregar descripción a las secciones
-- del cotizador (Sprint 2). Quedó afuera del diseño original del
-- esquema 0003.
-- ============================================================

ALTER TABLE tabs_cotizador ADD COLUMN descripcion TEXT;
