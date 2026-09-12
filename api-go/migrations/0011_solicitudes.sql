-- ============================================================
-- COTIZA — Migración 0011: Solicitudes + integraciones de API externo
--
-- Cubre dos conceptos nuevos:
--   1. integraciones_api: claves de acceso de vida larga para que un
--      sistema externo (Bitrix24 u otro) llame al API sin un login de
--      usuario. La clave real solo existe en el momento en que se
--      genera (ver internal/handlers/integraciones.go); acá solo se
--      guarda su hash bcrypt, igual que usuarios.pin_hash.
--   2. solicitudes: lo que ese sistema externo crea al llamar
--      POST /api/externo/solicitudes, y que el equipo de Cotiza
--      revisa y eventualmente convierte en una cotización real.
--
-- Alcance deliberado de este sprint: la única forma de crear una
-- solicitud es vía API externo (origen='API_EXTERNA'). Crear una
-- solicitud manual desde la pantalla de Cotiza, y llamar HACIA un CRM
-- externo (en vez de solo recibir llamadas), quedan fuera de alcance
-- a propósito — no se diseña nada para eso todavía.
-- ============================================================

-- ------------------------------------------------------------
-- 1. INTEGRACIONES DE API EXTERNO
-- ------------------------------------------------------------
CREATE TABLE integraciones_api (
    integracion_id  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nombre          TEXT NOT NULL,
    api_key_hash    TEXT NOT NULL,
    estado          TEXT NOT NULL DEFAULT 'Activo' CHECK (estado IN ('Activo', 'Inactivo')),
    creado_por      TEXT REFERENCES usuarios(usuario_id),
    fecha_creacion  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ultima_uso      TIMESTAMPTZ
);

CREATE INDEX idx_integraciones_api_estado ON integraciones_api(estado);

-- ------------------------------------------------------------
-- 2. SOLICITUDES
-- ------------------------------------------------------------
CREATE TABLE solicitudes (
    solicitud_id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    origen                  TEXT NOT NULL, -- 'API_EXTERNA' (única forma de crear una solicitud, por ahora)
    integracion_id          UUID REFERENCES integraciones_api(integracion_id),
    cliente_id              TEXT REFERENCES clientes(cliente_id),
    cliente_nombre          TEXT NOT NULL,
    contacto_nombre         TEXT,
    contacto_correo         TEXT,
    contacto_telefono       TEXT,
    calculadora_id          TEXT REFERENCES calculadoras(calculadora_id),
    descripcion             TEXT,
    estado                  TEXT NOT NULL DEFAULT 'Nueva'
                            CHECK (estado IN ('Nueva', 'En revisión', 'Descartada', 'Convertida')),
    cotizacion_id_generada  TEXT REFERENCES cotizaciones(cotizacion_id),
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_solicitudes_estado ON solicitudes(estado);

CREATE TRIGGER trg_solicitudes_fecha
    BEFORE UPDATE ON solicitudes
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
