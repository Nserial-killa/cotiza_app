-- ============================================================
-- COTIZA — Migración 0008: motor de ejecución del cotizador
-- ============================================================

ALTER TABLE cotizaciones
    ADD COLUMN compilado_id_usado UUID REFERENCES cotizadores_compilados(compilado_id);

CREATE INDEX idx_cotizaciones_compilado_usado
    ON cotizaciones(compilado_id_usado)
    WHERE compilado_id_usado IS NOT NULL;

CREATE TABLE cotizacion_valores (
    cotizacion_id           TEXT NOT NULL,
    version                 INTEGER NOT NULL,
    elemento_id             TEXT NOT NULL,
    valor                   JSONB NOT NULL,
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cotizacion_id, version, elemento_id),
    FOREIGN KEY (cotizacion_id, version)
        REFERENCES cotizacion_versiones(cotizacion_id, numero_version)
        ON DELETE CASCADE
);

CREATE INDEX idx_cotizacion_valores_version
    ON cotizacion_valores(cotizacion_id, version);

CREATE TRIGGER trg_cotizacion_valores_fecha
    BEFORE UPDATE ON cotizacion_valores
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
