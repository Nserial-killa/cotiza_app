package handlers

// Auditoría de QA — dimensión 7 (Database Integrity).
//
// Estas pruebas NO pasan por los handlers: insertan y borran con SQL
// directo para comprobar que el esquema se defiende solo. La razón es
// que hoy casi toda la validación vive en Go (el handler valida antes
// de llegar a la base), así que si alguien agrega un camino de
// escritura nuevo —una migración de datos, un script, un endpoint
// futuro— la última línea de defensa es el CHECK/FK/UNIQUE de la
// tabla. Estas pruebas fijan esa línea.
//
// Cada afirmación exige el SQLSTATE exacto (23514 CHECK, 23503 FK,
// 23505 UNIQUE, 23502 NOT NULL) y no solo "hubo error": sin eso, un
// typo en el SQL de la prueba la haría pasar por la razón equivocada.
// Por el mismo motivo, cada CHECK prueba además un valor VÁLIDO.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	intgCodigoCheck   = "23514"
	intgCodigoFK      = "23503"
	intgCodigoUnico   = "23505"
	intgCodigoNotNull = "23502"
)

func intgAfirmarViolacion(t *testing.T, err error, codigoEsperado, restriccion string) {
	t.Helper()
	if err == nil {
		t.Fatalf("la base ACEPTÓ un dato que %s tenía que rechazar: esa restricción no está protegiendo la tabla", restriccion)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: se esperaba un error de Postgres y llegó otro tipo de error (%v)", restriccion, err)
	}
	if pgErr.Code != codigoEsperado {
		t.Fatalf("%s: se esperaba SQLSTATE %s y Postgres devolvió %s (%s). "+
			"Cuidado: puede estar fallando por una restricción distinta de la que esta prueba quiere verificar",
			restriccion, codigoEsperado, pgErr.Code, pgErr.Message)
	}
}

func intgAfirmarAceptado(t *testing.T, err error, caso string) {
	t.Helper()
	if err != nil {
		t.Fatalf("la base rechazó %s, que es un caso VÁLIDO: sin este control, la prueba del caso inválido "+
			"podría estar pasando por la razón equivocada. Error: %v", caso, err)
	}
}

func intgContar(t *testing.T, pool *pgxpool.Pool, consulta string, args ...any) int {
	t.Helper()
	var total int
	if err := pool.QueryRow(context.Background(), consulta, args...).Scan(&total); err != nil {
		t.Fatalf("no se pudo contar filas (%s): %v", consulta, err)
	}
	return total
}

// --- fixtures descartables -------------------------------------------------

func intgCrearCalculadora(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := "TEST-INTG-CALC-" + sufijoUnico()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1, 'Cotizador integridad', 'Activo')`, id)
	if err != nil {
		t.Fatalf("no se pudo crear la calculadora de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id = $1`, id)
	})
	return id
}

func intgCrearTab(t *testing.T, pool *pgxpool.Pool, calculadoraID string) string {
	t.Helper()
	id := "TEST-INTG-TAB-" + sufijoUnico()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre) VALUES ($1, $2, 'Tab integridad')`, id, calculadoraID)
	if err != nil {
		t.Fatalf("no se pudo crear el tab de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE tab_id = $1`, id)
	})
	return id
}

func intgCrearPlantilla(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	codigo := "PLT-INTG-" + sufijoUnico()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO plantillas (codigo, nombre) VALUES ($1, 'Plantilla integridad') RETURNING plantilla_id::text`, codigo).Scan(&id)
	if err != nil {
		t.Fatalf("no se pudo crear la plantilla de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM plantillas WHERE plantilla_id::text = $1`, id)
	})
	return id
}

func intgCrearSeccion(t *testing.T, pool *pgxpool.Pool, plantillaID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO plantilla_secciones (plantilla_id, nombre) VALUES ($1::uuid, 'Sección integridad') RETURNING seccion_id::text`,
		plantillaID).Scan(&id)
	if err != nil {
		t.Fatalf("no se pudo crear la sección de prueba: %v", err)
	}
	return id
}

func intgCrearBloque(t *testing.T, pool *pgxpool.Pool, seccionID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO plantilla_bloques (seccion_id, tipo_bloque, nombre_interno)
		 VALUES ($1::uuid, 'TEXTO', 'bloque-integridad') RETURNING bloque_id::text`, seccionID).Scan(&id)
	if err != nil {
		t.Fatalf("no se pudo crear el bloque de prueba: %v", err)
	}
	return id
}

// intgCrearCotizacionConVersion deja una cotización con su versión 1,
// que es el mínimo que exigen las FK compuestas de cotizacion_valores
// y cotizacion_enlaces_publicos.
func intgCrearCotizacionConVersion(t *testing.T, pool *pgxpool.Pool, calculadoraID string) string {
	t.Helper()
	id := "TEST-INTG-COT-" + sufijoUnico()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizaciones (cotizacion_id, calculadora_id, estado, version_actual) VALUES ($1, $2, 'Borrador', 1)`,
		id, calculadoraID); err != nil {
		t.Fatalf("no se pudo crear la cotización de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, estado) VALUES ($1, 1, 'Borrador')`, id); err != nil {
		t.Fatalf("no se pudo crear la versión de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, id)
	})
	return id
}

// =====================================================================
// CHECK
// =====================================================================

func TestIntegridadTabsCotizador_CheckAlcance(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	ctx := context.Background()

	// OJO: este es el CHECK de tabs_cotizador.alcance (0003), con sus
	// propios valores PROPIO/REUTILIZABLE — no confundir con el CHECK
	// de catalogos.alcance (0014), que usa GLOBAL/COTIZADOR y se
	// prueba en TestIntegridadCatalogos_CheckAlcance más abajo.
	for _, valido := range []string{"PROPIO", "REUTILIZABLE"} {
		id := "TEST-INTG-TAB-OK-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, alcance) VALUES ($1, $2, 'Tab', $3)`,
			id, calculadoraID, valido)
		intgAfirmarAceptado(t, err, "alcance "+valido+" en tabs_cotizador")
		pool.Exec(ctx, `DELETE FROM tabs_cotizador WHERE tab_id = $1`, id)
	}

	id := "TEST-INTG-TAB-MAL-" + sufijoUnico()
	_, err := pool.Exec(ctx,
		`INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, alcance) VALUES ($1, $2, 'Tab', 'GLOBAL')`,
		id, calculadoraID)
	pool.Exec(ctx, `DELETE FROM tabs_cotizador WHERE tab_id = $1`, id)
	intgAfirmarViolacion(t, err, intgCodigoCheck, "el CHECK de tabs_cotizador.alcance (alcance='GLOBAL')")
}

func TestIntegridadElementosTab_CheckTipoYColumnasAncho(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	tabID := intgCrearTab(t, pool, calculadoraID)
	ctx := context.Background()

	insertar := func(tipo string, ancho int) error {
		id := "TEST-INTG-ELE-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO elementos_tab_cotizador (elemento_id, tab_id, tipo, columnas_ancho) VALUES ($1, $2, $3, $4)`,
			id, tabID, tipo, ancho)
		pool.Exec(ctx, `DELETE FROM elementos_tab_cotizador WHERE elemento_id = $1`, id)
		return err
	}

	t.Run("tipos válidos aceptados", func(t *testing.T) {
		for _, tipo := range []string{"CAMPO", "CAMPO_CATALOGO", "LEYENDA", "TEXTO_INFORMATIVO"} {
			intgAfirmarAceptado(t, insertar(tipo, 1), "el tipo de elemento "+tipo)
		}
	})

	t.Run("tipo inventado rechazado", func(t *testing.T) {
		intgAfirmarViolacion(t, insertar("CAMPO_MAGICO", 1), intgCodigoCheck,
			"el CHECK de elementos_tab_cotizador.tipo (tipo='CAMPO_MAGICO')")
	})

	t.Run("columnas_ancho válido aceptado", func(t *testing.T) {
		for _, ancho := range []int{1, 2, 3, 4} {
			intgAfirmarAceptado(t, insertar("CAMPO", ancho), "columnas_ancho válido")
		}
	})

	t.Run("columnas_ancho fuera de rango rechazado", func(t *testing.T) {
		for _, ancho := range []int{0, 5, -1} {
			intgAfirmarViolacion(t, insertar("CAMPO", ancho), intgCodigoCheck,
				"el CHECK de elementos_tab_cotizador.columnas_ancho (fuera de 1..4)")
		}
	})
}

func TestIntegridadCotizadoresCompilados_CheckVersionYEstado(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	ctx := context.Background()

	insertar := func(version int, estado string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
			 VALUES ($1, $2, $3, '{}'::jsonb)`, calculadoraID, version, estado)
		return err
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id = $1`, calculadoraID)
	})

	intgAfirmarAceptado(t, insertar(1, "ACTIVA"), "un compilado versión 1 en estado ACTIVA")
	intgAfirmarViolacion(t, insertar(0, "ANTERIOR"), intgCodigoCheck,
		"el CHECK de cotizadores_compilados.version (version=0)")
	intgAfirmarViolacion(t, insertar(-3, "ANTERIOR"), intgCodigoCheck,
		"el CHECK de cotizadores_compilados.version (version negativa)")
	intgAfirmarViolacion(t, insertar(2, "BORRADOR"), intgCodigoCheck,
		"el CHECK de cotizadores_compilados.estado (estado='BORRADOR')")
}

// estadosCotizacionEsquema replica la lista del CHECK de
// 0007_cotizaciones_shell.sql. Si alguien agrega un estado al CHECK y
// no acá, la prueba del caso válido lo deja ver.
var estadosCotizacionEsquema = []string{
	"Borrador", "Revisión Comercial", "Enviada al Cliente", "Vista por el Cliente",
	"Cambios solicitados", "Aceptada", "Ganada", "Perdida", "Vencida", "Cancelada",
}

func TestIntegridadCotizaciones_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	ctx := context.Background()

	for _, estado := range estadosCotizacionEsquema {
		id := "TEST-INTG-COTE-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizaciones (cotizacion_id, calculadora_id, estado) VALUES ($1, $2, $3)`,
			id, calculadoraID, estado)
		intgAfirmarAceptado(t, err, "el estado de cotización "+estado)
		pool.Exec(ctx, `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, id)
	}

	id := "TEST-INTG-COTE-MAL-" + sufijoUnico()
	_, err := pool.Exec(ctx,
		`INSERT INTO cotizaciones (cotizacion_id, calculadora_id, estado) VALUES ($1, $2, 'Inventado')`,
		id, calculadoraID)
	pool.Exec(ctx, `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, id)
	intgAfirmarViolacion(t, err, intgCodigoCheck,
		"el CHECK de cotizaciones.estado (estado='Inventado')")
}

func TestIntegridadCotizacionVersiones_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	cotizacionID := intgCrearCotizacionConVersion(t, pool, calculadoraID)
	ctx := context.Background()

	// La versión 1 ya existe; se prueban estados sobre versiones nuevas.
	numero := 100
	for _, estado := range estadosCotizacionEsquema {
		numero++
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, estado) VALUES ($1, $2, $3)`,
			cotizacionID, numero, estado)
		intgAfirmarAceptado(t, err, "el estado de versión "+estado)
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, estado) VALUES ($1, 999, 'Casi lista')`,
		cotizacionID)
	intgAfirmarViolacion(t, err, intgCodigoCheck,
		"el CHECK de cotizacion_versiones.estado (estado='Casi lista')")
}

func TestIntegridadCotizacionUsuarios_CheckFuncion(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	cotizacionID := intgCrearCotizacionConVersion(t, pool, calculadoraID)
	usuarioID := crearUsuarioPrueba(t, pool, "intg.funcion."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	ctx := context.Background()

	for _, funcion := range []string{"Vendedor", "Analista", "Líder de producto"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1, $2, $3)`,
			cotizacionID, usuarioID, funcion)
		intgAfirmarAceptado(t, err, "la función "+funcion)
		pool.Exec(ctx, `DELETE FROM cotizacion_usuarios WHERE cotizacion_id = $1 AND funcion = $2`, cotizacionID, funcion)
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1, $2, 'Gerente')`,
		cotizacionID, usuarioID)
	intgAfirmarViolacion(t, err, intgCodigoCheck,
		"el CHECK de cotizacion_usuarios.funcion (funcion='Gerente')")
}

func TestIntegridadPlantillas_CheckEstadoYVersion(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado string, version int) error {
		codigo := "PLT-INTG-CHK-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO plantillas (codigo, nombre, estado, version) VALUES ($1, 'Plantilla', $2, $3)`,
			codigo, estado, version)
		pool.Exec(ctx, `DELETE FROM plantillas WHERE codigo = $1`, codigo)
		return err
	}

	for _, estado := range []string{"Borrador", "Publicada", "Archivada"} {
		intgAfirmarAceptado(t, insertar(estado, 1), "el estado de plantilla "+estado)
	}
	intgAfirmarViolacion(t, insertar("Aprobada", 1), intgCodigoCheck,
		"el CHECK de plantillas.estado (estado='Aprobada')")
	intgAfirmarViolacion(t, insertar("Borrador", 0), intgCodigoCheck,
		"el CHECK de plantillas.version (version=0)")
}

func TestIntegridadPlantillaSecciones_CheckVisibilidadDisenoYOrden(t *testing.T) {
	pool := setupTestDB(t)
	plantillaID := intgCrearPlantilla(t, pool)
	ctx := context.Background()

	insertar := func(visibilidad, diseno string, orden int) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO plantilla_secciones (plantilla_id, nombre, visibilidad, diseno_bloques, orden)
			 VALUES ($1::uuid, 'Sección', $2, $3, $4)`, plantillaID, visibilidad, diseno, orden)
		return err
	}

	t.Run("valores válidos aceptados", func(t *testing.T) {
		for _, visibilidad := range []string{"SIEMPRE", "CONDICIONAL"} {
			intgAfirmarAceptado(t, insertar(visibilidad, "UNA", 0), "la visibilidad "+visibilidad)
		}
		for _, diseno := range []string{"UNA", "DOS_50_50", "DOS_30_70", "DOS_70_30", "TRES_IGUALES"} {
			intgAfirmarAceptado(t, insertar("SIEMPRE", diseno, 1), "el diseño de bloques "+diseno)
		}
	})

	t.Run("visibilidad inventada rechazada", func(t *testing.T) {
		intgAfirmarViolacion(t, insertar("A_VECES", "UNA", 0), intgCodigoCheck,
			"el CHECK de plantilla_secciones.visibilidad (visibilidad='A_VECES')")
	})
	t.Run("diseño inventado rechazado", func(t *testing.T) {
		intgAfirmarViolacion(t, insertar("SIEMPRE", "CUATRO_IGUALES", 0), intgCodigoCheck,
			"el CHECK de plantilla_secciones.diseno_bloques (diseno='CUATRO_IGUALES')")
	})
	t.Run("orden negativo rechazado", func(t *testing.T) {
		intgAfirmarViolacion(t, insertar("SIEMPRE", "UNA", -1), intgCodigoCheck,
			"el CHECK de plantilla_secciones.orden (orden=-1)")
	})
}

func TestIntegridadPlantillaBloques_CheckColumnaYOrden(t *testing.T) {
	pool := setupTestDB(t)
	plantillaID := intgCrearPlantilla(t, pool)
	seccionID := intgCrearSeccion(t, pool, plantillaID)
	ctx := context.Background()

	insertar := func(columna, orden int) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO plantilla_bloques (seccion_id, tipo_bloque, nombre_interno, columna, orden)
			 VALUES ($1::uuid, 'TEXTO', 'bloque', $2, $3)`, seccionID, columna, orden)
		return err
	}

	for _, columna := range []int{0, 1, 2, 3} {
		intgAfirmarAceptado(t, insertar(columna, 0), "la columna válida indicada")
	}
	intgAfirmarViolacion(t, insertar(4, 0), intgCodigoCheck,
		"el CHECK de plantilla_bloques.columna (columna=4, fuera de 0..3)")
	intgAfirmarViolacion(t, insertar(-1, 0), intgCodigoCheck,
		"el CHECK de plantilla_bloques.columna (columna=-1)")
	intgAfirmarViolacion(t, insertar(0, -5), intgCodigoCheck,
		"el CHECK de plantilla_bloques.orden (orden=-5)")
}

func TestIntegridadPlantillaVinculaciones_CheckFuenteTipo(t *testing.T) {
	pool := setupTestDB(t)
	calculadoraID := intgCrearCalculadora(t, pool)
	plantillaID := intgCrearPlantilla(t, pool)
	seccionID := intgCrearSeccion(t, pool, plantillaID)
	bloqueID := intgCrearBloque(t, pool, seccionID)
	ctx := context.Background()

	insertar := func(fuenteTipo string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO plantilla_vinculaciones (bloque_id, calculadora_id, fuente_tipo, fuente_id)
			 VALUES ($1::uuid, $2, $3, 'total_precio')`, bloqueID, calculadoraID, fuenteTipo)
		pool.Exec(ctx, `DELETE FROM plantilla_vinculaciones WHERE bloque_id::text = $1`, bloqueID)
		return err
	}

	intgAfirmarAceptado(t, insertar("CAMPO"), "fuente_tipo CAMPO")
	intgAfirmarAceptado(t, insertar("COTIZACION_BASE"), "fuente_tipo COTIZACION_BASE")
	intgAfirmarViolacion(t, insertar("FORMULA"), intgCodigoCheck,
		"el CHECK de plantilla_vinculaciones.fuente_tipo (fuente_tipo='FORMULA')")
}

func TestIntegridadPlantillaEstilos_CheckDeLasCuatroOpciones(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	// Cada caso usa su propia plantilla porque plantilla_estilos tiene
	// UNIQUE sobre plantilla_id (un estilo por plantilla).
	insertar := func(formato, margenes, portada, tablas string) error {
		plantillaID := intgCrearPlantilla(t, pool)
		_, err := pool.Exec(ctx,
			`INSERT INTO plantilla_estilos (plantilla_id, formato_pagina, margenes, diseno_portada, estilo_tablas)
			 VALUES ($1::uuid, $2, $3, $4, $5)`, plantillaID, formato, margenes, portada, tablas)
		return err
	}

	intgAfirmarAceptado(t, insertar("CARTA", "NORMAL", "BANDA_SUPERIOR", "LINEAS"), "los cuatro valores por defecto")
	intgAfirmarAceptado(t, insertar("A4", "AMPLIO", "LATERAL", "TARJETAS"), "una combinación válida no-default")

	casos := []struct {
		nombre                             string
		formato, margenes, portada, tablas string
		restriccion                        string
	}{
		{"formato_pagina", "OFICIO", "NORMAL", "BANDA_SUPERIOR", "LINEAS", "el CHECK de plantilla_estilos.formato_pagina (formato='OFICIO')"},
		{"margenes", "CARTA", "GIGANTES", "BANDA_SUPERIOR", "LINEAS", "el CHECK de plantilla_estilos.margenes (margenes='GIGANTES')"},
		{"diseno_portada", "CARTA", "NORMAL", "CIRCULAR", "LINEAS", "el CHECK de plantilla_estilos.diseno_portada (portada='CIRCULAR')"},
		{"estilo_tablas", "CARTA", "NORMAL", "BANDA_SUPERIOR", "ZEBRA", "el CHECK de plantilla_estilos.estilo_tablas (tablas='ZEBRA')"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			intgAfirmarViolacion(t, insertar(caso.formato, caso.margenes, caso.portada, caso.tablas),
				intgCodigoCheck, caso.restriccion)
		})
	}
}

func TestIntegridadIntegracionesApi_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado string) error {
		nombre := "Integración " + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO integraciones_api (nombre, api_key_hash, estado) VALUES ($1, 'hash', $2)`, nombre, estado)
		pool.Exec(ctx, `DELETE FROM integraciones_api WHERE nombre = $1`, nombre)
		return err
	}

	intgAfirmarAceptado(t, insertar("Activo"), "el estado Activo")
	intgAfirmarAceptado(t, insertar("Inactivo"), "el estado Inactivo")
	intgAfirmarViolacion(t, insertar("Revocada"), intgCodigoCheck,
		"el CHECK de integraciones_api.estado (estado='Revocada')")
}

func TestIntegridadSolicitudes_CheckEstadoYPrioridad(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado, prioridad string) error {
		nombre := "Cliente solicitud " + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO solicitudes (origen, cliente_nombre, estado, prioridad) VALUES ('API_EXTERNA', $1, $2, $3)`,
			nombre, estado, prioridad)
		pool.Exec(ctx, `DELETE FROM solicitudes WHERE cliente_nombre = $1`, nombre)
		return err
	}

	t.Run("estados válidos", func(t *testing.T) {
		for _, estado := range []string{"Nueva", "En revisión", "Descartada", "Convertida"} {
			intgAfirmarAceptado(t, insertar(estado, "Media"), "el estado de solicitud "+estado)
		}
	})
	t.Run("prioridades válidas", func(t *testing.T) {
		for _, prioridad := range []string{"Baja", "Media", "Alta", "Urgente"} {
			intgAfirmarAceptado(t, insertar("Nueva", prioridad), "la prioridad "+prioridad)
		}
	})
	t.Run("estado inventado rechazado", func(t *testing.T) {
		intgAfirmarViolacion(t, insertar("Aprobada", "Media"), intgCodigoCheck,
			"el CHECK de solicitudes.estado (estado='Aprobada')")
	})
	t.Run("prioridad inventada rechazada", func(t *testing.T) {
		intgAfirmarViolacion(t, insertar("Nueva", "Crítica"), intgCodigoCheck,
			"el CHECK de solicitudes.prioridad (prioridad='Crítica')")
	})
}

// Las 5 pruebas que siguen cubren 0014_checks_faltantes.sql (auditoría
// de QA, Tanda 2): clientes/usuarios/calculadoras/crm_conexiones.estado
// y catalogos.alcance no tenían CHECK a nivel de esquema.

func TestIntegridadClientes_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado string) error {
		id := "TEST-INTG-CLI-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO clientes (cliente_id, nombre_comercial, estado) VALUES ($1, 'Cliente integridad', $2)`,
			id, estado)
		pool.Exec(ctx, `DELETE FROM clientes WHERE cliente_id = $1`, id)
		return err
	}

	for _, valido := range []string{"Activo", "Inactivo"} {
		intgAfirmarAceptado(t, insertar(valido), "el estado de cliente "+valido)
	}
	intgAfirmarViolacion(t, insertar("Suspendido"), intgCodigoCheck,
		"el CHECK de clientes.estado (estado='Suspendido')")
}

func TestIntegridadUsuarios_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado string) error {
		id := "TEST-INTG-USR-" + sufijoUnico()
		correo := "integridad." + sufijoUnico() + "@exceltecgroup.com"
		_, err := pool.Exec(ctx,
			`INSERT INTO usuarios (usuario_id, nombre, correo, pin_hash, rol, estado)
			 VALUES ($1, 'Usuario integridad', $2, 'hash-de-prueba', 'Vendedor', $3)`,
			id, correo, estado)
		pool.Exec(ctx, `DELETE FROM usuarios WHERE usuario_id = $1`, id)
		return err
	}

	for _, valido := range []string{"Activo", "Inactivo"} {
		intgAfirmarAceptado(t, insertar(valido), "el estado de usuario "+valido)
	}
	intgAfirmarViolacion(t, insertar("Pendiente"), intgCodigoCheck,
		"el CHECK de usuarios.estado (estado='Pendiente')")
}

func TestIntegridadCalculadoras_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado string) error {
		id := "TEST-INTG-CALCEST-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1, 'Cotizador integridad', $2)`,
			id, estado)
		pool.Exec(ctx, `DELETE FROM calculadoras WHERE calculadora_id = $1`, id)
		return err
	}

	// 'Publicado' es el que escribe compilador.Compilar; 'Borrador' es
	// el filtro que ya expone cotiza_scripts.html.
	for _, valido := range []string{"Activo", "Inactivo", "Borrador", "Publicado"} {
		intgAfirmarAceptado(t, insertar(valido), "el estado de calculadora "+valido)
	}
	intgAfirmarViolacion(t, insertar("Archivado"), intgCodigoCheck,
		"el CHECK de calculadoras.estado (estado='Archivado')")
}

func TestIntegridadCrmConexiones_CheckEstado(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(estado string) error {
		id := "TEST-INTG-CRM-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO crm_conexiones (crm_conexion_id, tipo_crm, estado) VALUES ($1, 'BITRIX24', $2)`,
			id, estado)
		pool.Exec(ctx, `DELETE FROM crm_conexiones WHERE crm_conexion_id = $1`, id)
		return err
	}

	for _, valido := range []string{"Activo", "Inactivo"} {
		intgAfirmarAceptado(t, insertar(valido), "el estado de crm_conexiones "+valido)
	}
	intgAfirmarViolacion(t, insertar("Caducado"), intgCodigoCheck,
		"el CHECK de crm_conexiones.estado (estado='Caducado')")
}

func TestIntegridadCatalogos_CheckAlcance(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	insertar := func(alcance *string) error {
		id := "TEST-INTG-CAT-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO catalogos (catalogo_id, nombre_catalogo, alcance) VALUES ($1, 'Catálogo integridad', $2)`,
			id, alcance)
		pool.Exec(ctx, `DELETE FROM catalogos WHERE catalogo_id = $1`, id)
		return err
	}

	for _, valido := range []string{"GLOBAL", "COTIZADOR"} {
		valido := valido
		intgAfirmarAceptado(t, insertar(&valido), "el alcance de catálogo "+valido)
	}
	// alcance es NULLABLE: el CHECK no debe exigir un valor.
	intgAfirmarAceptado(t, insertar(nil), "un catálogo sin alcance informado (NULL)")
	intgAfirmarViolacion(t, insertar(strPtr("REGIONAL")), intgCodigoCheck,
		"el CHECK de catalogos.alcance (alcance='REGIONAL')")
}

// =====================================================================
// UNIQUE
// =====================================================================

func TestIntegridadUnicos_RechazanDuplicados(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	t.Run("usuarios.correo", func(t *testing.T) {
		correo := "intg.unico." + sufijoUnico() + "@exceltecgroup.com"
		crearUsuarioPrueba(t, pool, correo, "1234", "Vendedor", "Activo")
		id := "test-usr-dup-" + sufijoUnico()
		_, err := pool.Exec(ctx,
			`INSERT INTO usuarios (usuario_id, nombre, correo, pin_hash, rol, estado)
			 VALUES ($1, 'Duplicado', $2, 'hash', 'Vendedor', 'Activo')`, id, correo)
		pool.Exec(ctx, `DELETE FROM usuarios WHERE usuario_id = $1`, id)
		intgAfirmarViolacion(t, err, intgCodigoUnico, "el UNIQUE de usuarios.correo")
	})

	t.Run("plantillas.codigo", func(t *testing.T) {
		codigo := "PLT-INTG-DUP-" + sufijoUnico()
		if _, err := pool.Exec(ctx, `INSERT INTO plantillas (codigo, nombre) VALUES ($1, 'Original')`, codigo); err != nil {
			t.Fatalf("no se pudo crear la plantilla original: %v", err)
		}
		t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM plantillas WHERE codigo = $1`, codigo) })
		_, err := pool.Exec(ctx, `INSERT INTO plantillas (codigo, nombre) VALUES ($1, 'Copia')`, codigo)
		intgAfirmarViolacion(t, err, intgCodigoUnico, "el UNIQUE de plantillas.codigo")
	})

	t.Run("cotizadores_compilados (calculadora_id, version)", func(t *testing.T) {
		calculadoraID := intgCrearCalculadora(t, pool)
		t.Cleanup(func() {
			pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id = $1`, calculadoraID)
		})
		if _, err := pool.Exec(ctx,
			`INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
			 VALUES ($1, 7, 'ANTERIOR', '{}'::jsonb)`, calculadoraID); err != nil {
			t.Fatalf("no se pudo crear el compilado original: %v", err)
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
			 VALUES ($1, 7, 'ANTERIOR', '{}'::jsonb)`, calculadoraID)
		intgAfirmarViolacion(t, err, intgCodigoUnico, "el UNIQUE de cotizadores_compilados (calculadora_id, version)")
	})

	t.Run("plantilla_vinculaciones (bloque_id, calculadora_id)", func(t *testing.T) {
		calculadoraID := intgCrearCalculadora(t, pool)
		plantillaID := intgCrearPlantilla(t, pool)
		seccionID := intgCrearSeccion(t, pool, plantillaID)
		bloqueID := intgCrearBloque(t, pool, seccionID)
		if _, err := pool.Exec(ctx,
			`INSERT INTO plantilla_vinculaciones (bloque_id, calculadora_id, fuente_tipo, fuente_id)
			 VALUES ($1::uuid, $2, 'COTIZACION_BASE', 'total_precio')`, bloqueID, calculadoraID); err != nil {
			t.Fatalf("no se pudo crear la vinculación original: %v", err)
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO plantilla_vinculaciones (bloque_id, calculadora_id, fuente_tipo, fuente_id)
			 VALUES ($1::uuid, $2, 'CAMPO', 'otro-campo')`, bloqueID, calculadoraID)
		intgAfirmarViolacion(t, err, intgCodigoUnico,
			"el UNIQUE de plantilla_vinculaciones (bloque_id, calculadora_id): un bloque no puede tener dos fuentes para el mismo cotizador")
	})

	t.Run("plantilla_estilos.plantilla_id", func(t *testing.T) {
		plantillaID := intgCrearPlantilla(t, pool)
		if _, err := pool.Exec(ctx,
			`INSERT INTO plantilla_estilos (plantilla_id) VALUES ($1::uuid)`, plantillaID); err != nil {
			t.Fatalf("no se pudo crear el estilo original: %v", err)
		}
		_, err := pool.Exec(ctx, `INSERT INTO plantilla_estilos (plantilla_id) VALUES ($1::uuid)`, plantillaID)
		intgAfirmarViolacion(t, err, intgCodigoUnico,
			"el UNIQUE de plantilla_estilos.plantilla_id: una plantilla tiene un solo estilo")
	})

	t.Run("cotizacion_enlaces_publicos (cotizacion_id, version)", func(t *testing.T) {
		calculadoraID := intgCrearCalculadora(t, pool)
		cotizacionID := intgCrearCotizacionConVersion(t, pool, calculadoraID)
		if _, err := pool.Exec(ctx,
			`INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version) VALUES ($1, $2, 1)`,
			"tok-intg-"+sufijoUnico(), cotizacionID); err != nil {
			t.Fatalf("no se pudo crear el enlace original: %v", err)
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version) VALUES ($1, $2, 1)`,
			"tok-intg-dup-"+sufijoUnico(), cotizacionID)
		intgAfirmarViolacion(t, err, intgCodigoUnico,
			"el UNIQUE de cotizacion_enlaces_publicos (cotizacion_id, version): dos tokens para la misma versión")
	})
}

// TestIntegridadCompilados_SoloUnaVersionActivaPorCotizador cubre
// uq_cotizadores_compilados_activa, el índice único PARCIAL de
// 0005_cotizadores_compilados.sql. Es la regla de negocio más
// importante del esquema (el motor de ejecución resuelve "el compilado
// ACTIVA" asumiendo que hay exactamente uno) y hasta ahora solo estaba
// probada por el camino del handler, nunca a nivel de base.
func TestIntegridadCompilados_SoloUnaVersionActivaPorCotizador(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	calculadoraA := intgCrearCalculadora(t, pool)
	calculadoraB := intgCrearCalculadora(t, pool)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id = ANY($1)`,
			[]string{calculadoraA, calculadoraB})
	})

	insertar := func(calculadoraID string, version int, estado string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
			 VALUES ($1, $2, $3, '{}'::jsonb)`, calculadoraID, version, estado)
		return err
	}

	intgAfirmarAceptado(t, insertar(calculadoraA, 1, "ACTIVA"), "el primer compilado ACTIVA del cotizador A")

	// El caso que protege la regla: una segunda ACTIVA del MISMO cotizador.
	intgAfirmarViolacion(t, insertar(calculadoraA, 2, "ACTIVA"), intgCodigoUnico,
		"el índice parcial uq_cotizadores_compilados_activa: quedarían DOS versiones ACTIVA del mismo cotizador y el motor de ejecución no sabría cuál usar")

	// Dos cotizadores distintos sí pueden tener cada uno su ACTIVA.
	intgAfirmarAceptado(t, insertar(calculadoraB, 1, "ACTIVA"),
		"una ACTIVA del cotizador B mientras A ya tiene la suya (el índice es por calculadora_id)")

	// Y un ACTIVA convive con varias ANTERIOR del mismo cotizador.
	intgAfirmarAceptado(t, insertar(calculadoraA, 2, "ANTERIOR"), "una versión ANTERIOR junto a la ACTIVA")
	intgAfirmarAceptado(t, insertar(calculadoraA, 3, "ANTERIOR"), "una segunda versión ANTERIOR")

	activas := intgContar(t, pool,
		`SELECT COUNT(*) FROM cotizadores_compilados WHERE calculadora_id = $1 AND estado = 'ACTIVA'`, calculadoraA)
	if activas != 1 {
		t.Errorf("el cotizador A quedó con %d versiones ACTIVA y debía tener exactamente 1", activas)
	}
}

// =====================================================================
// FOREIGN KEYS
// =====================================================================

func TestIntegridadForeignKeys_RechazanIdsInexistentes(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	inexistenteTexto := "NO-EXISTE-" + sufijoUnico()
	inexistenteUUID := "11111111-2222-3333-4444-555555555555"

	calculadoraID := intgCrearCalculadora(t, pool)
	plantillaID := intgCrearPlantilla(t, pool)
	seccionID := intgCrearSeccion(t, pool, plantillaID)
	bloqueID := intgCrearBloque(t, pool, seccionID)
	cotizacionID := intgCrearCotizacionConVersion(t, pool, calculadoraID)

	casos := []struct {
		nombre      string
		consulta    string
		args        []any
		restriccion string
	}{
		{
			"plantilla_calculadoras.calculadora_id",
			`INSERT INTO plantilla_calculadoras (plantilla_id, calculadora_id) VALUES ($1::uuid, $2)`,
			[]any{plantillaID, inexistenteTexto},
			"la FK plantilla_calculadoras.calculadora_id",
		},
		{
			"plantilla_secciones.plantilla_id",
			`INSERT INTO plantilla_secciones (plantilla_id, nombre) VALUES ($1::uuid, 'Sección huérfana')`,
			[]any{inexistenteUUID},
			"la FK plantilla_secciones.plantilla_id",
		},
		{
			"plantilla_bloques.seccion_id",
			`INSERT INTO plantilla_bloques (seccion_id, tipo_bloque, nombre_interno) VALUES ($1::uuid, 'TEXTO', 'huérfano')`,
			[]any{inexistenteUUID},
			"la FK plantilla_bloques.seccion_id",
		},
		{
			"plantilla_vinculaciones.calculadora_id",
			`INSERT INTO plantilla_vinculaciones (bloque_id, calculadora_id, fuente_tipo, fuente_id)
			 VALUES ($1::uuid, $2, 'CAMPO', 'x')`,
			[]any{bloqueID, inexistenteTexto},
			"la FK plantilla_vinculaciones.calculadora_id",
		},
		{
			"cotizaciones.calculadora_id",
			`INSERT INTO cotizaciones (cotizacion_id, calculadora_id) VALUES ($1, $2)`,
			[]any{"TEST-INTG-FK-COT-" + sufijoUnico(), inexistenteTexto},
			"la FK cotizaciones.calculadora_id",
		},
		{
			"cotizaciones.cliente_id",
			`INSERT INTO cotizaciones (cotizacion_id, calculadora_id, cliente_id) VALUES ($1, $2, $3)`,
			[]any{"TEST-INTG-FK-CLI-" + sufijoUnico(), calculadoraID, inexistenteTexto},
			"la FK cotizaciones.cliente_id",
		},
		{
			"cotizacion_versiones.cotizacion_id",
			`INSERT INTO cotizacion_versiones (cotizacion_id, numero_version) VALUES ($1, 1)`,
			[]any{inexistenteTexto},
			"la FK cotizacion_versiones.cotizacion_id",
		},
		{
			"cotizacion_valores (cotizacion_id, version)",
			`INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, valor) VALUES ($1, 99, 'ele', '"x"'::jsonb)`,
			[]any{cotizacionID},
			"la FK compuesta de cotizacion_valores hacia cotizacion_versiones (versión 99 inexistente)",
		},
		{
			"cotizacion_usuarios.usuario_id",
			`INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1, $2, 'Vendedor')`,
			[]any{cotizacionID, inexistenteTexto},
			"la FK cotizacion_usuarios.usuario_id",
		},
		{
			"cotizacion_historial.usuario_id",
			`INSERT INTO cotizacion_historial (cotizacion_id, accion, usuario_id) VALUES ($1, 'prueba', $2)`,
			[]any{cotizacionID, inexistenteTexto},
			"la FK cotizacion_historial.usuario_id",
		},
		{
			"catalogo_valores.catalogo_id",
			`INSERT INTO catalogo_valores (valor_id, catalogo_id, texto_visible, valor_sistema) VALUES ($1, $2, 'x', 'x')`,
			[]any{"TEST-INTG-VAL-" + sufijoUnico(), inexistenteTexto},
			"la FK catalogo_valores.catalogo_id",
		},
		{
			"solicitudes.integracion_id",
			`INSERT INTO solicitudes (origen, cliente_nombre, integracion_id) VALUES ('API_EXTERNA', 'Cliente', $1::uuid)`,
			[]any{inexistenteUUID},
			"la FK solicitudes.integracion_id",
		},
		{
			"solicitudes.calculadora_id",
			`INSERT INTO solicitudes (origen, cliente_nombre, calculadora_id) VALUES ('API_EXTERNA', 'Cliente', $1)`,
			[]any{inexistenteTexto},
			"la FK solicitudes.calculadora_id",
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			_, err := pool.Exec(ctx, caso.consulta, caso.args...)
			intgAfirmarViolacion(t, err, intgCodigoFK, caso.restriccion)
		})
	}
}

func TestIntegridadNotNull_RechazaColumnasObligatorias(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	calculadoraID := intgCrearCalculadora(t, pool)

	t.Run("cotizaciones.calculadora_id", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO cotizaciones (cotizacion_id, calculadora_id) VALUES ($1, NULL)`,
			"TEST-INTG-NN-"+sufijoUnico())
		intgAfirmarViolacion(t, err, intgCodigoNotNull, "el NOT NULL de cotizaciones.calculadora_id")
	})

	t.Run("solicitudes.cliente_nombre", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO solicitudes (origen, cliente_nombre, calculadora_id) VALUES ('API_EXTERNA', NULL, $1)`,
			calculadoraID)
		intgAfirmarViolacion(t, err, intgCodigoNotNull, "el NOT NULL de solicitudes.cliente_nombre")
	})

	t.Run("clientes.nombre_comercial", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO clientes (cliente_id, nombre_comercial) VALUES ($1, NULL)`,
			"TEST-INTG-NN-CLI-"+sufijoUnico())
		intgAfirmarViolacion(t, err, intgCodigoNotNull, "el NOT NULL de clientes.nombre_comercial")
	})
}

// =====================================================================
// ON DELETE CASCADE
// =====================================================================

func TestIntegridadCascada_BorrarPlantillaArrastraTodoSuArbol(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	calculadoraID := intgCrearCalculadora(t, pool)

	// Árbol completo: plantilla → sección → bloque → vinculación,
	// más estilo y asociaciones de la plantilla.
	codigo := "PLT-INTG-CASC-" + sufijoUnico()
	var plantillaID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plantillas (codigo, nombre) VALUES ($1, 'Plantilla cascada') RETURNING plantilla_id::text`,
		codigo).Scan(&plantillaID); err != nil {
		t.Fatalf("no se pudo crear la plantilla: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM plantillas WHERE plantilla_id::text = $1`, plantillaID)
	})
	seccionID := intgCrearSeccion(t, pool, plantillaID)
	bloqueID := intgCrearBloque(t, pool, seccionID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO plantilla_vinculaciones (bloque_id, calculadora_id, fuente_tipo, fuente_id)
		 VALUES ($1::uuid, $2, 'COTIZACION_BASE', 'total_precio')`, bloqueID, calculadoraID); err != nil {
		t.Fatalf("no se pudo crear la vinculación: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plantilla_estilos (plantilla_id) VALUES ($1::uuid)`, plantillaID); err != nil {
		t.Fatalf("no se pudo crear el estilo: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO plantilla_calculadoras (plantilla_id, calculadora_id) VALUES ($1::uuid, $2)`,
		plantillaID, calculadoraID); err != nil {
		t.Fatalf("no se pudo asociar el cotizador: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO plantilla_tipos_propuesta (plantilla_id, tipo_propuesta) VALUES ($1::uuid, 'Servicios')`,
		plantillaID); err != nil {
		t.Fatalf("no se pudo asociar el tipo de propuesta: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM plantillas WHERE plantilla_id::text = $1`, plantillaID); err != nil {
		t.Fatalf("el borrado de la plantilla falló: %v", err)
	}

	hijos := []struct {
		tabla    string
		consulta string
		arg      any
	}{
		{"plantilla_secciones", `SELECT COUNT(*) FROM plantilla_secciones WHERE plantilla_id::text = $1`, plantillaID},
		{"plantilla_bloques", `SELECT COUNT(*) FROM plantilla_bloques WHERE seccion_id::text = $1`, seccionID},
		{"plantilla_vinculaciones", `SELECT COUNT(*) FROM plantilla_vinculaciones WHERE bloque_id::text = $1`, bloqueID},
		{"plantilla_estilos", `SELECT COUNT(*) FROM plantilla_estilos WHERE plantilla_id::text = $1`, plantillaID},
		{"plantilla_calculadoras", `SELECT COUNT(*) FROM plantilla_calculadoras WHERE plantilla_id::text = $1`, plantillaID},
		{"plantilla_tipos_propuesta", `SELECT COUNT(*) FROM plantilla_tipos_propuesta WHERE plantilla_id::text = $1`, plantillaID},
	}
	for _, hijo := range hijos {
		if total := intgContar(t, pool, hijo.consulta, hijo.arg); total != 0 {
			t.Errorf("quedaron %d filas huérfanas en %s tras borrar la plantilla: falta ON DELETE CASCADE en esa FK", total, hijo.tabla)
		}
	}
}

func TestIntegridadCascada_BorrarCotizacionArrastraVersionesValoresEHistorial(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	calculadoraID := intgCrearCalculadora(t, pool)
	usuarioID := crearUsuarioPrueba(t, pool, "intg.cascada."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")

	cotizacionID := "TEST-INTG-CASC-COT-" + sufijoUnico()
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizaciones (cotizacion_id, calculadora_id, estado, version_actual) VALUES ($1, $2, 'Borrador', 1)`,
		cotizacionID, calculadoraID); err != nil {
		t.Fatalf("no se pudo crear la cotización: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, estado) VALUES ($1, 1, 'Borrador')`,
		cotizacionID); err != nil {
		t.Fatalf("no se pudo crear la versión: %v", err)
	}
	// cotizacion_valores cuelga de la versión por FK compuesta; su cascada
	// es de segundo nivel (cotización → versión → valores).
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, valor) VALUES ($1, 1, 'ele-1', '"hola"'::jsonb)`,
		cotizacionID); err != nil {
		t.Fatalf("no se pudo crear el valor: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_historial (cotizacion_id, numero_version, accion, usuario_id) VALUES ($1, 1, 'creada', $2)`,
		cotizacionID, usuarioID); err != nil {
		t.Fatalf("no se pudo crear el historial: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1, $2, 'Vendedor')`,
		cotizacionID, usuarioID); err != nil {
		t.Fatalf("no se pudo asignar el vendedor: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version) VALUES ($1, $2, 1)`,
		"tok-intg-casc-"+sufijoUnico(), cotizacionID); err != nil {
		t.Fatalf("no se pudo crear el enlace público: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID); err != nil {
		t.Fatalf("el borrado de la cotización falló: %v", err)
	}

	for _, tabla := range []string{
		"cotizacion_versiones", "cotizacion_valores", "cotizacion_historial",
		"cotizacion_usuarios", "cotizacion_enlaces_publicos",
	} {
		total := intgContar(t, pool, `SELECT COUNT(*) FROM `+tabla+` WHERE cotizacion_id = $1`, cotizacionID)
		if total != 0 {
			t.Errorf("quedaron %d filas huérfanas en %s tras borrar la cotización: falta ON DELETE CASCADE en esa FK", total, tabla)
		}
	}
}

func TestIntegridadCascada_BorrarCatalogoArrastraValoresYRelaciones(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	// No se usa crearCatalogoPrueba porque acá el catálogo se borra
	// dentro de la prueba y hace falta controlar el ciclo de vida.
	catalogoPadreID := "TEST-INTG-CAT-P-" + sufijoUnico()
	catalogoHijoID := "TEST-INTG-CAT-H-" + sufijoUnico()
	for _, id := range []string{catalogoPadreID, catalogoHijoID} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO catalogos (catalogo_id, nombre_catalogo, activo) VALUES ($1, 'Catálogo cascada', true)`, id); err != nil {
			t.Fatalf("no se pudo crear el catálogo %s: %v", id, err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM catalogos WHERE catalogo_id = ANY($1)`,
			[]string{catalogoPadreID, catalogoHijoID})
	})

	valorPadreID := "TEST-INTG-VAL-P-" + sufijoUnico()
	valorHijoID := "TEST-INTG-VAL-H-" + sufijoUnico()
	if _, err := pool.Exec(ctx,
		`INSERT INTO catalogo_valores (valor_id, catalogo_id, texto_visible, valor_sistema, activo)
		 VALUES ($1, $2, 'Padre', 'padre', true), ($3, $4, 'Hijo', 'hijo', true)`,
		valorPadreID, catalogoPadreID, valorHijoID, catalogoHijoID); err != nil {
		t.Fatalf("no se pudieron crear los valores: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO catalogo_relaciones (catalogo_padre_id, valor_padre_id, catalogo_hijo_id, valor_hijo_id, activo)
		 VALUES ($1, $2, $3, $4, true)`,
		catalogoPadreID, valorPadreID, catalogoHijoID, valorHijoID); err != nil {
		t.Fatalf("no se pudo crear la relación: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM catalogos WHERE catalogo_id = $1`, catalogoPadreID); err != nil {
		t.Fatalf("el borrado del catálogo padre falló: %v", err)
	}

	if total := intgContar(t, pool, `SELECT COUNT(*) FROM catalogo_valores WHERE catalogo_id = $1`, catalogoPadreID); total != 0 {
		t.Errorf("quedaron %d valores huérfanos tras borrar el catálogo: falta ON DELETE CASCADE", total)
	}
	if total := intgContar(t, pool,
		`SELECT COUNT(*) FROM catalogo_relaciones WHERE catalogo_padre_id = $1 OR valor_padre_id = $2`,
		catalogoPadreID, valorPadreID); total != 0 {
		t.Errorf("quedaron %d relaciones huérfanas tras borrar el catálogo: falta ON DELETE CASCADE", total)
	}
}

// TestIntegridadCliente_NoSeBorraSiTieneCotizaciones documenta el
// comportamiento REAL de cotizaciones.cliente_id: la FK no tiene
// CASCADE (0007_cotizaciones_shell.sql), así que la base impide borrar
// un cliente con cotizaciones. Es la contraparte a nivel de esquema del
// guard de desactivación de ClientesHandler.Editar: por eso la pantalla
// de Clientes desactiva en vez de borrar. Si algún día se le pone
// CASCADE a esa FK, esta prueba falla y obliga a revisar la decisión.
func TestIntegridadCliente_NoSeBorraSiTieneCotizaciones(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	calculadoraID := intgCrearCalculadora(t, pool)

	clienteID := "TEST-INTG-CLI-" + sufijoUnico()
	if _, err := pool.Exec(ctx,
		`INSERT INTO clientes (cliente_id, nombre_comercial, estado) VALUES ($1, 'Cliente con cotización', 'Activo')`,
		clienteID); err != nil {
		t.Fatalf("no se pudo crear el cliente: %v", err)
	}
	cotizacionID := "TEST-INTG-COT-CLI-" + sufijoUnico()
	if _, err := pool.Exec(ctx,
		`INSERT INTO cotizaciones (cotizacion_id, calculadora_id, cliente_id) VALUES ($1, $2, $3)`,
		cotizacionID, calculadoraID, clienteID); err != nil {
		t.Fatalf("no se pudo crear la cotización: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id = $1`, clienteID)
	})

	_, err := pool.Exec(ctx, `DELETE FROM clientes WHERE cliente_id = $1`, clienteID)
	intgAfirmarViolacion(t, err, intgCodigoFK,
		"la FK cotizaciones.cliente_id (sin CASCADE): borrar un cliente con cotizaciones dejaría la cotización sin cliente")
}
