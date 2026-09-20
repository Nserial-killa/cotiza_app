-- ============================================================
-- COTIZA — Migración 0026: totales por Opción de Propuesta.
--
-- Paso 0 de la Ronda de Plantillas condicionales: investigamos qué
-- pasaba cuando un elemento con funcion_campo distinto de NORMAL
-- (Ronda 2, migración 0018) queda anidado bajo Opciones de Propuesta
-- (Ronda 5, migración 0021). Hallazgo: actualizarTotalesCotizacionVersion
-- (internal/handlers/cotizador_runtime.go) no tenía ningún caso para
-- esto — el valor guardado de un elemento así es un mapa opcion_id ->
-- valor, no el escalar que la función esperaba, así que el UPDATE de
-- cotizacion_versiones simplemente no encontraba nada que guardar: ni
-- tomaba la primera opción, ni sumaba, ni daba error, solo se quedaba
-- callado. cotizacion_versiones sigue siendo una sola fila por
-- versión, así que un total "por escenario" no cabe ahí — de ahí estas
-- columnas en cotizacion_opciones en vez de forzarlo en
-- cotizacion_versiones. Ver el fix en cotizador_runtime.go
-- (actualizarTotalesCotizacionVersion / actualizarColumnaCotizacionOpcion).
-- ============================================================

ALTER TABLE cotizacion_opciones
    ADD COLUMN total_precio   NUMERIC(14,2),
    ADD COLUMN total_costo    NUMERIC(14,2),
    ADD COLUMN total_ganancia NUMERIC(14,2),
    ADD COLUMN margen_total   NUMERIC(6,2),
    ADD COLUMN moneda         TEXT,
    ADD COLUMN tipo_cambio    NUMERIC(14,4),
    ADD COLUMN subtotal       NUMERIC(14,2),
    ADD COLUMN descuento      NUMERIC(14,2),
    ADD COLUMN impuestos      NUMERIC(14,2);
