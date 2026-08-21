-- ============================================================
-- COTIZA — Migración 0001: Esquema fundacional
-- Cubre: Organización, Autenticación/Roles, Clientes, CRM, Catálogos.
--
-- Alcance deliberado (Sprint 0/1 del plan de trabajo):
-- Este archivo NO incluye todavía Diseñador/Reglas/Plantillas/
-- Cotizaciones — esas tablas llegan en migraciones 0002+ a medida
-- que esos módulos se implementan. Mantener el esquema alineado
-- al sprint evita rehacer diseño de tablas que aún nadie usa.
--
-- Origen de referencia: BD_Cotizador_Exceltec.xlsx y
-- BD_Cotizador_Parametros.xlsx (NO usar la versión .xlsm, es
-- una copia vieja del esquema).
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto"; -- para gen_random_uuid()

-- ------------------------------------------------------------
-- Función utilitaria: mantiene fecha_actualizacion al día
-- en cualquier tabla que tenga esa columna.
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION set_fecha_actualizacion()
RETURNS TRIGGER AS $$
BEGIN
  NEW.fecha_actualizacion = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ============================================================
-- 1. ORGANIZACIONES
-- ============================================================
CREATE TABLE organizaciones (
    organizacion_id        TEXT PRIMARY KEY,
    nombre                 TEXT NOT NULL,
    razon_social           TEXT,
    identificacion_fiscal  TEXT,
    correo                 TEXT,
    telefono               TEXT,
    sitio_web              TEXT,
    logo_url               TEXT,
    zona_horaria           TEXT DEFAULT 'America/Costa_Rica',
    moneda_base            TEXT DEFAULT 'US$',
    configuracion_json     JSONB DEFAULT '{}'::jsonb,
    estado                 TEXT NOT NULL DEFAULT 'Activo',
    fecha_creacion         TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_organizaciones_fecha
    BEFORE UPDATE ON organizaciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ============================================================
-- 2. ROLES Y USUARIOS
-- ============================================================
CREATE TABLE roles (
    rol                     TEXT PRIMARY KEY,
    descripcion             TEXT,
    puede_crear             BOOLEAN NOT NULL DEFAULT false,
    puede_editar_borrador   BOOLEAN NOT NULL DEFAULT false,
    puede_crear_version     BOOLEAN NOT NULL DEFAULT false,
    puede_ver_price         BOOLEAN NOT NULL DEFAULT false,
    puede_aprobar           BOOLEAN NOT NULL DEFAULT false,
    puede_parametrizar      BOOLEAN NOT NULL DEFAULT false,
    estado                  TEXT NOT NULL DEFAULT 'Activo'
);

-- NOTA DE SEGURIDAD IMPORTANTE:
-- La hoja original guarda "pin_acceso" como número en texto plano.
-- Aquí se guarda únicamente el HASH del PIN (bcrypt/argon2 desde el
-- API de Go), nunca el valor real. No replicar el esquema viejo.
CREATE TABLE usuarios (
    usuario_id              TEXT PRIMARY KEY,
    organizacion_id         TEXT REFERENCES organizaciones(organizacion_id),
    nombre                  TEXT NOT NULL,
    correo                  TEXT NOT NULL UNIQUE,
    pin_hash                TEXT NOT NULL,
    rol                     TEXT NOT NULL REFERENCES roles(rol),
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    puede_ver_gestor        BOOLEAN NOT NULL DEFAULT true,
    ultimo_acceso           TIMESTAMPTZ,
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_usuarios_organizacion ON usuarios(organizacion_id);
CREATE INDEX idx_usuarios_rol ON usuarios(rol);

CREATE TRIGGER trg_usuarios_fecha
    BEFORE UPDATE ON usuarios
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ============================================================
-- 3. CALCULADORAS (Cotizadores) — catálogo maestro de cotizadores
-- ============================================================
CREATE TABLE calculadoras (
    calculadora_id          TEXT PRIMARY KEY,
    nombre_calculadora      TEXT NOT NULL,
    linea_negocio           TEXT,
    servicio_base           TEXT,
    version_actual          TEXT,
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    url_html                TEXT,
    descripcion             TEXT,
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_calculadoras_fecha
    BEFORE UPDATE ON calculadoras
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ------------------------------------------------------------
-- Funciones/roles de un usuario dentro de un cotizador específico
-- ------------------------------------------------------------
CREATE TABLE usuario_funciones (
    usuario_funcion_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organizacion_id         TEXT REFERENCES organizaciones(organizacion_id),
    usuario_id              TEXT NOT NULL REFERENCES usuarios(usuario_id) ON DELETE CASCADE,
    funcion                 TEXT NOT NULL,
    cotizador_id            TEXT REFERENCES calculadoras(calculadora_id),
    es_predeterminado       BOOLEAN NOT NULL DEFAULT false,
    orden                   INTEGER,
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_usuario_funciones_usuario ON usuario_funciones(usuario_id);

CREATE TRIGGER trg_usuario_funciones_fecha
    BEFORE UPDATE ON usuario_funciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ------------------------------------------------------------
-- Permisos de un usuario sobre un cotizador puntual
-- (nota: se elimina la columna "correo" duplicada del original;
--  se resuelve por join contra usuarios.correo).
-- ------------------------------------------------------------
CREATE TABLE usuario_calculadoras (
    usuario_id              TEXT NOT NULL REFERENCES usuarios(usuario_id) ON DELETE CASCADE,
    calculadora_id          TEXT NOT NULL REFERENCES calculadoras(calculadora_id) ON DELETE CASCADE,
    puede_ver               BOOLEAN NOT NULL DEFAULT true,
    puede_crear             BOOLEAN NOT NULL DEFAULT false,
    puede_editar            BOOLEAN NOT NULL DEFAULT false,
    puede_ver_price         BOOLEAN NOT NULL DEFAULT false,
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (usuario_id, calculadora_id)
);

CREATE TRIGGER trg_usuario_calculadoras_fecha
    BEFORE UPDATE ON usuario_calculadoras
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ============================================================
-- 4. CRM / CLIENTES / CONTACTOS
-- ============================================================
CREATE TABLE crm_conexiones (
    crm_conexion_id         TEXT PRIMARY KEY,
    organizacion_id         TEXT REFERENCES organizaciones(organizacion_id),
    tipo_crm                TEXT NOT NULL,          -- ej. 'BITRIX24'
    nombre_conexion         TEXT,
    url_base                TEXT,
    credencial_referencia   TEXT,                   -- referencia a secreto externo, NUNCA la credencial real
    configuracion_json      JSONB DEFAULT '{}'::jsonb,
    sincronizacion_automatica BOOLEAN NOT NULL DEFAULT false,
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    fecha_ultima_sincronizacion TIMESTAMPTZ,
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_crm_conexiones_fecha
    BEFORE UPDATE ON crm_conexiones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE clientes (
    cliente_id              TEXT PRIMARY KEY,
    organizacion_id         TEXT REFERENCES organizaciones(organizacion_id),
    origen                  TEXT,                    -- 'COTIZA' | 'BITRIX24' | ...
    crm_conexion_id         TEXT REFERENCES crm_conexiones(crm_conexion_id),
    crm_tipo                TEXT,
    crm_id                  TEXT,
    tipo_persona            TEXT,
    nombre_comercial        TEXT NOT NULL,
    razon_social            TEXT,
    identificacion          TEXT,
    industria               TEXT,
    pais                    TEXT,
    provincia               TEXT,
    ciudad                  TEXT,
    direccion               TEXT,
    sitio_web               TEXT,
    fecha_ultima_sincronizacion TIMESTAMPTZ,
    sincronizacion_estado   TEXT,
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    usuario_creador_id      TEXT REFERENCES usuarios(usuario_id),
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_clientes_organizacion ON clientes(organizacion_id);
CREATE INDEX idx_clientes_crm ON clientes(crm_tipo, crm_id);

CREATE TRIGGER trg_clientes_fecha
    BEFORE UPDATE ON clientes
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE cliente_contactos (
    contacto_id             TEXT PRIMARY KEY,
    organizacion_id         TEXT REFERENCES organizaciones(organizacion_id),
    cliente_id              TEXT NOT NULL REFERENCES clientes(cliente_id) ON DELETE CASCADE,
    origen                  TEXT,
    crm_conexion_id         TEXT REFERENCES crm_conexiones(crm_conexion_id),
    crm_id                  TEXT,
    nombre                  TEXT NOT NULL,
    cargo                   TEXT,
    correo                  TEXT,
    telefono                TEXT,
    contacto_principal      BOOLEAN NOT NULL DEFAULT false,
    fecha_ultima_sincronizacion TIMESTAMPTZ,
    sincronizacion_estado   TEXT,
    estado                  TEXT NOT NULL DEFAULT 'Activo',
    usuario_creador_id      TEXT REFERENCES usuarios(usuario_id),
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_contactos_cliente ON cliente_contactos(cliente_id);

CREATE TRIGGER trg_contactos_fecha
    BEFORE UPDATE ON cliente_contactos
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ============================================================
-- 5. CATÁLOGOS (simples y padre-hijo) — CTZ-CAT-001 a CAT-014
-- ============================================================
CREATE TABLE catalogos (
    catalogo_id             TEXT PRIMARY KEY,
    nombre_catalogo         TEXT NOT NULL,
    descripcion             TEXT,
    alcance                 TEXT,                    -- ej. 'GLOBAL' | calculadora_id específico
    catalogo_padre_id       TEXT REFERENCES catalogos(catalogo_id),
    orden                   INTEGER DEFAULT 0,
    activo                  BOOLEAN NOT NULL DEFAULT true,
    usuario_actualizacion   TEXT REFERENCES usuarios(usuario_id),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_catalogos_padre ON catalogos(catalogo_padre_id);

CREATE TRIGGER trg_catalogos_fecha
    BEFORE UPDATE ON catalogos
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

CREATE TABLE catalogo_valores (
    valor_id                TEXT PRIMARY KEY,
    catalogo_id             TEXT NOT NULL REFERENCES catalogos(catalogo_id) ON DELETE CASCADE,
    clave                   TEXT,
    texto_visible           TEXT NOT NULL,          -- lo que ve el usuario
    valor_sistema           TEXT NOT NULL,          -- lo que usan cálculos/reglas
    descripcion             TEXT,
    valor_padre_id          TEXT REFERENCES catalogo_valores(valor_id),  -- filtrado padre-hijo (CAT-012/013)
    orden                   INTEGER DEFAULT 0,
    activo                  BOOLEAN NOT NULL DEFAULT true,
    usuario_actualizacion   TEXT REFERENCES usuarios(usuario_id),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_catalogo_valores_catalogo ON catalogo_valores(catalogo_id);
CREATE INDEX idx_catalogo_valores_padre ON catalogo_valores(valor_padre_id);
CREATE INDEX idx_catalogo_valores_activo ON catalogo_valores(catalogo_id, activo);
-- Nota sobre CTZ-CAT-001/CAT-004: el índice anterior es clave para que
-- Vista Previa cargue TODOS los valores activos sin límite artificial.

CREATE TRIGGER trg_catalogo_valores_fecha
    BEFORE UPDATE ON catalogo_valores
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ------------------------------------------------------------
-- Relación explícita catálogo padre → catálogo hijo a nivel de
-- catálogo completo (además del valor_padre_id a nivel de valor).
-- ------------------------------------------------------------
CREATE TABLE catalogo_relaciones (
    relacion_id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalogo_padre_id       TEXT NOT NULL REFERENCES catalogos(catalogo_id) ON DELETE CASCADE,
    valor_padre_id          TEXT NOT NULL REFERENCES catalogo_valores(valor_id) ON DELETE CASCADE,
    catalogo_hijo_id        TEXT NOT NULL REFERENCES catalogos(catalogo_id) ON DELETE CASCADE,
    valor_hijo_id           TEXT NOT NULL REFERENCES catalogo_valores(valor_id) ON DELETE CASCADE,
    orden                   INTEGER DEFAULT 0,
    activo                  BOOLEAN NOT NULL DEFAULT true,
    usuario_actualizacion   TEXT REFERENCES usuarios(usuario_id),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_catalogo_relaciones_padre ON catalogo_relaciones(catalogo_padre_id, valor_padre_id);
CREATE INDEX idx_catalogo_relaciones_hijo ON catalogo_relaciones(catalogo_hijo_id);

CREATE TRIGGER trg_catalogo_relaciones_fecha
    BEFORE UPDATE ON catalogo_relaciones
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- ============================================================
-- Semilla mínima de roles (igual a la hoja Roles original)
-- ============================================================
INSERT INTO roles (rol, descripcion, puede_crear, puede_editar_borrador, puede_crear_version, puede_ver_price, puede_aprobar, puede_parametrizar, estado) VALUES
('Administrador',    'Acceso completo al sistema Cotiza.', true,  true,  true,  true,  true,  true,  'Activo'),
('Gerente Comercial','Crea, edita, versiona y aprueba; ve price interno; no parametriza.', true, true, true, true, true, false, 'Activo'),
('Vendedor',         'Crea cotizaciones y borradores; no ve price interno ni márgenes.', true, true, true, false, false, false, 'Activo'),
('Consultor',        'Apoya alcances y esfuerzos; no ve costos ni márgenes.', true, true, false, false, false, false, 'Activo'),
('Solo Consulta',    'Acceso únicamente de consulta.', false, false, false, false, false, false, 'Activo');
