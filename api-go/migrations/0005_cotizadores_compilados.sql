-- ============================================================
-- COTIZA — Migración 0005: versiones compiladas del cotizador
-- ============================================================

CREATE TABLE cotizadores_compilados (
    compilado_id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    calculadora_id         TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    version                INTEGER NOT NULL CHECK (version > 0),
    estado                 TEXT NOT NULL DEFAULT 'ACTIVA'
                               CHECK (estado IN ('ACTIVA', 'ANTERIOR')),
    configuracion          JSONB NOT NULL,
    fecha_creacion         TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (calculadora_id, version)
);

CREATE UNIQUE INDEX uq_cotizadores_compilados_activa
    ON cotizadores_compilados(calculadora_id)
    WHERE estado = 'ACTIVA';

CREATE INDEX idx_cotizadores_compilados_calculadora
    ON cotizadores_compilados(calculadora_id, version DESC);

CREATE TRIGGER trg_cotizadores_compilados_fecha
    BEFORE UPDATE ON cotizadores_compilados
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
