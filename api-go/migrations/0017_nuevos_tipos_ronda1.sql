-- ============================================================
-- COTIZA — Migración 0017: Diseñador de Cotizadores, Ronda 1 de 6
-- de tipos de componente nuevos: TITULO, CONTENEDOR, CAJA_VALOR.
--
-- Se amplía el CHECK de tipo de elementos_tab_cotizador (nacido en
-- 0003) y se agregan dos columnas de referencia entre elementos de
-- la misma sección:
--   - componente_padre_id: para anidar un componente dentro de un
--     Contenedor (CONTENEDOR no puede tener padre en esta ronda —
--     no hay contenedores anidados todavía).
--   - campo_fuente_id: solo para CAJA_VALOR, apunta al CAMPO o
--     CAMPO_CATALOGO cuyo valor guardado se refleja de solo lectura.
-- Ambas son opcionales y solo tienen sentido dentro del mismo tab
-- (eso se valida en Go, no acá, igual que catalogo_id).
-- ============================================================

ALTER TABLE elementos_tab_cotizador
    ADD COLUMN componente_padre_id TEXT REFERENCES elementos_tab_cotizador(elemento_id),
    ADD COLUMN campo_fuente_id     TEXT REFERENCES elementos_tab_cotizador(elemento_id);

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT chk_elementos_padre_no_autoreferencia
        CHECK (componente_padre_id IS NULL OR componente_padre_id <> elemento_id),
    ADD CONSTRAINT chk_elementos_fuente_no_autoreferencia
        CHECK (campo_fuente_id IS NULL OR campo_fuente_id <> elemento_id);

CREATE INDEX idx_elementos_componente_padre ON elementos_tab_cotizador(componente_padre_id)
    WHERE componente_padre_id IS NOT NULL;
CREATE INDEX idx_elementos_campo_fuente ON elementos_tab_cotizador(campo_fuente_id)
    WHERE campo_fuente_id IS NOT NULL;

ALTER TABLE elementos_tab_cotizador
    DROP CONSTRAINT elementos_tab_cotizador_tipo_check;

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT elementos_tab_cotizador_tipo_check
        CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO',
                         'TITULO', 'CONTENEDOR', 'CAJA_VALOR'));
