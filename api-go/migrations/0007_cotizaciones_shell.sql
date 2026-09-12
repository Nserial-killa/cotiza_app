-- ============================================================
-- COTIZA — Migración 0007: Gestor de Cotizaciones, cascarón
-- administrativo (Sprint 4).
--
-- Alcance deliberado: listar, ver detalle, cambiar estado, crear una
-- versión nueva. NO cubre llenar una cotización de verdad (eso abre
-- el Cotizador en modo de uso real, que depende de un motor de
-- ejecución que todavía no existe) ni la publicación al cliente
-- (link público) — ver internal/handlers/cotizaciones.go.
--
-- Las cotizaciones nuevas nacen desde una Solicitud (sprint futuro);
-- este esquema no tiene todavía un INSERT propio, solo lectura y
-- transiciones sobre filas que ya existen.
-- ============================================================

-- Lista cerrada de estados: refleja exactamente el arreglo ESTADOS de
-- frontend/legacy-gas/cotiza_scripts.html (línea ~3). Si ese arreglo
-- cambia, este CHECK y el mapa Go en cotizaciones.go tienen que
-- cambiar junto con él.
CREATE TABLE cotizaciones (
    cotizacion_id           TEXT PRIMARY KEY,
    calculadora_id          TEXT NOT NULL REFERENCES calculadoras(calculadora_id),
    cliente_id              TEXT REFERENCES clientes(cliente_id),
    organizacion_id         TEXT REFERENCES organizaciones(organizacion_id),
    codigo_oferta           TEXT,
    tipo_propuesta          TEXT,
    estado                  TEXT NOT NULL DEFAULT 'Borrador'
                                CHECK (estado IN (
                                    'Borrador', 'Revisión Comercial', 'Enviada al Cliente',
                                    'Vista por el Cliente', 'Cambios solicitados', 'Aceptada',
                                    'Ganada', 'Perdida', 'Vencida', 'Cancelada'
                                )),
    version_actual          INTEGER NOT NULL DEFAULT 1,
    version_aceptada        INTEGER,
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cotizaciones_calculadora ON cotizaciones(calculadora_id);
CREATE INDEX idx_cotizaciones_cliente ON cotizaciones(cliente_id);
CREATE INDEX idx_cotizaciones_estado ON cotizaciones(estado);

CREATE TRIGGER trg_cotizaciones_fecha
    BEFORE UPDATE ON cotizaciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ------------------------------------------------------------
-- Una fila por versión de la cotización. "estado" vive acá (fuente de
-- verdad de cada versión); cotizaciones.estado es una copia de la
-- versión activa (version_actual), mantenida por el propio API en el
-- mismo cambio — evita un JOIN extra en cada listado.
-- ------------------------------------------------------------
CREATE TABLE cotizacion_versiones (
    cotizacion_id           TEXT NOT NULL REFERENCES cotizaciones(cotizacion_id) ON DELETE CASCADE,
    numero_version          INTEGER NOT NULL,
    nombre_version          TEXT,
    resumen_cambios         TEXT,
    estado                  TEXT NOT NULL DEFAULT 'Borrador'
                                CHECK (estado IN (
                                    'Borrador', 'Revisión Comercial', 'Enviada al Cliente',
                                    'Vista por el Cliente', 'Cambios solicitados', 'Aceptada',
                                    'Ganada', 'Perdida', 'Vencida', 'Cancelada'
                                )),
    moneda                  TEXT NOT NULL DEFAULT 'US$',
    total_precio            NUMERIC(14,2) NOT NULL DEFAULT 0,
    total_costo             NUMERIC(14,2) NOT NULL DEFAULT 0,
    total_ganancia          NUMERIC(14,2) NOT NULL DEFAULT 0,
    margen_total            NUMERIC(6,2) NOT NULL DEFAULT 0,
    fecha_aceptacion        TIMESTAMPTZ,
    aceptada_por            TEXT,
    origen_aceptacion       TEXT,
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cotizacion_id, numero_version)
);

CREATE TRIGGER trg_cotizacion_versiones_fecha
    BEFORE UPDATE ON cotizacion_versiones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ------------------------------------------------------------
-- Personas asignadas a una cotización (no a una versión puntual —
-- vendedor/analista/líder de producto no cambian de versión en
-- versión). Puede haber más de un usuario por función; el listado
-- los agrega con STRING_AGG.
-- ------------------------------------------------------------
CREATE TABLE cotizacion_usuarios (
    cotizacion_id           TEXT NOT NULL REFERENCES cotizaciones(cotizacion_id) ON DELETE CASCADE,
    usuario_id              TEXT NOT NULL REFERENCES usuarios(usuario_id),
    funcion                 TEXT NOT NULL CHECK (funcion IN ('Vendedor', 'Analista', 'Líder de producto')),
    PRIMARY KEY (cotizacion_id, usuario_id, funcion)
);

CREATE INDEX idx_cotizacion_usuarios_cotizacion ON cotizacion_usuarios(cotizacion_id);

-- ------------------------------------------------------------
-- Bitácora de cambios de estado / versión. numero_version queda NULL
-- cuando el evento no aplica a una versión puntual.
-- ------------------------------------------------------------
CREATE TABLE cotizacion_historial (
    historial_id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cotizacion_id           TEXT NOT NULL REFERENCES cotizaciones(cotizacion_id) ON DELETE CASCADE,
    numero_version          INTEGER,
    accion                  TEXT NOT NULL,
    estado_anterior         TEXT,
    estado_nuevo            TEXT,
    comentario              TEXT,
    usuario_id              TEXT REFERENCES usuarios(usuario_id),
    fecha                   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cotizacion_historial_cotizacion ON cotizacion_historial(cotizacion_id, fecha DESC);
