-- ============================================================
-- COTIZA — Migración 0028: columnas totalizables explícitas en Tabla
-- (TAB-003, CTZ-TBL-002, Ronda D).
--
-- Hasta ahora el total de una Tabla se inferia siempre de "la primera
-- columna con tipo_dato NUMERO o MONEDA" (primeraColumnaNumericaTabla,
-- calculo.go). Con "totalizable" el diseñador puede marcar explícitamente
-- qué columna(s) se suman; si ninguna queda marcada, el compilador sigue
-- infiriendo la primera numérica (compatibilidad con cotizadores ya
-- publicados) pero avisa con TABLA_TOTALES_COLUMNAS_INFERIDAS al validar.
-- ============================================================

ALTER TABLE tabla_columnas
    ADD COLUMN totalizable BOOLEAN NOT NULL DEFAULT false;
