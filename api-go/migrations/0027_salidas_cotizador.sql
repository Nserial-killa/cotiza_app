-- Salidas normalizadas del Anexo Técnico. cotizacion_valores conserva
-- intacto su propósito: valores internos de los campos del runtime.
BEGIN;

CREATE TABLE mapa_salidas_cotizador (
    calculadora_id TEXT NOT NULL REFERENCES calculadoras(calculadora_id) ON DELETE CASCADE,
    clave_salida TEXT NOT NULL CHECK (clave_salida IN
        ('TOTAL_PRECIO','TOTAL_COSTO','TOTAL_GANANCIA','MARGEN_TOTAL','SUBTOTAL','DESCUENTO','IMPUESTOS','MONEDA','TIPO_CLIENTE','TIPO_PROPUESTA')),
    tipo_fuente TEXT NOT NULL CHECK (tipo_fuente IN ('CAMPO','CALCULADO','TOTAL_TABLA','LISTA_PRECIO','ESCENARIO','SALIDA')),
    fuente_id TEXT,
    propiedad_fuente TEXT,
    requerido BOOLEAN NOT NULL DEFAULT false,
    activo BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (calculadora_id, clave_salida)
);

ALTER TABLE cotizacion_versiones ADD COLUMN snapshot_json JSONB;

CREATE TABLE cotizacion_salidas (
    salida_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cotizacion_id TEXT NOT NULL,
    numero_version INTEGER NOT NULL,
    clave_salida TEXT NOT NULL,
    fuente_id TEXT,
    tipo_dato TEXT NOT NULL CHECK (tipo_dato IN ('MONEDA','PORCENTAJE','TEXTO','BOOLEANO','FECHA')),
    valor_numero NUMERIC(20,6),
    valor_texto TEXT,
    valor_booleano BOOLEAN,
    valor_fecha DATE,
    valor_visible TEXT,
    moneda TEXT,
    UNIQUE (cotizacion_id, numero_version, clave_salida),
    FOREIGN KEY (cotizacion_id, numero_version) REFERENCES cotizacion_versiones(cotizacion_id, numero_version) ON DELETE CASCADE,
    CHECK ((tipo_dato IN ('MONEDA','PORCENTAJE') AND valor_numero IS NOT NULL AND valor_texto IS NULL AND valor_booleano IS NULL AND valor_fecha IS NULL)
        OR (tipo_dato='TEXTO' AND valor_texto IS NOT NULL AND valor_numero IS NULL AND valor_booleano IS NULL AND valor_fecha IS NULL)
        OR (tipo_dato='BOOLEANO' AND valor_booleano IS NOT NULL AND valor_numero IS NULL AND valor_texto IS NULL AND valor_fecha IS NULL)
        OR (tipo_dato='FECHA' AND valor_fecha IS NOT NULL AND valor_numero IS NULL AND valor_texto IS NULL AND valor_booleano IS NULL))
);

CREATE TABLE cotizacion_items (
    item_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cotizacion_id TEXT NOT NULL,
    numero_version INTEGER NOT NULL,
    fuente_id TEXT,
    opcion_id TEXT,
    categoria TEXT NOT NULL,
    descripcion TEXT NOT NULL,
    cantidad NUMERIC(20,6) NOT NULL DEFAULT 1,
    precio_unitario NUMERIC(20,6),
    total_precio NUMERIC(20,6),
    total_costo NUMERIC(20,6),
    moneda TEXT,
    detalle_json JSONB NOT NULL DEFAULT '{}',
    orden INTEGER NOT NULL,
    FOREIGN KEY (cotizacion_id, numero_version) REFERENCES cotizacion_versiones(cotizacion_id, numero_version) ON DELETE CASCADE,
    UNIQUE (cotizacion_id, numero_version, orden)
);

CREATE INDEX idx_cotizacion_salidas_clave_version ON cotizacion_salidas(clave_salida, cotizacion_id, numero_version);
CREATE INDEX idx_cotizaciones_estado_fecha ON cotizaciones(estado, fecha_creacion);
-- Ya existe desde 0001; no duplicar el índice físico.
CREATE INDEX IF NOT EXISTS idx_cotizaciones_calculadora ON cotizaciones(calculadora_id);
COMMIT;
