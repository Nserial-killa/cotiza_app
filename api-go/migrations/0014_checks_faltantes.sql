-- ============================================================
-- COTIZA — Migración 0014: CHECK faltantes en columnas de estado.
--
-- La auditoría de QA (docs/AUDITORIA_QA.md, dimensión 7 "Database
-- Integrity") encontró 24 de 26 CHECK esperados: estas 5 columnas
-- nunca tuvieron restricción a nivel de esquema, solo se validaban en
-- Go (catalogos.alcance ni eso). Antes de escribir esta migración se
-- confirmó con SELECT DISTINCT sobre los datos sembrados (0002 +
-- entorno local) que hoy no hay ningún valor "sucio": clientes.estado
-- y usuarios.estado solo tienen 'Activo' sembrado, y calculadoras,
-- crm_conexiones y catalogos no tienen filas todavía. El conjunto de
-- cada CHECK no sale de esa muestra (sería insuficiente) sino del
-- código que efectivamente escribe cada columna — ver el comentario
-- de cada bloque.
-- ============================================================

-- clientes.estado: mismo conjunto que estadosClienteValidos en
-- internal/handlers/clientes.go (Crear/Editar rechazan cualquier otro
-- valor con 400 antes de llegar a la base).
ALTER TABLE clientes
    ADD CONSTRAINT chk_clientes_estado CHECK (estado IN ('Activo', 'Inactivo'));

-- usuarios.estado: PATCH /api/usuarios/{id} (cotiza_scripts.html,
-- botón activar/desactivar) solo manda estos dos valores, y auth.go
-- trata cualquier valor distinto de 'Activo' como bloqueado al hacer
-- login.
ALTER TABLE usuarios
    ADD CONSTRAINT chk_usuarios_estado CHECK (estado IN ('Activo', 'Inactivo'));

-- calculadoras.estado: 'Activo' e 'Inactivo' vienen del Excel migrado
-- (migrar_calculadoras); 'Borrador' es un valor de filtro que ya
-- expone el combo de Parámetros en cotiza_scripts.html; 'Publicado' lo
-- escribe compilador.go al compilar (UPDATE calculadoras SET
-- estado='Publicado'), y calculadoras.go depende de que 'Activo' y
-- 'Publicado' sigan siendo válidos para poblar el selector de
-- cotizador.
ALTER TABLE calculadoras
    ADD CONSTRAINT chk_calculadoras_estado CHECK (estado IN ('Activo', 'Inactivo', 'Borrador', 'Publicado'));

-- crm_conexiones.estado: tabla sembrada en 0001 pero sin ningún
-- handler ni script de migración que la use todavía (no hay una
-- integración CRM real construida) — no es un valor sucio, es una
-- tabla sin escritura propia. Se deja en el mismo par Activo/Inactivo
-- que usa el resto del esquema para "estado" simple.
ALTER TABLE crm_conexiones
    ADD CONSTRAINT chk_crm_conexiones_estado CHECK (estado IN ('Activo', 'Inactivo'));

-- catalogos.alcance: nace en 0001 documentado en un comentario como
-- 'GLOBAL' | calculadora_id específico, pero el formulario real
-- (cotiza_scripts.html, selectStatic del catálogo) y
-- cotizador_app.html ya lo restringieron a estos dos valores fijos
-- ("Cotizador guarda alcance COTIZADOR. Sistema guarda alcance
-- GLOBAL."). La columna es NULLABLE (no tiene NOT NULL): el CHECK
-- solo restringe el valor cuando viene informado, una fila con
-- alcance NULL sigue siendo válida.
ALTER TABLE catalogos
    ADD CONSTRAINT chk_catalogos_alcance CHECK (alcance IN ('GLOBAL', 'COTIZADOR'));
