-- ============================================================
-- COTIZA — Migración 0012: campos de la gestión manual de solicitudes
--
-- La pantalla operativa distingue prioridad y responsables, pero su
-- "Etapa" corresponde al estado que ya existe. Por eso no se agrega
-- una segunda columna que duplique ese concepto.
-- ============================================================

ALTER TABLE solicitudes
    ADD COLUMN titulo TEXT,
    ADD COLUMN crm_id TEXT,
    ADD COLUMN fecha_requerida DATE,
    ADD COLUMN prioridad TEXT NOT NULL DEFAULT 'Media'
        CHECK (prioridad IN ('Baja', 'Media', 'Alta', 'Urgente')),
    ADD COLUMN vendedor_id TEXT REFERENCES usuarios(usuario_id),
    ADD COLUMN analista_id TEXT REFERENCES usuarios(usuario_id),
    ADD COLUMN lider_producto_id TEXT REFERENCES usuarios(usuario_id),
    ADD COLUMN creado_por TEXT REFERENCES usuarios(usuario_id);

CREATE INDEX idx_solicitudes_prioridad ON solicitudes(prioridad);
CREATE INDEX idx_solicitudes_vendedor ON solicitudes(vendedor_id);
CREATE INDEX idx_solicitudes_analista ON solicitudes(analista_id);
CREATE INDEX idx_solicitudes_lider_producto ON solicitudes(lider_producto_id);
