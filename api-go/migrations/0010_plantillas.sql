-- ============================================================
-- COTIZA — Migración 0010: plantillas de propuesta
-- ============================================================

CREATE TABLE plantillas (
    plantilla_id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    codigo                        TEXT NOT NULL UNIQUE,
    nombre                        TEXT NOT NULL,
    descripcion                   TEXT,
    estado                        TEXT NOT NULL DEFAULT 'Borrador'
                                      CHECK (estado IN ('Borrador', 'Publicada', 'Archivada')),
    version                       INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    organizacion_id               TEXT REFERENCES organizaciones(organizacion_id),
    disponible_nuevas_propuestas BOOLEAN NOT NULL DEFAULT true,
    permite_duplicar              BOOLEAN NOT NULL DEFAULT true,
    fecha_creacion                TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_plantillas_estado ON plantillas(estado);
CREATE INDEX idx_plantillas_organizacion ON plantillas(organizacion_id);

CREATE TRIGGER trg_plantillas_fecha
    BEFORE UPDATE ON plantillas
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE plantilla_calculadoras (
    plantilla_id   UUID NOT NULL REFERENCES plantillas(plantilla_id) ON DELETE CASCADE,
    calculadora_id TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    PRIMARY KEY (plantilla_id, calculadora_id)
);

CREATE INDEX idx_plantilla_calculadoras_calculadora
    ON plantilla_calculadoras(calculadora_id);

CREATE TABLE plantilla_tipos_propuesta (
    plantilla_id  UUID NOT NULL REFERENCES plantillas(plantilla_id) ON DELETE CASCADE,
    tipo_propuesta TEXT NOT NULL,
    PRIMARY KEY (plantilla_id, tipo_propuesta)
);

CREATE INDEX idx_plantilla_tipos_tipo
    ON plantilla_tipos_propuesta(tipo_propuesta);

CREATE TABLE plantilla_secciones (
    seccion_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plantilla_id     UUID NOT NULL REFERENCES plantillas(plantilla_id) ON DELETE CASCADE,
    nombre           TEXT NOT NULL,
    titulo           TEXT,
    mostrar_titulo   BOOLEAN NOT NULL DEFAULT true,
    visibilidad      TEXT NOT NULL DEFAULT 'SIEMPRE'
                         CHECK (visibilidad IN ('SIEMPRE', 'CONDICIONAL')),
    diseno_bloques   TEXT NOT NULL DEFAULT 'UNA'
                         CHECK (diseno_bloques IN ('UNA', 'DOS_50_50', 'DOS_30_70', 'DOS_70_30', 'TRES_IGUALES')),
    mostrar_web      BOOLEAN NOT NULL DEFAULT true,
    mostrar_pdf      BOOLEAN NOT NULL DEFAULT true,
    orden            INTEGER NOT NULL DEFAULT 0 CHECK (orden >= 0),
    fecha_creacion   TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_plantilla_secciones_plantilla
    ON plantilla_secciones(plantilla_id, orden);

CREATE TRIGGER trg_plantilla_secciones_fecha
    BEFORE UPDATE ON plantilla_secciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE plantilla_bloques (
    bloque_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seccion_id         UUID NOT NULL REFERENCES plantilla_secciones(seccion_id) ON DELETE CASCADE,
    tipo_bloque        TEXT NOT NULL,
    nombre_interno     TEXT NOT NULL,
    titulo             TEXT,
    contenido          TEXT,
    columna            INTEGER NOT NULL DEFAULT 0 CHECK (columna BETWEEN 0 AND 3),
    mostrar_web        BOOLEAN NOT NULL DEFAULT true,
    mostrar_pdf        BOOLEAN NOT NULL DEFAULT true,
    orden              INTEGER NOT NULL DEFAULT 0 CHECK (orden >= 0),
    fecha_creacion     TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_plantilla_bloques_seccion
    ON plantilla_bloques(seccion_id, orden);

CREATE TRIGGER trg_plantilla_bloques_fecha
    BEFORE UPDATE ON plantilla_bloques
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE plantilla_vinculaciones (
    vinculacion_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bloque_id      UUID NOT NULL REFERENCES plantilla_bloques(bloque_id) ON DELETE CASCADE,
    calculadora_id TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    fuente_tipo    TEXT NOT NULL CHECK (fuente_tipo IN ('CAMPO', 'COTIZACION_BASE')),
    fuente_id      TEXT NOT NULL,
    fecha_creacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (bloque_id, calculadora_id)
);

CREATE INDEX idx_plantilla_vinculaciones_calculadora
    ON plantilla_vinculaciones(calculadora_id);

CREATE TRIGGER trg_plantilla_vinculaciones_fecha
    BEFORE UPDATE ON plantilla_vinculaciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE plantilla_estilos (
    estilo_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plantilla_id    UUID NOT NULL UNIQUE REFERENCES plantillas(plantilla_id) ON DELETE CASCADE,
    tema             TEXT NOT NULL DEFAULT 'PROFESIONAL',
    formato_pagina   TEXT NOT NULL DEFAULT 'CARTA'
                         CHECK (formato_pagina IN ('CARTA', 'A4')),
    margenes         TEXT NOT NULL DEFAULT 'NORMAL'
                         CHECK (margenes IN ('COMPACTO', 'NORMAL', 'AMPLIO')),
    diseno_portada   TEXT NOT NULL DEFAULT 'BANDA_SUPERIOR'
                         CHECK (diseno_portada IN ('BANDA_SUPERIOR', 'LATERAL', 'MINIMALISTA', 'BLOQUE')),
    estilo_tablas    TEXT NOT NULL DEFAULT 'LINEAS'
                         CHECK (estilo_tablas IN ('LINEAS', 'SUAVE', 'TARJETAS')),
    fecha_creacion   TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_plantilla_estilos_tema ON plantilla_estilos(tema);

CREATE TRIGGER trg_plantilla_estilos_fecha
    BEFORE UPDATE ON plantilla_estilos
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
