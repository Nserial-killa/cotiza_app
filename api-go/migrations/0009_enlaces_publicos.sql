-- ============================================================
-- COTIZA — Migración 0009: enlaces públicos de la cotización
-- (Vista Previa / enlace al cliente — versión acotada: solo el
-- token y la lectura pública de una versión ya llenada. El
-- diseñador de plantillas personalizable (colores, secciones
-- arrastrables) queda para una fase posterior — ver
-- internal/handlers/enlaces_publicos.go).
--
-- Un enlace queda atado a una cotización + versión puntual, no a
-- "la versión actual" — así el cliente sigue viendo lo que se le
-- envió aunque el vendedor cree una versión nueva después. Por eso
-- el UNIQUE es (cotizacion_id, version): pedir un enlace para la
-- misma versión dos veces reutiliza el mismo token en vez de
-- acumular enlaces de sobra.
-- ============================================================

CREATE TABLE cotizacion_enlaces_publicos (
    token                   TEXT PRIMARY KEY,
    cotizacion_id           TEXT NOT NULL REFERENCES cotizaciones(cotizacion_id) ON DELETE CASCADE,
    version                 INTEGER NOT NULL,
    creado_por              TEXT REFERENCES usuarios(usuario_id),
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_expiracion        TIMESTAMPTZ,
    ultima_visita           TIMESTAMPTZ,
    visitas                 INTEGER NOT NULL DEFAULT 0,
    UNIQUE (cotizacion_id, version),
    FOREIGN KEY (cotizacion_id, version)
        REFERENCES cotizacion_versiones(cotizacion_id, numero_version)
        ON DELETE CASCADE
);

CREATE INDEX idx_enlaces_publicos_cotizacion ON cotizacion_enlaces_publicos(cotizacion_id);
