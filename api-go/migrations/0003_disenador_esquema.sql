-- ============================================================
-- COTIZA — Migración 0003: Diseñador de Cotizadores (Sprint 2)
-- Tabs/secciones, elementos simples, y catálogo de reglas.
-- ============================================================

CREATE TABLE tabs_cotizador (
    tab_id                TEXT PRIMARY KEY,
    calculadora_id        TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    nombre                TEXT NOT NULL,
    alcance               TEXT NOT NULL DEFAULT 'PROPIO'
                              CHECK (alcance IN ('PROPIO', 'REUTILIZABLE')),
    orden                 INTEGER NOT NULL DEFAULT 0,
    activo                BOOLEAN NOT NULL DEFAULT true,
    fecha_creacion        TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tabs_cotizador_calculadora ON tabs_cotizador(calculadora_id);

CREATE TRIGGER trg_tabs_cotizador_fecha
    BEFORE UPDATE ON tabs_cotizador
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();


CREATE TABLE elementos_tab_cotizador (
    elemento_id           TEXT PRIMARY KEY,
    tab_id                TEXT NOT NULL REFERENCES tabs_cotizador(tab_id) ON DELETE CASCADE,
    tipo                  TEXT NOT NULL
                              CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO')),
    etiqueta              TEXT,
    catalogo_id           TEXT REFERENCES catalogos(catalogo_id),
    columnas_ancho        INTEGER NOT NULL DEFAULT 1 CHECK (columnas_ancho IN (1, 2, 3, 4)),
    orden                 INTEGER NOT NULL DEFAULT 0,
    requerido             BOOLEAN NOT NULL DEFAULT false,
    configuracion         JSONB NOT NULL DEFAULT '{}'::jsonb,
    activo                BOOLEAN NOT NULL DEFAULT true,
    fecha_creacion        TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_elementos_tab ON elementos_tab_cotizador(tab_id);
CREATE INDEX idx_elementos_catalogo ON elementos_tab_cotizador(catalogo_id) WHERE catalogo_id IS NOT NULL;

CREATE TRIGGER trg_elementos_tab_fecha
    BEFORE UPDATE ON elementos_tab_cotizador
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();



CREATE TABLE reglas (
    regla_id              TEXT PRIMARY KEY,
    nombre                TEXT NOT NULL,
    categoria             TEXT,
    tipo                  TEXT,
    descripcion           TEXT,
    severidad             TEXT NOT NULL DEFAULT 'INFORMATIVA'
                              CHECK (severidad IN ('INFORMATIVA', 'ADVERTENCIA', 'BLOQUEANTE', 'APROBACION')),
    momento               TEXT NOT NULL DEFAULT 'AL_CAMBIAR_CAMPO'
                              CHECK (momento IN ('AL_CAMBIAR_CAMPO', 'AL_CARGAR', 'AL_VALIDAR', 'AL_GUARDAR', 'AL_PUBLICAR')),
    mensaje               TEXT,
    orden                 INTEGER NOT NULL DEFAULT 10,
    parametros_schema     JSONB NOT NULL DEFAULT '[]'::jsonb,
    activo                BOOLEAN NOT NULL DEFAULT true,
    fecha_creacion        TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_reglas_categoria ON reglas(categoria);
CREATE INDEX idx_reglas_activo ON reglas(activo);

CREATE TRIGGER trg_reglas_fecha
    BEFORE UPDATE ON reglas
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();