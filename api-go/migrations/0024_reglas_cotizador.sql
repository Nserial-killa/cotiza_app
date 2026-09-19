-- ============================================================
-- COTIZA — Migración 0024: motor de Reglas en tiempo real
-- (segundo de 4 huecos del documento de definición funcional del
-- jefe, caso ISA Custom — el más grande de los 4).
--
-- reglas_cotizador es DISTINTA de "reglas" (catálogo global genérico
-- de severidad/momento, migración 0003, sin lógica real — ver el
-- comentario en internal/handlers/reglas.go). Acá sí hay una
-- condición real (un campo + operador + valor de comparación) y una
-- acción concreta sobre uno o más campos objetivo de ESA calculadora:
-- ocultar/mostrar, deshabilitar/habilitar, poner en cero, exigir un
-- mínimo, marcar requerido, o bloquear el guardado completo.
--
-- Ver internal/handlers/reglas_cotizador.go (CRUD, valida que
-- campo_condicion_id y cada campo objetivo pertenezcan a la MISMA
-- calculadora_id — no expresable como CHECK de una sola tabla) y
-- internal/handlers/reglas_evaluacion.go (motor de evaluación puro,
-- sin acceso a base de datos, para poder probarlo con pruebas
-- unitarias simples, mismo criterio que calculo.go).
-- ============================================================

CREATE TABLE reglas_cotizador (
    regla_id                TEXT PRIMARY KEY,
    calculadora_id          TEXT NOT NULL REFERENCES calculadoras(calculadora_id) ON DELETE CASCADE,
    nombre                  TEXT,
    campo_condicion_id      TEXT NOT NULL REFERENCES elementos_tab_cotizador(elemento_id),
    operador                TEXT NOT NULL
                                CHECK (operador IN ('IGUAL_A', 'DISTINTO_DE', 'MAYOR_QUE', 'MENOR_QUE',
                                                     'MAYOR_O_IGUAL_QUE', 'MENOR_O_IGUAL_QUE',
                                                     'ESTA_VACIO', 'NO_ESTA_VACIO')),
    -- Obligatorio salvo ESTA_VACIO/NO_ESTA_VACIO (Go lo valida y limpia
    -- este campo a NULL para esos dos operadores, para no dejar un
    -- valor viejo dando vueltas sin sentido).
    valor_comparacion       TEXT,
    accion                  TEXT NOT NULL
                                CHECK (accion IN ('OCULTAR', 'MOSTRAR', 'DESHABILITAR', 'HABILITAR',
                                                   'PONER_EN_CERO', 'EXIGIR_MINIMO', 'CAMPO_REQUERIDO',
                                                   'BLOQUEAR_GUARDADO')),
    -- Solo tiene sentido (y es obligatorio) para EXIGIR_MINIMO.
    valor_accion            TEXT,
    -- Mensaje a mostrar cuando la regla es de validación
    -- (BLOQUEAR_GUARDADO/CAMPO_REQUERIDO) y se dispara; si viene vacío,
    -- reglas_evaluacion.go arma uno genérico.
    mensaje                 TEXT,
    orden                   INTEGER NOT NULL DEFAULT 0,
    activo                  BOOLEAN NOT NULL DEFAULT true,
    usuario_actualizacion   TEXT REFERENCES usuarios(usuario_id),
    fecha_creacion          TIMESTAMPTZ NOT NULL DEFAULT now(),
    fecha_actualizacion     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_reglas_cotizador_calculadora ON reglas_cotizador(calculadora_id, activo);
CREATE INDEX idx_reglas_cotizador_campo_condicion ON reglas_cotizador(campo_condicion_id);

CREATE TRIGGER trg_reglas_cotizador_fecha
    BEFORE UPDATE ON reglas_cotizador
    FOR EACH ROW EXECUTE FUNCTION set_fecha_actualizacion();

-- Puente regla -> campos objetivo (una regla puede afectar varios
-- campos a la vez, ej. R01 oculta MINUTOS_MES y MINUTOS_EXTRA a la
-- vez). Vacío para BLOQUEAR_GUARDADO, que no apunta a ningún campo —
-- la condición sola ya bloquea el guardado completo.
CREATE TABLE reglas_cotizador_campos_objetivo (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    regla_id                TEXT NOT NULL REFERENCES reglas_cotizador(regla_id) ON DELETE CASCADE,
    elemento_id             TEXT NOT NULL REFERENCES elementos_tab_cotizador(elemento_id),
    orden                   INTEGER NOT NULL DEFAULT 0,
    UNIQUE (regla_id, elemento_id)
);

CREATE INDEX idx_reglas_cotizador_objetivo_regla ON reglas_cotizador_campos_objetivo(regla_id);
CREATE INDEX idx_reglas_cotizador_objetivo_elemento ON reglas_cotizador_campos_objetivo(elemento_id);
