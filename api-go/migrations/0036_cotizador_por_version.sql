-- Una cotización puede cambiar de cotizador creando una versión nueva.
-- La versión anterior conserva su estructura y su enlace público.
BEGIN;
ALTER TABLE cotizaciones
    ADD COLUMN creado_por TEXT REFERENCES usuarios(usuario_id);

UPDATE cotizaciones c SET creado_por = (
    SELECT h.usuario_id FROM cotizacion_historial h
     WHERE h.cotizacion_id=c.cotizacion_id AND h.accion='creada' AND h.usuario_id IS NOT NULL
     ORDER BY h.fecha, h.historial_id LIMIT 1
);

ALTER TABLE cotizacion_versiones
    ADD COLUMN calculadora_id TEXT REFERENCES calculadoras(calculadora_id),
    ADD COLUMN compilado_id_usado UUID REFERENCES cotizadores_compilados(compilado_id);

UPDATE cotizacion_versiones v SET
    calculadora_id=c.calculadora_id,
    compilado_id_usado=CASE
        WHEN v.snapshot_json->>'compilado_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN (v.snapshot_json->>'compilado_id')::uuid
        ELSE c.compilado_id_usado END
  FROM cotizaciones c WHERE c.cotizacion_id=v.cotizacion_id;

CREATE INDEX idx_cotizacion_versiones_calculadora
    ON cotizacion_versiones(calculadora_id);
COMMIT;
