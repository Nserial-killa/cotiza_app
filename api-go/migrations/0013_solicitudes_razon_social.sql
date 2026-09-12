-- ============================================================
-- COTIZA — Migración 0013: razón social propuesta en solicitudes
--
-- Hasta ahora, al convertir una solicitud en cotización, el mismo
-- texto de cliente_nombre se copiaba a clientes.nombre_comercial Y
-- clientes.razon_social. Esta columna permite capturar, desde el
-- alta manual de la solicitud, una razón social distinta del nombre
-- comercial, para que la conversión (SolicitudesHandler.Convertir)
-- la use en vez de duplicar cliente_nombre. Nullable: si no se
-- indica, Convertir sigue cayendo al mismo nombre que antes.
-- ============================================================

ALTER TABLE solicitudes
    ADD COLUMN cliente_razon_social TEXT;
