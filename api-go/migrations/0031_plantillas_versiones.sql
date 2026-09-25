-- ============================================================
-- COTIZA — Migración 0031: versiones de una plantilla (Ronda P5,
-- "Publicación controlada").
--
-- Una plantilla publicada queda bloqueada para edición directa (Go lo
-- valida en cada endpoint que la modifica, ver plantilla_versiones.go):
-- para cambiarla se crea una versión nueva, que es otra fila de
-- plantillas con el MISMO codigo y version+1, en Borrador. Al publicar
-- esa versión, la anterior pasa a Archivada — el renderizador solo usa
-- plantillas Publicadas, así que las ofertas siguen viendo la versión
-- anterior hasta que la nueva se publica. Mismo criterio que
-- cotizadores_compilados: lo publicado no se toca, se versiona.
--
--   * codigo deja de ser único solo: lo es junto con version.
--   * A lo sumo una versión Publicada y una en Borrador por codigo (los
--     índices parciales lo garantizan aunque dos personas publiquen o
--     creen una versión a la vez).
--   * version_anterior_id apunta a la versión de la que se creó.
-- ============================================================

ALTER TABLE plantillas DROP CONSTRAINT plantillas_codigo_key;

ALTER TABLE plantillas
    ADD CONSTRAINT plantillas_codigo_version_key UNIQUE (codigo, version);

ALTER TABLE plantillas
    ADD COLUMN version_anterior_id UUID REFERENCES plantillas(plantilla_id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_plantillas_una_publicada_por_codigo
    ON plantillas(codigo) WHERE estado = 'Publicada';
CREATE UNIQUE INDEX idx_plantillas_un_borrador_por_codigo
    ON plantillas(codigo) WHERE estado = 'Borrador';
