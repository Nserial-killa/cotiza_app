-- ============================================================
-- COTIZA — Migración 0029: campos internos de un bloque de plantilla
-- (Ronda P1: la paleta pasa de 6 a 14 tipos de bloque, para coincidir
-- con la plantilla real de producción de Exceltec).
--
-- plantilla_bloques.tipo_bloque ya es TEXT libre (0010, sin CHECK), así
-- que los tipos nuevos no necesitan tocar esa tabla — la lista válida
-- vive en Go (tiposBloquePlantilla, plantilla_estructura.go).
--
-- Lo que sí faltaba: varios tipos nuevos necesitan VARIOS campos
-- internos, no una sola fuente — plantilla_vinculaciones es
-- UNIQUE(bloque_id, calculadora_id), o sea una fuente por bloque y
-- cotizador. plantilla_bloque_campos es la lista ordenada de pares
-- Etiqueta/Valor de un bloque:
--
--   PORTADA                  Cliente / Contacto / Fecha / Número de oferta
--   DATOS_CLIENTE            Nombre del contacto / Empresa / Correo / Teléfono / Cargo
--   GRUPO_INFORMACION        pares Concepto/Valor libres
--   CONDICIONES_COMERCIALES  Validez / Forma de pago / Plazo / Moneda / Observaciones
--   FIRMA_ACEPTACION         Nombre / Cargo de quien acepta
--
-- A diferencia de plantilla_vinculaciones/plantilla_tabla_columnas,
-- calculadora_id es NULLABLE: solo una fuente CAMPO depende del
-- vocabulario de un cotizador puntual. COTIZACION_BASE (datos comunes
-- a cualquier cotización) y VALOR_FIJO (texto escrito en la plantilla)
-- valen para todos los cotizadores asociados, y así DATOS_CLIENTE/
-- CONDICIONES_COMERCIALES pueden nacer pre-poblados sin depender de qué
-- cotizadores tenga la plantilla en ese momento. Al renderizar se toman
-- los campos comunes (calculadora_id NULL) más los del cotizador de la
-- cotización, en un único orden por bloque.
-- ============================================================

CREATE TABLE plantilla_bloque_campos (
    campo_id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bloque_id           UUID NOT NULL REFERENCES plantilla_bloques(bloque_id) ON DELETE CASCADE,
    calculadora_id      TEXT REFERENCES calculadoras(calculadora_id),
    etiqueta            TEXT NOT NULL CHECK (btrim(etiqueta) <> ''),
    fuente_tipo         TEXT NOT NULL CHECK (fuente_tipo IN ('CAMPO', 'COTIZACION_BASE', 'VALOR_FIJO')),
    -- NULL solo para VALOR_FIJO. Go valida que exista (CAMPO: activo en ese
    -- cotizador; COTIZACION_BASE: una de fuentesCotizacionBase).
    fuente_id           TEXT,
    -- Solo VALOR_FIJO. Puede quedar vacío: los campos pre-poblados de
    -- CONDICIONES_COMERCIALES nacen sin texto para que el equipo los llene.
    valor_fijo          TEXT,
    orden               INTEGER NOT NULL DEFAULT 0 CHECK (orden >= 0),
    fecha_creacion      TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT plantilla_bloque_campos_fuente_check CHECK (
        (fuente_tipo = 'CAMPO' AND calculadora_id IS NOT NULL
             AND fuente_id IS NOT NULL AND btrim(fuente_id) <> '' AND valor_fijo IS NULL)
     OR (fuente_tipo = 'COTIZACION_BASE' AND calculadora_id IS NULL
             AND fuente_id IS NOT NULL AND btrim(fuente_id) <> '' AND valor_fijo IS NULL)
     OR (fuente_tipo = 'VALOR_FIJO' AND calculadora_id IS NULL AND fuente_id IS NULL)
    )
);

CREATE INDEX idx_plantilla_bloque_campos_bloque
    ON plantilla_bloque_campos(bloque_id, orden);
CREATE INDEX idx_plantilla_bloque_campos_calculadora
    ON plantilla_bloque_campos(calculadora_id);

CREATE TRIGGER trg_plantilla_bloque_campos_fecha
    BEFORE UPDATE ON plantilla_bloque_campos
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();
