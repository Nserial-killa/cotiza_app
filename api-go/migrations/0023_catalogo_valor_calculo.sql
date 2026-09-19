-- ============================================================
-- COTIZA — Migración 0023: Catálogos con valor_calculo tipado.
--
-- Este archivo llegó vacío del merge anterior (mismo quirk que
-- 0008_motot_ejecucion.sql y 0018_campo_calculado.sql, documentado en
-- CLAUDE.md) — el contenido real nunca se escribió. Se completa acá con
-- el primero de los 4 huecos del documento de definición funcional del
-- jefe (caso ISA Custom):
--
--   "Cambiar una etiqueta visible no debe romper reglas ni fórmulas. La
--   referencia estable es el código; para cálculos se consume
--   valor_calculo."
--
-- catalogos.tipo_calculo clasifica el catálogo completo: SIN_VALOR
-- (default, no rompe catálogos existentes — son solo descriptivos, ej.
-- CAT_TIPO_AGENTE), NUMERO (ej. CAT_COMPLEJIDAD: BASICA=250, MEDIA=500)
-- o PORCENTAJE (ej. CAT_MARGEN: M20=0.20, M30=0.30 — se guarda como
-- decimal, no como 20/30).
--
-- catalogo_valores.valor_calculo es NUMERIC nullable: obligatorio u
-- obligatoriamente ausente según el tipo_calculo del catálogo dueño, pero
-- esa regla se valida en Go (GuardarValor en catalogos.go), no acá,
-- porque depende de una fila de OTRA tabla y un CHECK de Postgres no
-- puede expresar eso sin un trigger — mismo criterio que ya se usa para
-- funcion_campo/tiposConFuncionCampo en cotizador_tabs.go.
-- ============================================================

ALTER TABLE catalogos
    ADD COLUMN tipo_calculo TEXT NOT NULL DEFAULT 'SIN_VALOR'
        CHECK (tipo_calculo IN ('SIN_VALOR', 'NUMERO', 'PORCENTAJE'));

ALTER TABLE catalogo_valores
    ADD COLUMN valor_calculo NUMERIC(14, 4);
