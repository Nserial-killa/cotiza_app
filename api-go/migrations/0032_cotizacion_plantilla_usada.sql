-- ============================================================
-- COTIZA — Migración 0032: plantilla fijada por versión de cotización
--
-- Hasta acá el enlace público resolvía siempre "la plantilla Publicada
-- ahora", así que una oferta ya enviada cambiaba de aspecto si alguien
-- publicaba una versión nueva de la plantilla. Ahora, la PRIMERA vez que
-- se genera el enlace público de una cotización+versión, se graba acá qué
-- plantilla (la fila puntual de esa versión, ver 0031) le correspondía en
-- ese momento; desde entonces el enlace y la Vista Previa usan esa, aunque
-- después quede Archivada. Mismo criterio que cotizaciones.compilado_id_usado
-- con el cotizador compilado.
--
--   * Nullable: una versión que nunca generó enlace no tiene nada fijado y
--     se sigue resolviendo en vivo.
--   * plantilla_id_usada ya identifica la versión (cada versión es su propia
--     fila de plantillas); plantilla_version_usada se guarda para leerla sin
--     JOIN y para auditoría. Van juntas o ninguna.
--   * Sin ON DELETE: una plantilla fijada fue Publicada, y solo un Borrador
--     se puede eliminar (plantillas.go), así que nunca debería llegar a
--     borrarse; si alguien lo intenta a mano, la FK lo frena.
-- ============================================================

ALTER TABLE cotizacion_versiones
    ADD COLUMN plantilla_id_usada UUID REFERENCES plantillas(plantilla_id),
    ADD COLUMN plantilla_version_usada INTEGER,
    ADD CONSTRAINT chk_cotizacion_versiones_plantilla_usada
        CHECK ((plantilla_id_usada IS NULL) = (plantilla_version_usada IS NULL));

CREATE INDEX idx_cotizacion_versiones_plantilla_usada
    ON cotizacion_versiones(plantilla_id_usada) WHERE plantilla_id_usada IS NOT NULL;
