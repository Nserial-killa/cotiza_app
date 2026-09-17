-- ============================================================
-- COTIZA — Migración 0018: Diseñador de Cotizadores, Ronda 2 de 6:
-- Campo Calculado y "Función del campo".
--
-- Este archivo llegó vacío del merge anterior (mismo tipo de quirk
-- que 0008_motot_ejecucion.sql, documentado en CLAUDE.md) — el
-- contenido real nunca se escribió. Se completa acá con lo que
-- describe el pedido de la Ronda 2:
--   - tipo acepta CAMPO_CALCULADO (tipo_formula/operacion/
--     tipo_resultado/decimales/operandos viven en configuracion,
--     igual que columnas/estilo de CONTENEDOR en la migración 0017 —
--     no hacen falta columnas nuevas para eso).
--   - funcion_campo: NOT NULL con default 'NORMAL' (el caso común,
--     "no tiene una función especial") y CHECK con los 9 roles + NORMAL.
--     Qué tipos pueden usar qué funcion_campo (CAMPO/CAMPO_CATALOGO/
--     CAMPO_CALCULADO sí, TITULO/CONTENEDOR/CAJA_VALOR/LEYENDA/
--     TEXTO_INFORMATIVO no) y la unicidad por cotizador se validan en
--     Go (GuardarElemento) — no son expresables como CHECK/UNIQUE de
--     una sola tabla porque calculadora_id vive en tabs_cotizador, no
--     acá.
--   - cotizacion_versiones gana tipo_cambio/subtotal/descuento/
--     impuestos, que junto con moneda (ya existía) y
--     total_precio/total_costo/total_ganancia/margen_total (ya
--     existían) cubren los 9 roles de "Función del campo".
-- ============================================================

ALTER TABLE elementos_tab_cotizador
    DROP CONSTRAINT elementos_tab_cotizador_tipo_check;

ALTER TABLE elementos_tab_cotizador
    ADD CONSTRAINT elementos_tab_cotizador_tipo_check
        CHECK (tipo IN ('CAMPO', 'CAMPO_CATALOGO', 'LEYENDA', 'TEXTO_INFORMATIVO',
                         'TITULO', 'CONTENEDOR', 'CAJA_VALOR', 'CAMPO_CALCULADO'));

ALTER TABLE elementos_tab_cotizador
    ADD COLUMN funcion_campo TEXT NOT NULL DEFAULT 'NORMAL'
        CHECK (funcion_campo IN (
            'NORMAL', 'MONEDA_OFERTA', 'TIPO_CAMBIO', 'SUBTOTAL_OFERTA',
            'DESCUENTO_OFERTA', 'IMPUESTOS_OFERTA', 'TOTAL_PRECIO_OFERTA',
            'TOTAL_COSTO_INTERNO', 'TOTAL_GANANCIA_INTERNA', 'MARGEN_TOTAL'
        ));

ALTER TABLE cotizacion_versiones
    ADD COLUMN tipo_cambio NUMERIC(14,4) NOT NULL DEFAULT 1,
    ADD COLUMN subtotal    NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN descuento   NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN impuestos   NUMERIC(14,2) NOT NULL DEFAULT 0;
