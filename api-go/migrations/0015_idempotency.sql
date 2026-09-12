-- ============================================================
-- COTIZA — Migración 0015: idempotencia de solicitudes externas
--
-- La clave es opcional: las solicitudes sin Idempotency-Key siguen
-- creando una fila en cada llamada. El índice parcial hace atómica
-- la deduplicación cuando dos reintentos llegan al mismo tiempo.
-- ============================================================

ALTER TABLE solicitudes
    ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX uq_solicitudes_idempotency_key
    ON solicitudes(idempotency_key)
    WHERE idempotency_key IS NOT NULL;
