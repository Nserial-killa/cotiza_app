-- ============================================================
-- COTIZA — Migración 0025: Plantillas condicionales (cuarto y
-- último hueco del documento de definición funcional del jefe,
-- caso ISA Custom).
--
-- Dos capacidades nuevas para plantilla_bloques, ambas por
-- (bloque, calculadora) igual que plantilla_vinculaciones —
-- una plantilla puede estar asociada a varios cotizadores con
-- vocabularios de campos distintos:
--
--   1. plantilla_bloque_condiciones: "mostrar este bloque solo
--      si <condición>". Reusa el mismo vocabulario de operador
--      que reglas_cotizador (migración 0024) — el motor de
--      evaluación es el mismo, ver internal/handlers/
--      reglas_evaluacion.go (evaluarCondicionRegla), reusado
--      desde internal/handlers/plantilla_renderizador.go.
--
--   2. origen_filas en plantilla_bloques + plantilla_tabla_
--      columnas: para un bloque TABLA_INVERSION, si sus filas
--      son fijas (una sola fila, tantas columnas como se
--      agreguen) o una fila por cada Opción de Propuesta de la
--      cotización. fuente_tipo NOMBRE_ESCENARIO/ES_RECOMENDADA
--      son columnas virtuales sin fuente_id: vienen de
--      cotizacion_opciones, no de un campo del cotizador.
-- ============================================================

CREATE TABLE plantilla_bloque_condiciones (
    condicion_id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bloque_id           UUID NOT NULL REFERENCES plantilla_bloques(bloque_id) ON DELETE CASCADE,
    calculadora_id      TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    fuente_tipo         TEXT NOT NULL CHECK (fuente_tipo IN ('CAMPO', 'COTIZACION_BASE')),
    fuente_id           TEXT NOT NULL,
    operador            TEXT NOT NULL
                            CHECK (operador IN ('IGUAL_A', 'DISTINTO_DE', 'MAYOR_QUE', 'MENOR_QUE',
                                                 'MAYOR_O_IGUAL_QUE', 'MENOR_O_IGUAL_QUE',
                                                 'ESTA_VACIO', 'NO_ESTA_VACIO')),
    -- Obligatorio salvo ESTA_VACIO/NO_ESTA_VACIO — Go lo valida y limpia a
    -- NULL para esos dos operadores, mismo criterio que reglas_cotizador.
    valor_comparacion   TEXT,
    fecha_creacion      TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (bloque_id, calculadora_id)
);

CREATE INDEX idx_plantilla_bloque_condiciones_calculadora
    ON plantilla_bloque_condiciones(calculadora_id);

CREATE TRIGGER trg_plantilla_bloque_condiciones_fecha
    BEFORE UPDATE ON plantilla_bloque_condiciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

ALTER TABLE plantilla_bloques
    ADD COLUMN origen_filas TEXT NOT NULL DEFAULT 'FIJO'
        CHECK (origen_filas IN ('FIJO', 'OPCIONES_PROPUESTA'));

CREATE TABLE plantilla_tabla_columnas (
    columna_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bloque_id           UUID NOT NULL REFERENCES plantilla_bloques(bloque_id) ON DELETE CASCADE,
    calculadora_id      TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    titulo              TEXT NOT NULL,
    fuente_tipo         TEXT NOT NULL
                            CHECK (fuente_tipo IN ('CAMPO', 'COTIZACION_BASE', 'NOMBRE_ESCENARIO', 'ES_RECOMENDADA')),
    -- NULL solo para las dos fuentes virtuales; CAMPO/COTIZACION_BASE lo
    -- necesitan (Go lo exige, ver internal/handlers/plantilla_tabla_columnas.go).
    fuente_id           TEXT,
    orden               INTEGER NOT NULL DEFAULT 0,
    fecha_creacion      TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT plantilla_tabla_columnas_fuente_id_check
        CHECK ((fuente_tipo IN ('CAMPO','COTIZACION_BASE') AND fuente_id IS NOT NULL AND btrim(fuente_id) <> '')
            OR (fuente_tipo IN ('NOMBRE_ESCENARIO','ES_RECOMENDADA') AND fuente_id IS NULL))
);

CREATE INDEX idx_plantilla_tabla_columnas_bloque
    ON plantilla_tabla_columnas(bloque_id, calculadora_id, orden);

CREATE TRIGGER trg_plantilla_tabla_columnas_fecha
    BEFORE UPDATE ON plantilla_tabla_columnas
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
