-- ============================================================
-- COTIZA — Migración 0019: Diseñador de Cotizadores, Ronda 3 de 6:
-- Lista de Precios.
--
-- A diferencia de los catálogos (catalogos/catalogo_valores, globales
-- y reutilizables entre cotizadores), los ítems de una Lista de
-- Precios viven DENTRO de un elemento puntual: cada LISTA_PRECIOS
-- tiene los suyos propios, no se comparten. Por eso lista_precios_items
-- referencia directo a elementos_tab_cotizador y no a un catálogo.
--
-- item_id es UUID (tabla de detalle, no una entidad de negocio con ID
-- propio como calculadora_id/elemento_id) y "codigo" es el identificador
-- legible que el Diseñador teclea (ej. "OUT-MT") — único DENTRO del
-- elemento, no global, de ahí el UNIQUE(elemento_id, codigo).
-- ============================================================

ALTER TABLE elementos_tab_cotizador
    DROP CONSTRAINT elementos_tab_cotizador_tipo_check;

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT elementos_tab_cotizador_tipo_check
        CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO',
                         'TITULO', 'CONTENEDOR', 'CAJA_VALOR', 'CAMPO_CALCULADO',
                         'LISTA_PRECIOS'));

CREATE TABLE lista_precios_items (
    item_id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    elemento_id         TEXT NOT NULL REFERENCES elementos_tab_cotizador(elemento_id) ON DELETE CASCADE,
    codigo              TEXT NOT NULL,
    nombre              TEXT NOT NULL,
    descripcion         TEXT,
    precio              NUMERIC(14,2) NOT NULL DEFAULT 0,
    moneda              TEXT NOT NULL DEFAULT 'US$',
    unidad_cobro        TEXT,
    costo_interno       NUMERIC(14,2) NOT NULL DEFAULT 0,
    margen_porcentaje   NUMERIC(6,2) NOT NULL DEFAULT 0,
    orden               INTEGER NOT NULL DEFAULT 0,
    activo              BOOLEAN NOT NULL DEFAULT true,
    fecha_creacion      TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (elemento_id, codigo)
);

CREATE INDEX idx_lista_precios_items_elemento ON lista_precios_items(elemento_id);
CREATE INDEX idx_lista_precios_items_activo ON lista_precios_items(elemento_id, activo);

CREATE TRIGGER trg_lista_precios_items_fecha
    BEFORE UPDATE ON lista_precios_items
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
