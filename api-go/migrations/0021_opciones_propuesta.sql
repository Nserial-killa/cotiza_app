-- ============================================================
-- COTIZA — Migración 0021: Diseñador de Cotizadores, Ronda 5 de 6:
-- Opciones de Propuesta.
--
-- Los valores existentes permanecen como valores globales de la versión
-- (opcion_id IS NULL). Los valores de componentes hijos de una opción usan
-- la misma combinación elemento/opción sin reemplazar los de las demás.
-- ============================================================

ALTER TABLE elementos_tab_cotizador
    DROP CONSTRAINT elementos_tab_cotizador_tipo_check;

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT elementos_tab_cotizador_tipo_check
        CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO',
                         'TITULO', 'CONTENEDOR', 'CAJA_VALOR', 'CAMPO_CALCULADO',
                         'LISTA_PRECIOS', 'TABLA', 'OPCIONES_PROPUESTA'));

CREATE TABLE cotizacion_opciones (
    opcion_id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    cotizacion_id      TEXT NOT NULL,
    numero_version     INTEGER NOT NULL,
    elemento_padre_id  TEXT NOT NULL REFERENCES elementos_tab_cotizador(elemento_id),
    nombre             TEXT NOT NULL,
    es_recomendada     BOOLEAN NOT NULL DEFAULT FALSE,
    orden              INTEGER NOT NULL,
    fecha_creacion     TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (cotizacion_id, numero_version)
        REFERENCES cotizacion_versiones(cotizacion_id, numero_version)
        ON DELETE CASCADE,
    CONSTRAINT cotizacion_opciones_orden_positivo CHECK (orden > 0),
    CONSTRAINT cotizacion_opciones_nombre_no_vacio CHECK (btrim(nombre) <> ''),
    UNIQUE (opcion_id, cotizacion_id, numero_version),
    UNIQUE (cotizacion_id, numero_version, elemento_padre_id, orden)
);

CREATE UNIQUE INDEX uq_cotizacion_opciones_recomendada
    ON cotizacion_opciones(cotizacion_id, numero_version, elemento_padre_id)
    WHERE es_recomendada;

CREATE INDEX idx_cotizacion_opciones_padre
    ON cotizacion_opciones(cotizacion_id, numero_version, elemento_padre_id, orden);

CREATE TRIGGER trg_cotizacion_opciones_fecha
    BEFORE UPDATE ON cotizacion_opciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

ALTER TABLE cotizacion_valores
    ADD COLUMN valor_id UUID DEFAULT gen_random_uuid(),
    ADD COLUMN opcion_id TEXT;

-- Nombre confirmado contra la base real antes de escribir esta migración:
-- cotizacion_valores_pkey (cotizacion_id, version, elemento_id).
ALTER TABLE cotizacion_valores
    DROP CONSTRAINT cotizacion_valores_pkey,
    ALTER COLUMN valor_id SET NOT NULL,
    ADD CONSTRAINT cotizacion_valores_pkey PRIMARY KEY (valor_id),
    ADD CONSTRAINT cotizacion_valores_opcion_fkey
        FOREIGN KEY (opcion_id, cotizacion_id, version)
        REFERENCES cotizacion_opciones(opcion_id, cotizacion_id, numero_version)
        ON DELETE CASCADE;

CREATE UNIQUE INDEX uq_cotizacion_valores_sin_opcion
    ON cotizacion_valores(cotizacion_id, version, elemento_id)
    WHERE opcion_id IS NULL;

CREATE UNIQUE INDEX uq_cotizacion_valores_por_opcion
    ON cotizacion_valores(cotizacion_id, version, elemento_id, opcion_id)
    WHERE opcion_id IS NOT NULL;

CREATE INDEX idx_cotizacion_valores_opcion
    ON cotizacion_valores(opcion_id)
    WHERE opcion_id IS NOT NULL;
