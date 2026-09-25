-- ============================================================
-- COTIZA — Migración 0030: una vinculación de plantilla puede apuntar a
-- una Salida estándar del cotizador (Ronda P3, "Origen: Salida
-- especial" del paso 3 Vincular datos).
--
-- Las salidas (mapa_salidas_cotizador, 0027) llegaron después que
-- Vincular datos, que solo ofrecía CAMPO/COTIZACION_BASE. Con
-- SALIDA_ESTANDAR, fuente_id es la clave_salida (TOTAL_PRECIO, MONEDA,
-- ...) y el valor se lee de cotizacion_salidas de la versión que se
-- renderiza — no de cotizacion_versiones.
--
-- Solo las salidas públicas son vinculables: TOTAL_COSTO, TOTAL_GANANCIA
-- y MARGEN_TOTAL son información interna y Go las rechaza al guardar y
-- nunca las resuelve al renderizar (salidasPublicasPlantilla,
-- plantilla_vinculaciones.go). El CHECK de la base no enumera claves: la
-- lista vive en Go, igual que tiposSalidas.
-- ============================================================

ALTER TABLE plantilla_vinculaciones
    DROP CONSTRAINT plantilla_vinculaciones_fuente_tipo_check;

ALTER TABLE plantilla_vinculaciones
    ADD CONSTRAINT plantilla_vinculaciones_fuente_tipo_check
        CHECK (fuente_tipo IN ('CAMPO', 'COTIZACION_BASE', 'SALIDA_ESTANDAR'));
