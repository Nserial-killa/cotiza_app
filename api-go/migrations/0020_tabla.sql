-- ============================================================
-- COTIZA — Migración 0020: Diseñador de Cotizadores, Ronda 4 de 6:
-- Tabla.
--
-- Las columnas de una Tabla, a diferencia de los ítems de Lista de
-- Precios (Ronda 3, propios de cada elemento pero sin referencia a
-- otro elemento), pueden REUSAR un campo ya existente de la misma
-- sección (origen=CAMPO_EXISTENTE) o ser propias de esta tabla
-- (origen=PROPIA). El CHECK chk_tabla_columnas_forma obliga a que
-- cada fila tenga exactamente la forma que le corresponde a su
-- origen — ni una columna CAMPO_EXISTENTE con tipo_dato/etiqueta
-- propios, ni una PROPIA sin ellos.
--
-- La configuración de la Tabla en sí (tipo_tabla, etiqueta_total,
-- unidad, permitir_agregar_filas, etc.) vive en
-- elementos_tab_cotizador.configuracion — mismo patrón que Lista de
-- Precios — así que no hace falta ninguna columna nueva ahí.
-- ============================================================

ALTER TABLE elementos_tab_cotizador
    DROP CONSTRAINT elementos_tab_cotizador_tipo_check;

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT elementos_tab_cotizador_tipo_check
        CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO',
                         'TITULO', 'CONTENEDOR', 'CAJA_VALOR', 'CAMPO_CALCULADO',
                         'LISTA_PRECIOS', 'TABLA'));

CREATE TABLE tabla_columnas (
    columna_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    elemento_id         TEXT NOT NULL REFERENCES elementos_tab_cotizador(elemento_id) ON DELETE CASCADE,
    origen              TEXT NOT NULL CHECK (origen IN ('CAMPO_EXISTENTE', 'PROPIA')),
    campo_existente_id  TEXT REFERENCES elementos_tab_cotizador(elemento_id),
    tipo_dato           TEXT CHECK (tipo_dato IN ('TEXTO', 'NUMERO', 'MONEDA', 'PORCENTAJE')),
    etiqueta            TEXT,
    orden               INTEGER NOT NULL DEFAULT 0,
    fecha_creacion      TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_tabla_columnas_forma CHECK (
        (origen = 'CAMPO_EXISTENTE' AND campo_existente_id IS NOT NULL AND tipo_dato IS NULL AND etiqueta IS NULL)
        OR
        (origen = 'PROPIA' AND campo_existente_id IS NULL AND tipo_dato IS NOT NULL AND etiqueta IS NOT NULL)
    )
);

CREATE INDEX idx_tabla_columnas_elemento ON tabla_columnas(elemento_id);

CREATE TRIGGER trg_tabla_columnas_fecha
    BEFORE UPDATE ON tabla_columnas
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
