-- Respuestas y evidencia de aceptación de una versión enviada al cliente.
ALTER TABLE cotizacion_enlaces_publicos ADD COLUMN correo_destinatario TEXT;

CREATE TABLE cotizacion_verificaciones_aceptacion (
    token TEXT PRIMARY KEY REFERENCES cotizacion_enlaces_publicos(token) ON DELETE CASCADE,
    codigo_hash TEXT NOT NULL,
    nombre TEXT NOT NULL,
    cargo TEXT,
    correo TEXT NOT NULL,
    vence_en TIMESTAMPTZ NOT NULL,
    enviado_en TIMESTAMPTZ NOT NULL DEFAULT now(),
    intentos INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE cotizacion_respuestas_cliente (
    respuesta_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token TEXT NOT NULL REFERENCES cotizacion_enlaces_publicos(token) ON DELETE CASCADE,
    tipo TEXT NOT NULL CHECK (tipo IN ('COMENTARIO','CAMBIOS','RECHAZO')),
    nombre TEXT NOT NULL,
    correo TEXT,
    comentario TEXT NOT NULL,
    ip_publica TEXT,
    fecha TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cotizacion_aceptaciones_cliente (
    token TEXT PRIMARY KEY REFERENCES cotizacion_enlaces_publicos(token) ON DELETE CASCADE,
    cotizacion_id TEXT NOT NULL,
    numero_version INTEGER NOT NULL,
    nombre_firmante TEXT NOT NULL,
    cargo_firmante TEXT,
    correo_firmante TEXT NOT NULL,
    codigo_constancia TEXT NOT NULL UNIQUE,
    documento_sha256 TEXT NOT NULL,
    ip_publica TEXT,
    agente_usuario TEXT,
    fecha TIMESTAMPTZ NOT NULL DEFAULT now(),
    correo_confirmacion_enviado TIMESTAMPTZ,
    UNIQUE (cotizacion_id, numero_version),
    FOREIGN KEY (cotizacion_id, numero_version)
        REFERENCES cotizacion_versiones(cotizacion_id, numero_version) ON DELETE CASCADE
);

CREATE INDEX idx_respuestas_cliente_token_fecha ON cotizacion_respuestas_cliente(token, fecha DESC);
