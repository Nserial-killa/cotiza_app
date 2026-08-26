-- ============================================================
-- COTIZA — Migración 0005: sesiones de autenticación (Sprint 3)
-- POST /api/auth/login empieza a devolver un token; el resto de los
-- endpoints (salvo /health y /auth/login) empiezan a exigirlo via
-- internal/middleware/auth.go. Alcance de este sprint: solo "¿hay
-- sesión válida sí o no?" — ni refresh tokens ni permisos por rol
-- todavía.
-- ============================================================

CREATE TABLE sesiones (
    token                TEXT PRIMARY KEY,
    usuario_id           TEXT NOT NULL REFERENCES usuarios(usuario_id) ON DELETE CASCADE,
    fecha_creacion       TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_expiracion     TIMESTAMPTZ NOT NULL,
    fecha_ultimo_uso     TIMESTAMPTZ
);

CREATE INDEX idx_sesiones_usuario ON sesiones(usuario_id);
CREATE INDEX idx_sesiones_expiracion ON sesiones(fecha_expiracion);
