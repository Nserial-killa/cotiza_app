-- ============================================================
-- COTIZA — Migración 0022: Diseñador de Cotizadores, Ronda 6 de 6:
-- Secciones Adicionales reutilizables entre cotizadores.
-- ============================================================

ALTER TABLE elementos_tab_cotizador
    DROP CONSTRAINT elementos_tab_cotizador_tipo_check;

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT elementos_tab_cotizador_tipo_check
        CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO',
                        'TITULO', 'CONTENEDOR', 'CAJA_VALOR', 'CAMPO_CALCULADO',
                        'LISTA_PRECIOS', 'TABLA', 'OPCIONES_PROPUESTA',
                        'SECCIONES_ADICIONALES'));

-- El alcance existe desde 0003. Esta migración agrega la relación explícita:
-- el elemento selector identifica quién incorporó la sección y permite que
-- al eliminarlo desaparezcan solamente sus vínculos, nunca la sección fuente.
CREATE TABLE tabs_cotizador_asociaciones (
    asociacion_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    calculadora_id     TEXT NOT NULL REFERENCES calculadoras(calculadora_id) ON DELETE CASCADE,
    elemento_id        TEXT NOT NULL REFERENCES elementos_tab_cotizador(elemento_id) ON DELETE CASCADE,
    tab_id             TEXT NOT NULL REFERENCES tabs_cotizador(tab_id) ON DELETE CASCADE,
    orden              INTEGER NOT NULL DEFAULT 0,
    fecha_creacion     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (calculadora_id, tab_id),
    UNIQUE (elemento_id, tab_id)
);

CREATE INDEX idx_tabs_cotizador_asociaciones_elemento
    ON tabs_cotizador_asociaciones(elemento_id, orden, tab_id);

CREATE INDEX idx_tabs_cotizador_asociaciones_calculadora
    ON tabs_cotizador_asociaciones(calculadora_id, orden, tab_id);
